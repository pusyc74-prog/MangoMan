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
		{Time: now, RequestID: "r1", Provider: "groq", Model: "m", Outcome: "ok", Attempt: 1, LatencyMS: 100, Tokens: 15, Parts: map[string]int{"instructions": 9000, "user": 20}},
		{Time: now, RequestID: "r2", Provider: "groq", Model: "m", Outcome: "rate_limited", Attempt: 1, LatencyMS: 20},
		{Time: now, RequestID: "r2", Provider: "cerebras", Model: "m", Outcome: "ok", Attempt: 2, LatencyMS: 300},
		{Time: now, RequestID: "r3", Provider: "groq", Model: "m", Outcome: "quality:empty", Attempt: 1},
		{Time: now, RequestID: "r3", Provider: "nvidia", Model: "m", Outcome: "server_error", Attempt: 2},
		{Time: now, RequestID: "b1", Provider: "groq", Model: "m", Class: BrainClass, Outcome: "ok", Attempt: 1},
	}
	for _, e := range ev {
		l.Add(e)
	}
	l.Close()

	s, err := Summarize(p, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if s.Requests != 3 || s.Served != 2 || s.FailedOver != 1 || s.BrainCalls != 1 {
		t.Fatalf("summary %+v", s)
	}
	g := s.Rows[0]
	if s.Parts["instructions"] != 9000 || s.Parts["user"] != 20 {
		t.Fatalf("parts not summed: %v", s.Parts)
	}
	if g.Provider != "groq" || g.Attempts != 4 || g.OK != 2 || g.RateLimited != 1 || g.QualityFail != 1 || g.Tokens != 15 {
		t.Fatalf("groq row %+v", g)
	}
	if s2, _ := Summarize(filepath.Join(t.TempDir(), "none"), now); s2.Requests != 0 {
		t.Fatal("missing log should be empty")
	}
}

func TestAnalyze(t *testing.T) {
	p := filepath.Join(t.TempDir(), "usage.jsonl")
	l, _ := OpenLog(p)
	base := time.Now().UTC().Truncate(time.Hour)
	l.Add(Event{Time: base.Add(-2 * time.Hour), RequestID: "1", Provider: "groq", Model: "m", Outcome: "ok", Attempt: 1})
	l.Add(Event{Time: base.Add(-2*time.Hour + time.Minute), RequestID: "2", Provider: "groq", Model: "m", Outcome: "rate_limited", Attempt: 1})
	l.Add(Event{Time: base.Add(-2*time.Hour + time.Minute), RequestID: "2", Provider: "nvidia", Model: "m", Outcome: "ok", Attempt: 2})
	l.Add(Event{Time: base.Add(time.Minute), RequestID: "3", Provider: "groq", Model: "m", Outcome: "ok", Attempt: 1})
	l.Add(Event{Time: base.Add(2 * time.Minute), RequestID: "b", Provider: "groq", Model: "m", Class: BrainClass, Outcome: "ok", Attempt: 1})
	l.Close()
	a, err := Analyze(p, base.Add(-24*time.Hour), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Hourly) != 3 || a.Hourly[0].Provider != "groq" || a.Hourly[0].Attempts != 2 || a.Hourly[0].OK != 1 {
		t.Fatalf("hourly %+v", a.Hourly)
	}
	if len(a.Recent) != 2 || a.Recent[0].RequestID != "3" {
		t.Fatalf("recent %+v", a.Recent)
	}
	if a.Requests != 3 || a.FailedOver != 1 {
		t.Fatalf("summary %+v", a.Summary)
	}
}
