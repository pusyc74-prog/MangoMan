package adapt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ErrBadRequest marks a request that cannot be translated.
var ErrBadRequest = errors.New("bad request")

func bad(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrBadRequest}, a...)...)
}

// mapClientModel turns a vendor model name a client sends by default
// (claude-*, gpt-*) into a virtual model. Catalogue names and virtual names
// pass through.
func mapClientModel(m string) string {
	low := strings.ToLower(strings.TrimSpace(m))
	switch {
	case low == "":
		return "free/auto"
	case strings.Contains(low, "haiku"), strings.Contains(low, "mini"), strings.Contains(low, "nano"):
		if strings.HasPrefix(low, "claude") || strings.HasPrefix(low, "gpt-") || strings.HasPrefix(low, "o4-") {
			return "free/fast"
		}
	case strings.HasPrefix(low, "claude"), strings.HasPrefix(low, "gpt-"), strings.HasPrefix(low, "o1"), strings.HasPrefix(low, "o3"):
		return "free/coder"
	}
	return m
}

// ---------- Anthropic Messages request -> Chat Completions ----------

type anthBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	Source    *struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
		URL       string `json:"url"`
	} `json:"source"`
}

// anthBlocks parses content that is a string or an array of blocks.
func anthBlocks(c json.RawMessage) ([]anthBlock, error) {
	c = bytes.TrimSpace(c)
	if len(c) == 0 || string(c) == "null" {
		return nil, nil
	}
	if c[0] == '"' {
		var s string
		if err := json.Unmarshal(c, &s); err != nil {
			return nil, err
		}
		return []anthBlock{{Type: "text", Text: s}}, nil
	}
	var bs []anthBlock
	err := json.Unmarshal(c, &bs)
	return bs, err
}

func imagePart(b anthBlock) map[string]any {
	if b.Source == nil {
		return nil
	}
	url := b.Source.URL
	if b.Source.Type == "base64" {
		url = "data:" + b.Source.MediaType + ";base64," + b.Source.Data
	}
	if url == "" {
		return nil
	}
	return map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}}
}

// AnthropicRequest is what the codec needs to know about the original request.
type AnthropicRequest struct {
	Model  string // as the client sent it, echoed back
	Stream bool
}

