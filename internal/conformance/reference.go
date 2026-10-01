package conformance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Reference is a well-behaved OpenAI-compatible provider: it answers every
// corpus case the way real clients expect. sloppy flips to common free-model
// failures so we know the checks catch them. models is what GET /models
// lists. It exists for tests and demos; it never runs in the router.
func Reference(sloppy bool, models []string) http.HandlerFunc {
	if models == nil {
		models = []string{"ref-model"}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models") {
			data := make([]map[string]string, 0, len(models))
			for _, m := range models {
				data = append(data, map[string]string{"id": m, "object": "model"})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
			return
		}
		var req struct {
			Model          string           `json:"model"`
			Stream         bool             `json:"stream"`
			MaxTokens      int              `json:"max_tokens"`
			Tools          []any            `json:"tools"`
			ResponseFormat *json.RawMessage `json:"response_format"`
			Messages       []struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if strings.Contains(req.Model, "no-such-model") {
			w.WriteHeader(404)
			fmt.Fprint(w, `{"error":{"message":"model not found","type":"invalid_request_error"}}`)
			return
		}
		w.Header().Set("x-ratelimit-remaining-requests", "99")
		last := req.Messages[len(req.Messages)-1]
		lastText, _ := last.Content.(string)

		content, finish := "pong", "stop"
		var toolArgs string
		switch {
		case last.Role == "tool":
			content = "It is 31 C and clear in Pune."
		case len(req.Tools) > 0:
			toolArgs, content, finish = `{"city":"Pune"}`, "", "tool_calls"
		case req.ResponseFormat != nil:
			content = `{"name":"Ada Lovelace","age":36}`
		case req.MaxTokens > 0 && req.MaxTokens < 50:
			content, finish = "1, 2, 3, 4, 5, 6", "length"
		case strings.Contains(lastText, "my name"):
			content = "Ada."
		case strings.Contains(lastText, "Count"):
			content = "1 2 3 4 5"
		}
		if sloppy {
			switch {
			case toolArgs != "":
				toolArgs = `{city: Pune`
			case req.ResponseFormat != nil:
				content = "Sure! Here is the JSON: {name: Ada}"
			case req.Stream:
				content = ""
			}
		}

		if !req.Stream {
			msg := map[string]any{"role": "assistant", "content": content}
			if toolArgs != "" {
				msg["content"] = nil
				msg["tool_calls"] = []any{map[string]any{"id": "c1", "type": "function", "function": map[string]any{"name": "get_weather", "arguments": toolArgs}}}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "x", "object": "chat.completion", "model": req.Model,
				"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}},
				"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
			})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(v any) {
			b, _ := json.Marshal(v)
			fmt.Fprintf(w, "data: %s\n\n", b)
			w.(http.Flusher).Flush()
		}
		delta := func(d map[string]any, fin any) map[string]any {
			return map[string]any{"choices": []any{map[string]any{"index": 0, "delta": d, "finish_reason": fin}}}
		}
		send(delta(map[string]any{"role": "assistant", "content": ""}, nil))
		if toolArgs != "" {
			send(delta(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "c1", "type": "function", "function": map[string]any{"name": "get_weather", "arguments": ""}}}}, nil))
			half := len(toolArgs) / 2
			for _, part := range []string{toolArgs[:half], toolArgs[half:]} {
				send(delta(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": part}}}}, nil))
			}
		} else {
			for _, word := range strings.SplitAfter(content, " ") {
				if word != "" {
					send(delta(map[string]any{"content": word}, nil))
				}
			}
		}
		send(delta(map[string]any{}, finish))
		fmt.Fprint(w, "data: [DONE]\n\n")
	}
}
