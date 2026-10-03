package classify

import (
	"strings"
	"testing"

	"github.com/pusyc74-prog/mangoman/internal/core"
)

func TestClassify(t *testing.T) {
	cases := map[string]string{
		`{"model":"free/fast","messages":[{"role":"user","content":"fix this python bug"}]}`:                  Fast,
		`{"messages":[{"role":"user","content":"fix this python bug"}]}`:                                      Code,
		`{"messages":[{"role":"user","content":"explain step by step why the sky is blue"}]}`:                 Reasoning,
		`{"messages":[{"role":"user","content":"write a warm birthday note"}]}`:                               Writing,
		`{"response_format":{"type":"json_object"},"messages":[{"role":"user","content":"names"}]}`:           Extraction,
		`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"x"}}]}]}`:             Vision,
		`{"tools":[{"type":"function","function":{"name":"f"}}],"messages":[{"role":"user","content":"hi"}]}`: Code,
	}
	for body, want := range cases {
		r, err := core.ParseChat([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := ClassifySure(r); got != want {
			t.Errorf("%s: got %s want %s", body, got, want)
		}
	}
	long, _ := core.ParseChat([]byte(`{"messages":[{"role":"user","content":"` + strings.Repeat("a ", 60000) + `"}]}`))
	if c, _ := ClassifySure(long); c != LongContext {
		t.Error("long input should be long-context")
	}
}