// AnthropicToChat converts a Messages request body to a Chat Completions body.
func AnthropicToChat(body []byte) ([]byte, AnthropicRequest, error) {
	var in struct {
		Model    string          `json:"model"`
		System   json.RawMessage `json:"system"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		MaxTokens     int      `json:"max_tokens"`
		Temperature   *float64 `json:"temperature"`
		TopP          *float64 `json:"top_p"`
		StopSequences []string `json:"stop_sequences"`
		Stream        bool     `json:"stream"`
		Tools         []struct {
			Type        string          `json:"type"`
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"input_schema"`
		} `json:"tools"`
		ToolChoice *struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"tool_choice"`
	}
	var info AnthropicRequest
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, info, bad("body is not a valid Messages request: %v", err)
	}
	if len(in.Messages) == 0 {
		return nil, info, bad("messages is required")
	}
	info.Model, info.Stream = in.Model, in.Stream

	var msgs []map[string]any
	if sys, err := anthBlocks(in.System); err != nil {
		return nil, info, bad("system must be a string or text blocks")
	} else if len(sys) > 0 {
		var t []string
		for _, b := range sys {
			if b.Type == "text" && b.Text != "" {
				t = append(t, b.Text)
			}
		}
		if len(t) > 0 {
			msgs = append(msgs, map[string]any{"role": "system", "content": strings.Join(t, "\n\n")})
		}
	}

	for i, m := range in.Messages {
		blocks, err := anthBlocks(m.Content)
		if err != nil {
			return nil, info, bad("messages[%d].content is not valid", i)
		}
		switch m.Role {
		case "assistant":
			var text strings.Builder
			var calls []map[string]any
			for _, b := range blocks {
				switch b.Type {
				case "text":
					text.WriteString(b.Text)
				case "tool_use":
					args := string(b.Input)
					if strings.TrimSpace(args) == "" || args == "null" {
						args = "{}"
					}
					calls = append(calls, map[string]any{"id": b.ID, "type": "function",
						"function": map[string]any{"name": b.Name, "arguments": args}})
				}
				// thinking and redacted_thinking blocks are not forwarded.
			}
			msg := map[string]any{"role": "assistant"}
			if text.Len() > 0 || len(calls) == 0 {
				msg["content"] = text.String()
			} else {
				msg["content"] = nil
			}
			if len(calls) > 0 {
				msg["tool_calls"] = calls
			}
			msgs = append(msgs, msg)
		case "user":
			// Tool results become tool messages, which must directly follow
			// the assistant turn that called them; other blocks follow.
			var parts []map[string]any
			for _, b := range blocks {
				switch b.Type {
				case "tool_result":
					res, _ := anthBlocks(b.Content)
					var t []string
					for _, r := range res {
						if r.Type == "text" {
							t = append(t, r.Text)
						}
					}
					out := strings.Join(t, "\n")
					if b.IsError {
						out = "Error: " + out
					}
					msgs = append(msgs, map[string]any{"role": "tool", "tool_call_id": b.ToolUseID, "content": out})
				case "text":
					parts = append(parts, map[string]any{"type": "text", "text": b.Text})
				case "image":
					if p := imagePart(b); p != nil {
						parts = append(parts, p)
					}
				case "document":
					// PDFs and other documents are not supported by the free providers.
					parts = append(parts, map[string]any{"type": "text", "text": "[document omitted]"})
				}
			}
			if len(parts) == 0 {
				continue
			}
			allText := true
			for _, p := range parts {
				if p["type"] != "text" {
					allText = false
				}
			}
			if allText {
				var t []string
				for _, p := range parts {
					t = append(t, p["text"].(string))
				}
				msgs = append(msgs, map[string]any{"role": "user", "content": strings.Join(t, "\n")})
			} else {
				msgs = append(msgs, map[string]any{"role": "user", "content": parts})
			}
		default:
			return nil, info, bad("messages[%d].role must be user or assistant", i)
		}
	}

	out := map[string]any{"model": mapClientModel(in.Model), "messages": msgs, "stream": in.Stream}
	if in.MaxTokens > 0 {
		out["max_tokens"] = in.MaxTokens
	}
	if in.Temperature != nil {
		out["temperature"] = *in.Temperature
	}
	if in.TopP != nil {
		out["top_p"] = *in.TopP
	}
	if len(in.StopSequences) > 0 {
		out["stop"] = in.StopSequences
	}
	var tools []map[string]any
	for _, t := range in.Tools {
		if len(t.InputSchema) == 0 {
			continue // server tools (web search, code execution) are Anthropic-only
		}
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{
			"name": t.Name, "description": t.Description, "parameters": t.InputSchema}})
	}
	if len(tools) > 0 {
		out["tools"] = tools
		if tc := in.ToolChoice; tc != nil {
			switch tc.Type {
			case "any":
				out["tool_choice"] = "required"
			case "tool":
				out["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": tc.Name}}
			case "none":
				out["tool_choice"] = "none"
			}
		}
	}
	b, err := json.Marshal(out)
	return b, info, err
}

// ---------- Chat Completions reply -> Anthropic Messages ----------

func stopReason(finish string) string {
	switch finish {
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	default:
		return "end_turn"
	}
}

func newMsgID() string { return fmt.Sprintf("msg_mm%x", time.Now().UnixNano()) }

// AnthropicCodec converts router output to the Messages format.
type AnthropicCodec struct {
	Req       AnthropicRequest
	EstInput  int // input token estimate, until the provider reports usage
	started   bool
	block     int    // index of the open content block, -1 none
	blockType string // "text" or "tool_use"
	toolIdx   map[int]int
	stop      string
	usage     chatUsage
	outChars  int
}

// NewAnthropicCodec returns a codec for one request.
func NewAnthropicCodec(req AnthropicRequest, estInput int) *AnthropicCodec {
	return &AnthropicCodec{Req: req, EstInput: estInput, block: -1, toolIdx: map[int]int{}}
}

func anthErrorType(status int) string {
	switch {
	case status == 400 || status == 422:
		return "invalid_request_error"
	case status == 401:
		return "authentication_error"
	case status == 403:
		return "permission_error"
	case status == 404:
		return "not_found_error"
	case status == 413:
		return "request_too_large"
	case status == 429:
		return "rate_limit_error"
	case status == 529 || status == 503:
		return "overloaded_error"
	default:
		return "api_error"
	}
}

// AnthropicError builds an Anthropic error body.
func AnthropicError(status int, msg string) []byte {
	b, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]any{"type": anthErrorType(status), "message": msg}})
	return b
}

func (c *AnthropicCodec) model() string {
	if c.Req.Model != "" {
		return c.Req.Model
	}
	return "free/auto"
}

// JSON implements Codec.
func (c *AnthropicCodec) JSON(status int, body []byte) (int, []byte) {
	if status < 200 || status > 299 {
		_, msg := openAIError(body)
		return status, AnthropicError(status, msg)
	}
	var r chatResponse
	if json.Unmarshal(body, &r) != nil || len(r.Choices) == 0 {
		return http.StatusBadGateway, AnthropicError(http.StatusBadGateway, "provider sent an unreadable answer")
	}
	ch := r.Choices[0]
	content := []map[string]any{}
	if t := contentString(ch.Message.Content); t != "" {
		content = append(content, map[string]any{"type": "text", "text": t})
	}
	for _, tc := range ch.Message.ToolCalls {
		content = append(content, map[string]any{"type": "tool_use", "id": toolID(tc.ID),
			"name": tc.Function.Name, "input": toolInput(tc.Function.Arguments)})
	}
	if len(content) == 0 {
		content = append(content, map[string]any{"type": "text", "text": ""})
	}
	stop := stopReason(ch.FinishReason)
	if len(ch.Message.ToolCalls) > 0 {
		stop = "tool_use"
	}
	in := r.Usage.Prompt
	if in == 0 {
		in = c.EstInput
	}
	out := map[string]any{
		"id": newMsgID(), "type": "message", "role": "assistant", "model": c.model(),
		"content": content, "stop_reason": stop, "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": in, "output_tokens": r.Usage.Completion},
	}
	b, _ := json.Marshal(out)
	return status, b
}

// toolID keeps provider ids but makes sure there is one.
func toolID(id string) string {
	if id != "" {
		return id
	}
	return fmt.Sprintf("toolu_mm%x", time.Now().UnixNano())
}

// toolInput parses tool arguments; invalid JSON is wrapped so it is not lost.
func toolInput(args string) json.RawMessage {
	args = strings.TrimSpace(args)
	if args == "" {
		return json.RawMessage("{}")
	}
	if json.Valid([]byte(args)) && strings.HasPrefix(args, "{") {
		return json.RawMessage(args)
	}
	return raw(map[string]any{"_raw": args})
}

func (c *AnthropicCodec) start(id string) []Event {
	if c.started {
		return nil
	}
	c.started = true
	return []Event{{Name: "message_start", Data: map[string]any{"type": "message_start", "message": map[string]any{
		"id": newMsgID(), "type": "message", "role": "assistant", "model": c.model(), "content": []any{},
		"stop_reason": nil, "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": c.EstInput, "output_tokens": 0},
	}}}}
}

func (c *AnthropicCodec) closeBlock() []Event {
	if c.block < 0 {
		return nil
	}
	ev := Event{Name: "content_block_stop", Data: map[string]any{"type": "content_block_stop", "index": c.block}}
	c.blockType = ""
	return []Event{ev}
}

func (c *AnthropicCodec) open(kind string, block map[string]any) []Event {
	evs := c.closeBlock()
	c.block++
	c.blockType = kind
	return append(evs, Event{Name: "content_block_start", Data: map[string]any{
		"type": "content_block_start", "index": c.block, "content_block": block}})
}

// Chunk implements Codec.
func (c *AnthropicCodec) Chunk(data []byte) []Event {
	var ch chatChunk
	if json.Unmarshal(data, &ch) != nil {
		return nil
	}
	evs := c.start(ch.ID)
	if ch.Usage != nil {
		c.usage = *ch.Usage
	}
	for _, choice := range ch.Choices {
		d := choice.Delta
		if d.Content != nil && *d.Content != "" {
			if c.blockType != "text" {
				evs = append(evs, c.open("text", map[string]any{"type": "text", "text": ""})...)
			}
			c.outChars += len(*d.Content)
			evs = append(evs, Event{Name: "content_block_delta", Data: map[string]any{"type": "content_block_delta",
				"index": c.block, "delta": map[string]any{"type": "text_delta", "text": *d.Content}}})
		}
		for _, tc := range d.ToolCalls {
			idx, seen := c.toolIdx[tc.Index]
			if !seen {
				evs = append(evs, c.open("tool_use", map[string]any{"type": "tool_use", "id": toolID(tc.ID),
					"name": tc.Function.Name, "input": map[string]any{}})...)
				idx = c.block
				c.toolIdx[tc.Index] = idx
			}
			c.outChars += len(tc.Function.Arguments)
			if tc.Function.Arguments != "" && idx == c.block {
				evs = append(evs, Event{Name: "content_block_delta", Data: map[string]any{"type": "content_block_delta",
					"index": idx, "delta": map[string]any{"type": "input_json_delta", "partial_json": tc.Function.Arguments}}})
			}
		}
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			c.stop = stopReason(*choice.FinishReason)
		}
	}
	return evs
}

// Done implements Codec.
func (c *AnthropicCodec) Done() []Event {
	evs := c.start("")
	if c.block < 0 {
		// Always send at least one block, as Anthropic does.
		evs = append(evs, c.open("text", map[string]any{"type": "text", "text": ""})...)
	}
	evs = append(evs, c.closeBlock()...)
	stop := c.stop
	if stop == "" {
		stop = "end_turn"
	}
	if len(c.toolIdx) > 0 {
		stop = "tool_use"
	}
	outTok := c.usage.Completion
	if outTok == 0 {
		outTok = c.outChars / 4 // provider sent no usage: estimate
	}
	usage := map[string]any{"output_tokens": outTok}
	if c.usage.Prompt > 0 {
		usage["input_tokens"] = c.usage.Prompt
	}
	return append(evs,
		Event{Name: "message_delta", Data: map[string]any{"type": "message_delta",
			"delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": usage}},
		Event{Name: "message_stop", Data: map[string]any{"type": "message_stop"}})
}

// StreamError implements Codec.
func (c *AnthropicCodec) StreamError(msg string) []Event {
	return []Event{{Name: "error", Data: map[string]any{"type": "error",
		"error": map[string]any{"type": "api_error", "message": msg}}}}
}
