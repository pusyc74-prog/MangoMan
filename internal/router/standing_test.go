package router

import (
	"net/http"
	"testing"
	"time"
)

// The whole rule in one table: what each outcome does to a model's place.
func TestRuleFor(t *testing.T) {
	cases := []struct {
		res  attemptResult
		want rule
	}{
		{attemptResult{outcome: "ok"}, rule{clear: true}},
		{attemptResult{outcome: "ok_truncated"}, rule{clear: true}},
		{attemptResult{outcome: "stream_error", status: 200, errMsg: "Service temporarily overloaded"}, rule{strikes: 1, last: busyFor, word: "busy"}},
		{attemptResult{outcome: "server_error", status: http.StatusServiceUnavailable}, rule{strikes: 1, last: busyFor, word: "busy"}},
		{attemptResult{outcome: "timeout"}, rule{strikes: 1, last: silentFor, word: "slow"}},
		{attemptResult{outcome: "garbled"}, rule{strikes: 1, last: garbledFor, word: "garbled"}},
		{attemptResult{outcome: "no_answer"}, rule{strikes: 1, last: busyFor, word: "empty"}},
		{attemptResult{outcome: "model_forbidden"}, rule{strikes: skipAfter, word: "failed"}},
		{attemptResult{outcome: "model_not_found"}, rule{strikes: skipAfter, word: "failed"}},
		{attemptResult{outcome: "server_error", status: 500}, rule{strikes: 1, word: "failed"}},
		{attemptResult{outcome: "stream_error", status: 200}, rule{strikes: 1, word: "failed"}},
		{attemptResult{outcome: "network_error"}, rule{strikes: 1, word: "failed"}},
		{attemptResult{outcome: "read_error"}, rule{strikes: 1, word: "failed"}},
		{attemptResult{outcome: "error_in_200"}, rule{strikes: 1, word: "failed"}},
		{attemptResult{outcome: "not_a_stream"}, rule{strikes: 1, word: "failed"}},
		{attemptResult{outcome: "stream_broken_after_commit"}, rule{strikes: 1, word: "failed"}},
		{attemptResult{outcome: "quality:unparseable"}, rule{strikes: 1, word: "failed"}},
		{attemptResult{outcome: "quality:empty"}, rule{clear: true, word: "failed"}},
		{attemptResult{outcome: "rate_limited"}, rule{word: "busy"}},
		{attemptResult{outcome: "key_rejected"}, rule{word: "failed"}},
		{attemptResult{outcome: "client_error"}, rule{word: "failed"}},
		{attemptResult{outcome: "retryable_408"}, rule{word: "failed"}},
		{attemptResult{outcome: "client_gone"}, rule{word: "failed"}},
	}
	for _, c := range cases {
		if got := ruleFor(c.res); got != c.want {
			t.Errorf("%s %d %q: got %+v, want %+v", c.res.outcome, c.res.status, c.res.errMsg, got, c.want)
		}
	}
}

func TestSkipCycle(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewLine()
	l.SetClock(func() time.Time { return now })
	k := "groq/m"
	fail := rule{strikes: 1}
	l.judge(k, fail)
	l.judge(k, fail)
	if !l.allow(k) || !l.SkippedUntil(k).IsZero() {
		t.Fatal("skipped below three strikes")
	}
	l.judge(k, fail)
	if l.allow(k) || l.SkippedUntil(k).IsZero() {
		t.Fatal("not skipped at three strikes")
	}
	now = now.Add(31 * time.Second)
	if !l.probation(k) || !l.allow(k) {
		t.Fatal("no probe after the skip")
	}
	if l.allow(k) {
		t.Fatal("more than one probe at a time")
	}
	l.judge(k, fail) // the probe failed: skipped again, twice as long
	now = now.Add(31 * time.Second)
	if l.allow(k) {
		t.Fatal("second skip should last 60 s")
	}
	now = now.Add(30 * time.Second)
	if !l.allow(k) {
		t.Fatal("no probe after 60 s")
	}
	l.judge(k, rule{clear: true})
	if l.probation(k) || !l.SkippedUntil(k).IsZero() {
		t.Fatal("an answer did not clear the skip")
	}
	// A probe that proved nothing (a rate limit) lets the next request probe.
	for range skipAfter {
		l.judge(k, fail)
	}
	now = now.Add(time.Hour)
	l.allow(k)
	l.judge(k, rule{word: "busy"})
	if !l.allow(k) {
		t.Fatal("probe not released")
	}
}

// Tried last lasts its time, also after an answer, and then ends.
func TestTriedLastExpires(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewLine()
	l.SetClock(func() time.Time { return now })
	a := Candidate{}
	a.Provider.ID, a.Model.Provider, a.Model.Upstream, a.Model.Canonical = "a", "a", "m1", "m1"
	b := a
	b.Provider.ID, b.Model.Provider = "b", "b"
	l.judge(a.Target(), rule{strikes: 1, last: busyFor})
	l.judge(a.Target(), rule{clear: true})
	if got := l.order([]Candidate{a, b}); got[0].Target() != b.Target() {
		t.Fatalf("tried-last model first: %v", got[0].Target())
	}
	now = now.Add(busyFor)
	if got := l.order([]Candidate{a, b}); got[0].Target() != a.Target() {
		t.Fatalf("tried last after it ended: %v", got[0].Target())
	}
}
