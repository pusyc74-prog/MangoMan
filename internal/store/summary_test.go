package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSummarize(t *testing.T) {
	p := filepath.Join(t.TempDir(), "usage.jsonl")
	l, err := OpenLog(p)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ev := []Event{
		{Time: now.Add(-48 * time.Hour), RequestID: "old", Provider: "groq", Model: "m", Outcome: "ok", Attempt: 1},
		{Time: now, RequestID: "r1", Provider: "groq", Model: "m", Outcome: "ok", Attempt: 1, LatencyMS: 100, PromptTok: 10, OutputTok: 5},
		{Time: now, RequestID: "r2", Provider: "groq", Model: "m", Outcome: "rate_limited", Attempt: 1, LatencyMS: 20},
		{Time: now, RequestID: "r2", Provider: "cerebras", Model: "m", Outcome: "ok", Attempt: 2, LatencyMS: 300},
		{Time: now, RequestID: "r3", Provider: "groq", Model: "m", Outcome: "quality:empty", Attempt: 1},
		{Time: now, RequestID: "r3", Provider: "nvidia", Model: "m", Outcome: "server_error", Attempt: 2},
	}
	for _, e := range ev {
		l.Add(e)
	}
	l.Close()

	s, err := Summarize(p, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if s.Requests != 3 || s.Served != 2 || s.FailedOver != 1 {
		t.Fatalf("summary %+v", s)
	}
	g := s.Rows[0]
	if g.Provider != "groq" || g.Attempts != 3 || g.OK != 1 || g.RateLimited != 1 || g.QualityFail != 1 || g.Tokens != 15 {
		t.Fatalf("groq row %+v", g)
	}
	if s2, _ := Summarize(filepath.Join(t.TempDir(), "none"), now); s2.Requests != 0 {
		t.Fatal("missing log should be empty")
	}
}
