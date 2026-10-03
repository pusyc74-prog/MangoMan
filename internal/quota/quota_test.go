package quota

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/catalogue"
)

func TestAllowRecordAndReset(t *testing.T) {
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	tr := New()
	tr.SetClock(func() time.Time { return now })
	k := Key{"groq", "default", "m"}
	l := catalogue.Limits{RPM: 2, TPD: 1000}
	for i := 0; i < 2; i++ {
		if ok, _ := tr.Allow(k, l, 10); !ok {
			t.Fatalf("request %d blocked", i)
		}
		tr.Record(k, 10)
	}
	ok, reset := tr.Allow(k, l, 10)
	if ok || !reset.Equal(now.Add(time.Minute)) {
		t.Fatalf("want blocked until +1m, got ok=%v reset=%v", ok, reset)
	}
	if s := tr.Share(k, l); s != 0 {
		t.Fatalf("share %v, want 0", s)
	}
	now = now.Add(61 * time.Second)
	if ok, _ := tr.Allow(k, l, 10); !ok {
		t.Fatal("minute window should have reset")
	}
	// Daily token budget.
	if ok, _ := tr.Allow(k, l, 990); ok {
		t.Fatal("daily tokens should block 20+990 > 1000")
	}
}

func TestOversizedRequestNotBlockedByTPM(t *testing.T) {
	tr := New()
	if ok, _ := tr.Allow(Key{"a", "d", "m"}, catalogue.Limits{TPM: 100}, 500); !ok {
		t.Fatal("a request larger than TPM should be left to the provider")
	}
}

func TestBlockAndHeaders(t *testing.T) {
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	tr := New()
	tr.SetClock(func() time.Time { return now })
	k := Key{"groq", "default", "m"}
	h := http.Header{}
	h.Set("x-ratelimit-remaining-tokens", "0")
	h.Set("x-ratelimit-reset-tokens", "7.5s")
	tr.FromHeaders(k, h, nil)
	ok, reset := tr.Allow(k, catalogue.Limits{}, 1)
	if ok || !reset.Equal(now.Add(7500*time.Millisecond)) {
		t.Fatalf("got ok=%v reset=%v", ok, reset)
	}
}

func TestParseReset(t *testing.T) {
	cases := map[string]time.Duration{"2m59.5s": 2*time.Minute + 59500*time.Millisecond, "30": 30 * time.Second, "1.5": 1500 * time.Millisecond, "": 0, "junk": 0}
	for in, want := range cases {
		if got := ParseReset(in); got != want {
			t.Errorf("ParseReset(%q) = %v, want %v", in, got, want)
		}
	}
	h := http.Header{}
	h.Set("Retry-After", "12")
	if RetryAfter(h) != 12*time.Second {
		t.Fatal("Retry-After seconds")
	}
	if RetryAfter(http.Header{}) != time.Minute {
		t.Fatal("default retry")
	}
}

func TestSaveLoad(t *testing.T) {
	tr := New()
	k := Key{"groq", "default", "m"}
	tr.Record(k, 50)
	p := filepath.Join(t.TempDir(), "q.json")
	if err := tr.Save(p); err != nil {
		t.Fatal(err)
	}
	tr2 := New()
	if err := tr2.Load(p); err != nil {
		t.Fatal(err)
	}
	snap := tr2.Snapshot()
	if len(snap) != 1 || snap[0].TokToday != 50 || snap[0].ReqToday != 1 {
		t.Fatalf("got %+v", snap)
	}
	if err := New().Load(filepath.Join(t.TempDir(), "missing.json")); err != nil {
		t.Fatal("missing file should be fine")
	}
}

func TestLearnFromHeaders(t *testing.T) {
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	tr := New()
	tr.SetClock(func() time.Time { return now })
	k := Key{"groq", "default", "m"}
	rules := []catalogue.RateHeader{
		{Kind: "requests", Window: "day", Limit: "x-ratelimit-limit-requests", Remaining: "x-ratelimit-remaining-requests", Reset: "x-ratelimit-reset-requests"},
		{Kind: "tokens", Window: "minute", Limit: "x-ratelimit-limit-tokens", Remaining: "x-ratelimit-remaining-tokens", Reset: "x-ratelimit-reset-tokens"},
	}
	h := http.Header{}
	h.Set("x-ratelimit-limit-requests", "14400")
	h.Set("x-ratelimit-remaining-requests", "14370")
	h.Set("x-ratelimit-reset-requests", "2m59.56s")
	h.Set("x-ratelimit-limit-tokens", "18000")
	h.Set("x-ratelimit-remaining-tokens", "17000")
	h.Set("x-ratelimit-reset-tokens", "7.66s")
	tr.FromHeaders(k, h, rules)
	if got := tr.Learned(k); got.RPD != 14400 || got.TPM != 18000 {
		t.Fatalf("learned %+v", got)
	}
	// The seed said RPD 1000; the learned 14400 wins, and 30 are used.
	seed := catalogue.Limits{RPD: 1000, TPM: 6000}
	if ok, _ := tr.Allow(k, seed, 500); !ok {
		t.Fatal("learned limits should allow this")
	}
	if s := tr.Share(k, seed); s > 0.95 || s < 0.9 {
		t.Fatalf("share %v, want about 1000/18000 used", s)
	}
	// Tokens exhausted: blocked until the reported reset.
	h.Set("x-ratelimit-remaining-tokens", "0")
	tr.FromHeaders(k, h, rules)
	ok, reset := tr.Allow(k, seed, 1)
	if ok || !reset.Equal(now.Add(7660*time.Millisecond)) {
		t.Fatalf("got ok=%v reset=%v", ok, reset)
	}
}

func TestSnapshotDropsLastDaysCounts(t *testing.T) {
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	tr := New()
	tr.SetClock(func() time.Time { return now })
	tr.Record(Key{Provider: "groq", Account: "default", Model: "m"}, 100)
	now = now.Add(25 * time.Hour)
	if s := tr.Snapshot()[0]; s.ReqToday != 0 || s.TokToday != 0 {
		t.Fatalf("stale counts: %+v", s)
	}
}
