package core

import (
	"encoding/json"
	"errors"
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
	out, _ := r.BodyFor("upstream-x")
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
