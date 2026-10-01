// Package guard is the quality guard: cheap local checks that catch bad
// answers so the router can retry on the next candidate. Phase 1 ships the
// objective checks (empty, truncated, invalid JSON, malformed tool calls).
// Refusal detection lands in M4 behind a setting, to avoid false positives.
package guard

import (
	"encoding/json"
	"strings"

	"github.com/pusyc74-prog/mangoman/internal/core"
)

// Failure reasons.
const (
	Empty       = "empty"
	Truncated   = "truncated"
	InvalidJSON = "invalid_json"
	BadToolCall = "bad_tool_call"
	Unparseable = "unparseable"
)

// ToolCall is the subset of a tool call the guard checks.
type ToolCall struct {
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Answer is a normalised view of a model answer.
type Answer struct {
	Content      string
	ToolCalls    []ToolCall
	FinishReason string
}

// ParseChatResponse extracts the first choice of a Chat Completions body.
func ParseChatResponse(body []byte) (Answer, bool) {
	var resp struct {
		Choices []struct {
			Message struct {
				Content   *string    `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if json.Unmarshal(body, &resp) != nil || len(resp.Choices) == 0 {
		return Answer{}, false
	}
	c := resp.Choices[0]
	a := Answer{ToolCalls: c.Message.ToolCalls, FinishReason: c.FinishReason}
	if c.Message.Content != nil {
		a.Content = *c.Message.Content
	}
	return a, true
}

// Check returns "" when the answer passes, else the failure reason.
func Check(r *core.Request, a Answer) string {
	if strings.TrimSpace(a.Content) == "" && len(a.ToolCalls) == 0 {
		return Empty
	}
	if a.FinishReason == "length" && r.MaxTokens == 0 {
		return Truncated
	}
	if r.WantsJSON && len(a.ToolCalls) == 0 && !json.Valid([]byte(StripFences(a.Content))) {
		return InvalidJSON
	}
	for _, tc := range a.ToolCalls {
		if tc.Function.Name == "" || (r.HasTools() && !r.ToolNames[tc.Function.Name]) {
			return BadToolCall
		}
		args := strings.TrimSpace(tc.Function.Arguments)
		if args != "" && !json.Valid([]byte(args)) {
			return BadToolCall
		}
	}
	return ""
}

// StripFences removes a surrounding ```json fence some models add.
func StripFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
}
