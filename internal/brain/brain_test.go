package brain

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func reply(content string) []byte {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	return b
}

func fixed(content string, calls *atomic.Int32) Caller {
	return func(ctx context.Context, body []byte) (int, []byte, error) {
		calls.Add(1)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		if req["response_format"] == nil || req["model"] == nil {
			return 400, nil, nil
		}
		return 200, reply(content), nil
	}
}

func TestParsing(t *testing.T) {
	opts := []string{"code", "writing"}
	cases := []struct {
		content string
		want    string
		conf    float64
		bad     bool
	}{
		{`{"answer":"code","confidence":0.92}`, "code", 0.92, false},
		{"```json\n{\"answer\": \"Writing\", \"confidence\": 80}\n```", "writing", 0.8, false},
		{`Sure! {"answer":"code"}`, "code", 0.5, false},
		{`{"answer":"poetry","confidence":0.9}`, "", 0, true},
		{`no json here`, "", 0, true},
	}
	for _, c := range cases {
		a, conf, err := parseAnswer(reply(c.content), opts)
		if (err != nil) != c.bad || a != c.want || conf != c.conf {
			t.Errorf("%q: got %q %v %v", c.content, a, conf, err)
		}
	}
	if a, _, err := parseAnswer(reply(`{"answer":true,"confidence":0.9}`), []string{"yes", "no"}); err != nil || a != "yes" {
		t.Errorf("boolean answer: %q %v", a, err)
	}
}

func TestDecisionsCacheAndStats(t *testing.T) {
	var calls atomic.Int32
	b := &Brain{Call: fixed(`{"answer":"code","confidence":0.9}`, &calls), Model: "free/fast"}
	for i := 0; i < 3; i++ {
		a, conf, ok := b.Choose(context.Background(), "task", "conv-1", "q", []string{"code", "writing"})
		if !ok || a != "code" || conf != 0.9 {
			t.Fatalf("choose: %q %v %v", a, conf, ok)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("cached decision asked %d times", calls.Load())
	}
	s := b.Snapshot()
	if s.Calls != 1 || s.Decided != 1 || s.CacheHits != 2 || s.Model != "free/fast" || len(s.Recent) != 3 || !s.Recent[0].Cached {
		t.Fatalf("stats %+v", s)
	}
}

func TestUnsureTimeoutAndErrorFallBack(t *testing.T) {
	var calls atomic.Int32
	low := &Brain{Call: fixed(`{"answer":"yes","confidence":0.3}`, &calls)}
	if _, _, ok := low.YesNo(context.Background(), "non_answer", "", "q"); ok {
		t.Fatal("low confidence must not decide")
	}
	slow := &Brain{Budget: 50 * time.Millisecond, Call: func(ctx context.Context, _ []byte) (int, []byte, error) {
		select {
		case <-ctx.Done():
			return 0, nil, ctx.Err()
		case <-time.After(2 * time.Second):
			return 200, reply(`{"answer":"yes","confidence":1}`), nil
		}
	}}
	start := time.Now()
	if _, _, ok := slow.YesNo(context.Background(), "non_answer", "", "q"); ok || time.Since(start) > time.Second {
		t.Fatalf("timeout should fall back fast, took %v", time.Since(start))
	}
	if r := slow.Snapshot().Recent[0]; r.Fallback != "timeout" {
		t.Fatalf("fallback reason %+v", r)
	}
	failing := &Brain{Call: func(context.Context, []byte) (int, []byte, error) { return 429, nil, nil }}
	if _, _, ok := failing.Choose(context.Background(), "task", "k", "q", []string{"a"}); ok {
		t.Fatal("error must not decide")
	}
	// An error is not cached: the next call tries again.
	if _, _, hit := failing.cached("k"); hit {
		t.Fatal("errors must not be cached")
	}
	var nilBrain *Brain
	if _, _, ok := nilBrain.Choose(context.Background(), "task", "", "q", []string{"a"}); ok {
		t.Fatal("nil brain decides nothing")
	}
	if !strings.HasPrefix(Key("a", "b"), "") || Key("a", "b") == Key("ab") {
		t.Fatal("key must separate parts")
	}
}
