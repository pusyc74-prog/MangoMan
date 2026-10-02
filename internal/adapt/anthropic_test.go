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
