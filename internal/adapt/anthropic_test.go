package adapt

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnthropicSystemMessagesInConversation(t *testing.T) {
	body := `{"model":"claude-opus-5-5","max_tokens":100,"system":"Base.","messages":[
	  {"role":"system","content":"Early note."},
	  {"role":"user","content":[{"type":"text","text":"hi"}]},
	  {"role":"system","content":[{"type":"text","text":"Reminder A."}]},
	  {"role":"assistant","content":"hello"},
	  {"role":"system","content":"Reminder B."}]}`
	chat, _, err := AnthropicToChat([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Messages []struct {
			Role    string
			Content string
		}
	}
	_ = json.Unmarshal(chat, &out)
	roles := []string{}
	for _, m := range out.Messages {
		roles = append(roles, m.Role)
	}
	if strings.Join(roles, ",") != "system,user,assistant,user" {
		t.Fatalf("roles %v", roles)
	}
	if out.Messages[0].Content != "Base.\n\nEarly note." || out.Messages[1].Content != "hi\n\nReminder A." || out.Messages[3].Content != "Reminder B." {
		t.Fatalf("contents %+v", out.Messages)
	}
	if _, _, err := AnthropicToChat([]byte(`{"model":"x","max_tokens":1,"messages":[{"role":"robot","content":"x"}]}`)); err == nil {
		t.Fatal("unknown role should fail")
	}
}

func TestMapClientModelKeepsCatalogueNames(t *testing.T) {
	for in, want := range map[string]string{
		"gpt-oss-120b":      "gpt-oss-120b",
		"o3-mini":           "free/fast",
		"o1-mini":           "free/fast",
		"claude-haiku-4-5":  "free/fast",
		"claude-sonnet-4-5": "free/coder",
		"gpt-5":             "free/coder",
		"llama-3.3-70b":     "llama-3.3-70b",
		"":                  "free/auto",
	} {
		if got := mapClientModel(in); got != want {
			t.Errorf("mapClientModel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAnthropicStreamKeepsInterleavedToolArgs(t *testing.T) {
	c := NewAnthropicCodec(AnthropicRequest{Model: "claude-x"}, 10)
	var all []Event
	for _, ch := range []string{
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c0","type":"function","function":{"name":"a","arguments":""}},{"index":1,"id":"c1","type":"function","function":{"name":"b","arguments":""}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"x\":1}"}},{"index":1,"function":{"arguments":"{\"y\":2}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	} {
		all = append(all, c.Chunk([]byte(ch))...)
	}
	all = append(all, c.Done()...)
	args := map[any]string{}
	for _, e := range all {
		m := e.Data.(map[string]any)
		if d, ok := m["delta"].(map[string]any); ok && d["type"] == "input_json_delta" {
			args[m["index"]] += d["partial_json"].(string)
		}
	}
	if args[0] != `{"x":1}` || args[1] != `{"y":2}` {
		t.Fatalf("args %v", args)
	}
}

func TestResponsesTextThenCallIsOneAssistantMessage(t *testing.T) {
	body := `{"model":"free/coder","input":[
 {"type":"message","role":"user","content":"fix it"},
 {"type":"message","role":"assistant","content":[{"type":"output_text","text":"Let me look."}]},
 {"type":"function_call","call_id":"c1","name":"shell","arguments":"{}"},
 {"type":"function_call_output","call_id":"c1","output":"ok"}]}`
	out, _, err := ResponsesToChat([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var chat struct {
		Messages []struct {
			Role      string `json:"role"`
			Content   any    `json:"content"`
			ToolCalls []any  `json:"tool_calls"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(out, &chat)
	if len(chat.Messages) != 3 || chat.Messages[1].Content != "Let me look." || len(chat.Messages[1].ToolCalls) != 1 {
		t.Fatalf("%s", out)
	}
}
