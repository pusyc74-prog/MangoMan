package arch

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// signed.json comes from the server's own test (node server/arch/test.mjs
// --fixture): a bundle signed by the Worker code must verify here.
func fixture(t *testing.T) (Signed, ed25519.PublicKey) {
	t.Helper()
	var f struct {
		PublicKey string `json:"public_key"`
		Signed    Signed `json:"signed"`
	}
	b, err := os.ReadFile("testdata/signed.json")
	if err != nil || json.Unmarshal(b, &f) != nil {
		t.Fatal(err)
	}
	pub, _ := base64.StdEncoding.DecodeString(f.PublicKey)
	return f.Signed, pub
}

func TestVerifyServerBundle(t *testing.T) {
	s, pub := fixture(t)
	b, err := Verify(s, pub)
	if err != nil || len(b.Lessons) != 1 || b.Lessons[0].Target != "nvidia/kimi-k3" || b.Lessons[0].Kind != "demote" {
		t.Fatalf("%v %+v", err, b)
	}
	bad := s
	data, _ := base64.StdEncoding.DecodeString(s.Data)
	data[10] ^= 1
	bad.Data = base64.StdEncoding.EncodeToString(data)
	if _, err := Verify(bad, pub); err == nil {
		t.Fatal("a changed bundle must not verify")
	}
	other, _, _ := ed25519.GenerateKey(nil)
	if _, err := Verify(s, other); err == nil {
		t.Fatal("another key must not verify")
	}
}

type fake struct {
	demoted map[string]time.Time
	share   bool
	counts  map[string]map[string]int
}

func (f *fake) client(url, dir string, pub ed25519.PublicKey, now time.Time) *Client {
	f.demoted = map[string]time.Time{}
	return &Client{URL: url, Key: pub, Dir: dir, Now: func() time.Time { return now },
		Known:  func(t string) bool { return t != "ollama/llama" },
		Demote: func(t string, until time.Time) { f.demoted[t] = until },
		Tally:  func() map[string]map[string]int { c := f.counts; f.counts = nil; return c },
		Share:  func() bool { return f.share }}
}

func TestSync(t *testing.T) {
	s, pub := fixture(t)
	b, _ := Verify(s, pub)
	var got []Report
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/report":
			var rep Report
			_ = json.NewDecoder(r.Body).Decode(&rep)
			got = append(got, rep)
		case "/v1/lessons":
			_ = json.NewEncoder(w).Encode(s)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	f := &fake{counts: map[string]map[string]int{"nvidia/kimi-k3": {"slow": 3}, "ollama/llama": {"ok": 2}}}
	c := f.client(srv.URL, dir, pub, b.Issued.Add(time.Minute))

	// Not agreed: counts are dropped, nothing is sent, lessons still apply.
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || f.counts != nil {
		t.Fatalf("sent without agreement: %+v", got)
	}
	if until := f.demoted["nvidia/kimi-k3"]; !until.Equal(b.Lessons[0].Until) {
		t.Fatalf("demoted %v", f.demoted)
	}
	if _, in := c.Status(); len(in) != 1 {
		t.Fatalf("status %v", in)
	}

	// Agreed: only catalogue cloud models are sent, under the same random id.
	f.share = true
	for range 2 {
		f.counts = map[string]map[string]int{"nvidia/kimi-k3": {"slow": 3}, "ollama/llama": {"ok": 2}}
		if err := c.Sync(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 2 || len(got[0].Models) != 1 || got[0].Models["nvidia/kimi-k3"]["slow"] != 3 ||
		len(got[0].Install) != 32 || got[0].Install != got[1].Install {
		t.Fatalf("%+v", got)
	}

	// After restart, the saved lessons apply before the first sync; expired
	// ones and lessons longer than MaxLife are cut.
	f2 := &fake{}
	c2 := f2.client("http://127.0.0.1:1", dir, pub, b.Issued.Add(time.Hour))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c2.Run(ctx, time.Hour, func(string, ...any) {})
	if _, ok := f2.demoted["nvidia/kimi-k3"]; !ok {
		t.Fatalf("cached lessons not applied: %v", f2.demoted)
	}
	late := &fake{}
	late.client("", dir, pub, b.Issued.Add(MaxLife+time.Minute)).apply(b)
	if len(late.demoted) != 0 {
		t.Fatalf("expired lesson applied: %v", late.demoted)
	}
	long := Bundle{Issued: b.Issued, Lessons: []Lesson{{Kind: "demote", Target: "groq/x", Until: b.Issued.Add(48 * time.Hour)},
		{Kind: "block", Target: "groq/y", Until: b.Issued.Add(time.Hour)}, {Kind: "demote", Target: "ollama/llama", Until: b.Issued.Add(time.Hour)}}}
	f3 := &fake{}
	f3.client("", dir, pub, b.Issued).apply(long)
	if len(f3.demoted) != 1 || !f3.demoted["groq/x"].Equal(b.Issued.Add(MaxLife)) {
		t.Fatalf("%v", f3.demoted)
	}
	if New(dir) != nil {
		t.Fatal("no server in this build: off")
	}
	if _, err := os.Stat(filepath.Join(dir, "arch-id")); err != nil {
		t.Fatal(err)
	}
	// A new day, a new id.
	f4 := &fake{}
	c4 := f4.client("", dir, pub, b.Issued.Add(24*time.Hour))
	if id, _ := c4.install(); id == got[0].Install || len(id) != 32 {
		t.Fatalf("id kept across days: %s", id)
	}
}
