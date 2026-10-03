package adapt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// ---------- OpenAI Responses request -> Chat Completions ----------

// ResponsesRequest is what the codec needs to know about the original request.
type ResponsesRequest struct {
	Model  string
	Stream bool
	// Custom holds the names of freeform ("custom") tools, which are sent
	// upstream as functions with one string argument and turned back into
	// custom tool calls on the way out.
	Custom map[string]bool
}

type respContent struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	ImageURL json.RawMessage `json:"image_url"`
}

// respParts parses message content that is a string or an array of parts.
func respParts(c json.RawMessage) []respContent {
	c = bytes.TrimSpace(c)
	if len(c) == 0 || string(c) == "null" {
		return nil
	}
	if c[0] == '"' {
		var s string
		_ = json.Unmarshal(c, &s)
		return []respContent{{Type: "input_text", Text: s}}
	}
	var ps []respContent
	_ = json.Unmarshal(c, &ps)
	return ps
}

// ResponsesToChat converts a Responses request body to a Chat Completions body.
func ResponsesToChat(body []byte) ([]byte, ResponsesRequest, error) {
	var in struct {
		Model        string          `json:"model"`
		Instructions string          `json:"instructions"`
		Input        json.RawMessage `json:"input"`
		Tools        []struct {
			Type        string          `json:"type"`
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"tools"`
		ToolChoice        json.RawMessage `json:"tool_choice"`
		Stream            bool            `json:"stream"`
		MaxOutputTokens   int             `json:"max_output_tokens"`
		Temperature       *float64        `json:"temperature"`
		TopP              *float64        `json:"top_p"`
		ParallelToolCalls *bool           `json:"parallel_tool_calls"`
		PreviousResponse  string          `json:"previous_response_id"`
		Text              *struct {
			Format struct {
				Type   string          `json:"type"`
				Name   string          `json:"name"`
				Schema json.RawMessage `json:"schema"`
				Strict *bool           `json:"strict"`
			} `json:"format"`
		} `json:"text"`
	}
	info := ResponsesRequest{Custom: map[string]bool{}}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, info, bad("body is not a valid Responses request: %v", err)
	}
	if in.PreviousResponse != "" {
		return nil, info, bad("previous_response_id is not supported: MangoMan keeps no conversation state, so send the full input (for Codex, keep store off)")
	}
	info.Model, info.Stream = in.Model, in.Stream

	var msgs []map[string]any
	if in.Instructions != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": in.Instructions})
	}

	input := bytes.TrimSpace(in.Input)
	if len(input) == 0 {
		return nil, info, bad("input is required")
	}
	var items []struct {
		Type      string          `json:"type"`
		Role      string          `json:"role"`
		Content   json.RawMessage `json:"content"`
		CallID    string          `json:"call_id"`
		Name      string          `json:"name"`
		Arguments string          `json:"arguments"`
		Input     string          `json:"input"`
		Output    json.RawMessage `json:"output"`
	}
	if input[0] == '"' {
		var s string
		_ = json.Unmarshal(input, &s)
		msgs = append(msgs, map[string]any{"role": "user", "content": s})
	} else if err := json.Unmarshal(input, &items); err != nil {
		return nil, info, bad("input must be a string or an array of items")
	}

	// Assistant text and the function calls that follow it are one turn.
	var pending map[string]any
	flush := func() {
		if pending != nil {
			msgs = append(msgs, pending)
			pending = nil
		}
	}
	addCall := func(id, name, args string) {
		if pending == nil {
			if n := len(msgs); n > 0 && msgs[n-1]["role"] == "assistant" && msgs[n-1]["tool_calls"] == nil {
				pending, msgs = msgs[n-1], msgs[:n-1]
				pending["tool_calls"] = []map[string]any{}
			} else {
				pending = map[string]any{"role": "assistant", "content": nil, "tool_calls": []map[string]any{}}
			}
		}
		pending["tool_calls"] = append(pending["tool_calls"].([]map[string]any), map[string]any{
			"id": id, "type": "function", "function": map[string]any{"name": name, "arguments": args}})
	}
	for i, it := range items {
		typ := it.Type
		if typ == "" && it.Role != "" {
			typ = "message"
		}
		switch typ {
		case "message":
			role := it.Role
			switch role {
			case "developer":
				role = "system"
			case "user", "assistant", "system":
			default:
				return nil, info, bad("input[%d].role %q is not supported", i, it.Role)
			}
			parts := respParts(it.Content)
			var text []string
			var chatParts []map[string]any
			images := false
			for _, p := range parts {
				switch p.Type {
				case "input_text", "output_text", "text":
					text = append(text, p.Text)
					chatParts = append(chatParts, map[string]any{"type": "text", "text": p.Text})
				case "input_image":
					var url string
					if json.Unmarshal(p.ImageURL, &url) != nil {
						var o struct {
							URL string `json:"url"`
						}
						_ = json.Unmarshal(p.ImageURL, &o)
						url = o.URL
					}
					if url != "" {
						images = true
						chatParts = append(chatParts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
					}
				}
			}
			if role == "assistant" && pending != nil && !images {
				// Text after the calls in the same turn.
				if t := strings.Join(text, ""); t != "" {
					if before, _ := pending["content"].(string); before != "" {
						t = before + "\n" + t
					}
					pending["content"] = t
				}
				flush()
				continue
			}
			flush()
			if images && role == "user" {
				msgs = append(msgs, map[string]any{"role": role, "content": chatParts})
			} else {
				msgs = append(msgs, map[string]any{"role": role, "content": strings.Join(text, "\n")})
			}
		case "function_call":
			addCall(it.CallID, it.Name, it.Arguments)
		case "custom_tool_call":
			args, _ := json.Marshal(map[string]string{"input": it.Input})
			addCall(it.CallID, it.Name, string(args))
		case "function_call_output", "custom_tool_call_output":
			flush()
			out := string(it.Output)
			var s string
			if json.Unmarshal(it.Output, &s) == nil {
				out = s
			} else if parts := respParts(it.Output); len(parts) > 0 {
				var t []string
				for _, p := range parts {
					t = append(t, p.Text)
				}
				out = strings.Join(t, "\n")
			}
			msgs = append(msgs, map[string]any{"role": "tool", "tool_call_id": it.CallID, "content": out})
		default:
			// Not forwarded: reasoning is provider-specific, references need
			// stored state, and hosted-tool items (web search, ...) exist only
			// at OpenAI. Skipping them keeps the conversation going.
		}
	}
	flush()

	out := map[string]any{"model": mapClientModel(in.Model), "messages": msgs, "stream": in.Stream}
	if in.MaxOutputTokens > 0 {
		out["max_tokens"] = in.MaxOutputTokens
	}
	if in.Temperature != nil {
		out["temperature"] = *in.Temperature
	}
	if in.TopP != nil {
		out["top_p"] = *in.TopP
	}
	var tools []map[string]any
	for _, t := range in.Tools {
		switch t.Type {
		case "function":
			params := t.Parameters
			if len(params) == 0 {
				params = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{
				"name": t.Name, "description": t.Description, "parameters": params}})
		case "custom":
			info.Custom[t.Name] = true
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{
				"name": t.Name, "description": t.Description + "\nPut the complete raw input in the \"input\" string.",
				"parameters": json.RawMessage(`{"type":"object","properties":{"input":{"type":"string"}},"required":["input"]}`)}})
		}
		// Hosted tools (web_search, file_search, ...) exist only at OpenAI.
	}
	if len(tools) > 0 {
		out["tools"] = tools
		tc := bytes.TrimSpace(in.ToolChoice)
		if len(tc) > 0 && tc[0] == '"' {
			var s string
			_ = json.Unmarshal(tc, &s)
			if s == "required" || s == "none" {
				out["tool_choice"] = s
			}
		} else if len(tc) > 0 {
			var o struct {
				Type string `json:"type"`
				Name string `json:"name"`
			}
			if json.Unmarshal(tc, &o) == nil && (o.Type == "function" || o.Type == "custom") && o.Name != "" {
				out["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": o.Name}}
			}
		}
		if in.ParallelToolCalls != nil && !*in.ParallelToolCalls {
			out["parallel_tool_calls"] = false
		}
	}
	if in.Text != nil {
		switch f := in.Text.Format; f.Type {
		case "json_object":
			out["response_format"] = map[string]any{"type": "json_object"}
		case "json_schema":
			js := map[string]any{"name": f.Name, "schema": f.Schema}
			if f.Strict != nil {
				js["strict"] = *f.Strict
			}
			out["response_format"] = map[string]any{"type": "json_schema", "json_schema": js}
		}
	}
	b, err := json.Marshal(out)
	return b, info, err
}

// ---------- Chat Completions reply -> Responses ----------

var idSeq atomic.Uint64

func newID(prefix string) string {
	return fmt.Sprintf("%s_mm%x%04x", prefix, time.Now().UnixNano(), idSeq.Add(1)&0xffff)
}

// ResponsesCodec converts router output to the Responses format.
type ResponsesCodec struct {
	Req      ResponsesRequest
	EstInput int

	id       string
	created  int64
	seq      int
	started  bool
	finish   string
	usage    chatUsage
	outChars int

	items   []map[string]any // output items, in order
	msgIdx  int              // index of the open message item, -1 none
	text    strings.Builder
	toolPos map[int]int // chat tool index -> items index
	args    map[int]*strings.Builder
}

// NewResponsesCodec returns a codec for one request.
func NewResponsesCodec(req ResponsesRequest, estInput int) *ResponsesCodec {
	return &ResponsesCodec{Req: req, EstInput: estInput, id: newID("resp"), created: time.Now().Unix(),
		msgIdx: -1, toolPos: map[int]int{}, args: map[int]*strings.Builder{}}
}

func (c *ResponsesCodec) model() string {
	if c.Req.Model != "" {
		return c.Req.Model
	}
	return "free/auto"
}

// callItem builds a function_call (or custom_tool_call) output item.
func (c *ResponsesCodec) callItem(id, name, args, status string) map[string]any {
	if c.Req.Custom[name] {
		var a struct {
			Input string `json:"input"`
		}
		input := args
		if json.Unmarshal([]byte(args), &a) == nil && a.Input != "" {
			input = a.Input
		}
		return map[string]any{"type": "custom_tool_call", "id": newID("ctc"), "call_id": id, "name": name,
			"input": input, "status": status}
	}
	return map[string]any{"type": "function_call", "id": newID("fc"), "call_id": id, "name": name,
		"arguments": args, "status": status}
}

func callID(id string) string {
	if id != "" {
		return id
	}
	return newID("call")
}

func (c *ResponsesCodec) response(status string, output []map[string]any) map[string]any {
	in := c.usage.Prompt
	if in == 0 {
		in = c.EstInput
	}
	outTok := c.usage.Completion
	if outTok == 0 {
		outTok = c.outChars / 4
	}
	r := map[string]any{
		"id": c.id, "object": "response", "created_at": c.created, "status": status, "model": c.model(),
		"output": output, "parallel_tool_calls": true, "tool_choice": "auto", "tools": []any{},
		"error": nil, "incomplete_details": nil,
	}
	if status != "in_progress" {
		r["usage"] = map[string]any{"input_tokens": in, "output_tokens": outTok, "total_tokens": in + outTok,
			"input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens_details": map[string]any{"reasoning_tokens": 0}}
	}
	if status == "incomplete" {
		r["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
	}
	return r
}

// ResponsesError builds an OpenAI-style error body.
func ResponsesError(status int, typ, msg string) []byte {
	b, _ := json.Marshal(map[string]any{"error": map[string]any{"message": msg, "type": typ, "code": typ}})
	return b
}

// JSON implements Codec.
func (c *ResponsesCodec) JSON(status int, body []byte) (int, []byte) {
	if status < 200 || status > 299 {
		return status, body // already OpenAI-style
	}
	var r chatResponse
	if json.Unmarshal(body, &r) != nil || len(r.Choices) == 0 {
		return http.StatusBadGateway, ResponsesError(http.StatusBadGateway, "upstream_unreadable", "provider sent an unreadable answer")
	}
	c.usage = r.Usage
	ch := r.Choices[0]
	var out []map[string]any
	if t := contentString(ch.Message.Content); t != "" {
		out = append(out, messageItem(newID("msg"), t, "completed"))
	}
	for _, tc := range ch.Message.ToolCalls {
		out = append(out, c.callItem(callID(tc.ID), tc.Function.Name, tc.Function.Arguments, "completed"))
	}
	if out == nil {
		out = []map[string]any{messageItem(newID("msg"), "", "completed")}
	}
	status2 := "completed"
	if ch.FinishReason == "length" {
		status2 = "incomplete"
	}
	resp := c.response(status2, out)
	b, _ := json.Marshal(resp)
	return status, b
}

func messageItem(id, text, status string) map[string]any {
	return map[string]any{"type": "message", "id": id, "status": status, "role": "assistant",
		"content": []map[string]any{{"type": "output_text", "text": text, "annotations": []any{}}}}
}

func (c *ResponsesCodec) ev(typ string, fields map[string]any) Event {
	fields["type"] = typ
	fields["sequence_number"] = c.seq
	c.seq++
	return Event{Name: typ, Data: fields}
}

func (c *ResponsesCodec) start() []Event {
	if c.started {
		return nil
	}
	c.started = true
	return []Event{
		c.ev("response.created", map[string]any{"response": c.response("in_progress", []map[string]any{})}),
		c.ev("response.in_progress", map[string]any{"response": c.response("in_progress", []map[string]any{})}),
	}
}

// closeMessage finishes the open message item.
func (c *ResponsesCodec) closeMessage() []Event {
	if c.msgIdx < 0 {
		return nil
	}
	item := c.items[c.msgIdx]
	id, t := item["id"].(string), c.text.String()
	part := map[string]any{"type": "output_text", "text": t, "annotations": []any{}}
	done := messageItem(id, t, "completed")
	c.items[c.msgIdx] = done
	evs := []Event{
		c.ev("response.output_text.done", map[string]any{"item_id": id, "output_index": c.msgIdx, "content_index": 0, "text": t}),
		c.ev("response.content_part.done", map[string]any{"item_id": id, "output_index": c.msgIdx, "content_index": 0, "part": part}),
		c.ev("response.output_item.done", map[string]any{"output_index": c.msgIdx, "item": done}),
	}
	c.msgIdx = -1
	c.text.Reset()
	return evs
}

// Chunk implements Codec.
func (c *ResponsesCodec) Chunk(data []byte) []Event {
	var ch chatChunk
	if json.Unmarshal(data, &ch) != nil {
		return nil
	}
	evs := c.start()
	if ch.Usage != nil {
		c.usage = *ch.Usage
	}
	for _, choice := range ch.Choices {
		d := choice.Delta
		if d.Content != nil && *d.Content != "" {
			if c.msgIdx < 0 {
				id := newID("msg")
				c.msgIdx = len(c.items)
				c.items = append(c.items, map[string]any{"type": "message", "id": id, "status": "in_progress", "role": "assistant", "content": []any{}})
				evs = append(evs,
					c.ev("response.output_item.added", map[string]any{"output_index": c.msgIdx, "item": c.items[c.msgIdx]}),
					c.ev("response.content_part.added", map[string]any{"item_id": id, "output_index": c.msgIdx, "content_index": 0,
						"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}}))
			}
			c.text.WriteString(*d.Content)
			c.outChars += len(*d.Content)
			evs = append(evs, c.ev("response.output_text.delta", map[string]any{"item_id": c.items[c.msgIdx]["id"],
				"output_index": c.msgIdx, "content_index": 0, "delta": *d.Content}))
		}
		for _, tc := range d.ToolCalls {
			pos, seen := c.toolPos[tc.Index]
			if !seen {
				evs = append(evs, c.closeMessage()...)
				item := c.callItem(callID(tc.ID), tc.Function.Name, "", "in_progress")
				pos = len(c.items)
				c.toolPos[tc.Index] = pos
				c.args[tc.Index] = &strings.Builder{}
				c.items = append(c.items, item)
				evs = append(evs, c.ev("response.output_item.added", map[string]any{"output_index": pos, "item": item}))
			}
			if a := tc.Function.Arguments; a != "" {
				c.args[tc.Index].WriteString(a)
				c.outChars += len(a)
				if c.items[pos]["type"] == "function_call" {
					evs = append(evs, c.ev("response.function_call_arguments.delta", map[string]any{
						"item_id": c.items[pos]["id"], "output_index": pos, "delta": a}))
				}
			}
		}
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			c.finish = *choice.FinishReason
		}
	}
	return evs
}

// Done implements Codec.
func (c *ResponsesCodec) Done() []Event {
	evs := c.start()
	evs = append(evs, c.closeMessage()...)
	posIdx := map[int]int{}
	for idx, pos := range c.toolPos {
		posIdx[pos] = idx
	}
	for pos, item := range c.items {
		idx, ok := posIdx[pos]
		if !ok {
			continue
		}
		args := c.args[idx].String()
		done := c.callItem(item["call_id"].(string), item["name"].(string), args, "completed")
		done["id"] = item["id"]
		c.items[pos] = done
		if done["type"] == "function_call" {
			evs = append(evs, c.ev("response.function_call_arguments.done", map[string]any{
				"item_id": done["id"], "output_index": pos, "arguments": args}))
		}
		evs = append(evs, c.ev("response.output_item.done", map[string]any{"output_index": pos, "item": done}))
	}
	status, name := "completed", "response.completed"
	if c.finish == "length" {
		status, name = "incomplete", "response.incomplete"
	}
	out := c.items
	if out == nil {
		out = []map[string]any{}
	}
	return append(evs, c.ev(name, map[string]any{"response": c.response(status, out)}))
}

// StreamError implements Codec.
func (c *ResponsesCodec) StreamError(msg string) []Event {
	evs := c.start()
	r := c.response("failed", c.itemsOrEmpty())
	r["error"] = map[string]any{"code": "server_error", "message": msg}
	return append(evs, c.ev("response.failed", map[string]any{"response": r}))
}

func (c *ResponsesCodec) itemsOrEmpty() []map[string]any {
	if c.items == nil {
		return []map[string]any{}
	}
	return c.items
}
