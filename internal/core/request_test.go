package core

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestParseChat(t *testing.T) {
	body := `{"model":"free/coder","stream":true,"max_tokens":200,"temperature":0.2,"x_custom":1,
	  "tools":[{"type":"function","function":{"name":"run","parameters":{}}}],
	  "response_format":{"type":"json_object"},
	  "messages":[{"role":"system","content":"be brief"},
	    {"role":"user","content":[{"type":"text","text":"what is this"},{"type":"image_url","image_url":{"url":"data:x"}}]}]}`
	r, err := ParseChat([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if r.Model != "free/coder" || !r.Stream || r.MaxTokens != 200 || !r.WantsJSON || !r.HasImages || !r.ToolNames["run"] {
		t.Fatalf("parsed %+v", r)
	}
	if r.LastUserText() != "what is this\n" {
		t.Fatalf("last user text %q", r.LastUserText())
	}
	out, _, _ := r.BodyWith("upstream-x", Upstream{})
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	if m["model"] != "upstream-x" || m["x_custom"] != float64(1) || m["temperature"] != 0.2 {
		t.Fatalf("passthrough lost fields: %v", m)
	}
}

func TestParseChatErrors(t *testing.T) {
	for _, b := range []string{`[]`, `{}`, `{"messages":[]}`, `{"messages":"x"}`, `nope`} {
		if _, err := ParseChat([]byte(b)); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s: want ErrBadRequest, got %v", b, err)
		}
	}
}

func TestEstimate(t *testing.T) {
	r, _ := ParseChat([]byte(`{"messages":[{"role":"user","content":"` + string(make([]byte, 0)) + `abcdabcdabcdabcd"}]}`))
	if r.EstTokens != 4+4 {
		t.Fatalf("est %d", r.EstTokens)
	}
}

func TestBodyWithQuirks(t *testing.T) {
	r, _ := ParseChat([]byte(`{"model":"x","stream":true,"max_tokens":50,"logit_bias":{},"stream_options":{"foo":1},"messages":[{"role":"user","content":"hi"}]}`))
	b, added, err := r.BodyWith("up", Upstream{Drop: []string{"logit_bias"}, MaxTokensField: "max_completion_tokens", StreamUsage: true})
	if err != nil || !added {
		t.Fatal(err, added)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if _, ok := m["logit_bias"]; ok {
		t.Fatal("logit_bias not dropped")
	}
	if _, ok := m["max_tokens"]; ok || m["max_completion_tokens"] != float64(50) {
		t.Fatalf("max tokens not renamed: %v", m)
	}
	so := m["stream_options"].(map[string]any)
	if so["include_usage"] != true || so["foo"] != float64(1) {
		t.Fatalf("stream_options %v", so)
	}
	// Client already asked for usage: we did not add it.
	r2, _ := ParseChat([]byte(`{"stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`))
	if _, added, _ := r2.BodyWith("up", Upstream{StreamUsage: true}); added {
		t.Fatal("should not mark usage as added")
	}
	// Non-stream requests never get stream_options.
	r3, _ := ParseChat([]byte(`{"messages":[{"role":"user","content":"hi"}]}`))
	b3, _, _ := r3.BodyWith("up", Upstream{StreamUsage: true})
	if strings.Contains(string(b3), "stream_options") {
		t.Fatal("stream_options on non-stream request")
	}
}

func TestTokenParts(t *testing.T) {
	body := `{"model":"x","tools":[{"type":"function","function":{"name":"read","parameters":{"type":"object"}}}],
	"messages":[{"role":"system","content":"` + strings.Repeat("rule ", 400) + `"},
	{"role":"user","content":"write my resume"},
	{"role":"assistant","content":null,"tool_calls":[{"id":"1","type":"function","function":{"name":"read","arguments":"{\"path\":\"cv.md\"}"}}]},
	{"role":"tool","tool_call_id":"1","content":"` + strings.Repeat("line ", 200) + `"}]}`
	r, err := ParseChat([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	p := r.TokenParts()
	if p["instructions"] < 400 || p["tool_results"] < 200 || p["tool_definitions"] == 0 || p["earlier_answers"] == 0 || p["user"] == 0 {
		t.Fatalf("every kind of input should be counted: %v", p)
	}
	if p["instructions"] <= p["tool_results"] {
		t.Fatalf("the long instructions should weigh most: %v", p)
	}
}

func TestHasSecret(t *testing.T) {
	msg := func(text string) *Request {
		b, _ := json.Marshal(map[string]any{"model": "x", "messages": []map[string]string{{"role": "user", "content": text}}})
		r, err := ParseChat(b)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	for _, s := range []string{
		"my key is nvapi-" + strings.Repeat("Ab3_", 12),
		"GROQ_API_KEY=gsk_" + strings.Repeat("a1B2", 10),
		"-----BEGIN OPENSSH PRIVATE KEY-----\nabc",
		"aws AKIAIOSFODNN7EXAMPLE here",
	} {
		if !msg(s).HasSecret() {
			t.Errorf("missed a secret in %q", s[:20])
		}
	}
	for _, s := range []string{
		"func ask() { return sk-1 }",
		"commit 3f2a9c1e5b7d4e8a9c217d4e5f6a8b90aa11bb22",
		"use the task-runner and a skip-list",
		"the price is Rs 780 a week",
	} {
		if msg(s).HasSecret() {
			t.Errorf("false alarm on %q", s)
		}
	}
}
