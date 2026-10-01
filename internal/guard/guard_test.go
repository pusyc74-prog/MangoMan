package guard

import (
	"testing"

	"github.com/pusyc74-prog/mangoman/internal/core"
)

func req(t *testing.T, body string) *core.Request {
	t.Helper()
	r, err := core.ParseChat([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCheck(t *testing.T) {
	plain := req(t, `{"messages":[{"role":"user","content":"hi"}]}`)
	capped := req(t, `{"max_tokens":5,"messages":[{"role":"user","content":"hi"}]}`)
	js := req(t, `{"response_format":{"type":"json_object"},"messages":[{"role":"user","content":"hi"}]}`)
	tools := req(t, `{"tools":[{"type":"function","function":{"name":"f"}}],"messages":[{"role":"user","content":"hi"}]}`)

	tc := func(name, args string) []ToolCall {
		var c ToolCall
		c.Function.Name, c.Function.Arguments = name, args
		return []ToolCall{c}
	}
	cases := []struct {
		name string
		r    *core.Request
		a    Answer
		want string
	}{
		{"ok", plain, Answer{Content: "hello", FinishReason: "stop"}, ""},
		{"empty", plain, Answer{Content: "  \n"}, Empty},
		{"truncated", plain, Answer{Content: "partial", FinishReason: "length"}, Truncated},
		{"user cap is fine", capped, Answer{Content: "partial", FinishReason: "length"}, ""},
		{"json ok", js, Answer{Content: `{"a":1}`}, ""},
		{"json fenced ok", js, Answer{Content: "```json\n{\"a\":1}\n```"}, ""},
		{"json bad", js, Answer{Content: "sure! {a:1}"}, InvalidJSON},
		{"tool ok", tools, Answer{ToolCalls: tc("f", `{"x":1}`)}, ""},
		{"tool unknown", tools, Answer{ToolCalls: tc("g", `{}`)}, BadToolCall},
		{"tool bad args", tools, Answer{ToolCalls: tc("f", `{x:1`)}, BadToolCall},
	}
	for _, c := range cases {
		if got := Check(c.r, c.a); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestParseChatResponse(t *testing.T) {
	a, ok := ParseChatResponse([]byte(`{"choices":[{"message":{"content":"hi"},"finish_reason":"stop"}]}`))
	if !ok || a.Content != "hi" || a.FinishReason != "stop" {
		t.Fatalf("%+v", a)
	}
	if _, ok := ParseChatResponse([]byte(`{"choices":[]}`)); ok {
		t.Fatal("no choices should fail")
	}
}
