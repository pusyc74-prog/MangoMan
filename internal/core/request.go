// Package core holds the internal request model shared by every ingress
// adapter and the router. In Phase 1 the canonical wire shape is OpenAI Chat
// Completions, because every launch provider speaks it natively; the other
// client formats (Anthropic Messages, OpenAI Responses) translate into it.
package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Message is one chat message. Content is kept raw so multimodal parts and
// provider extensions pass through untouched.
type Message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content,omitempty"`
}

// Request is a parsed chat request plus the facts the router needs.
type Request struct {
	// Raw holds every top-level field exactly as the client sent it, so fields
	// we do not model are forwarded unchanged.
	Raw map[string]json.RawMessage

	Model     string
	Stream    bool
	Messages  []Message
	ToolNames map[string]bool // declared function tool names
	WantsJSON bool            // response_format json_object or json_schema
	MaxTokens int             // client-set output limit, 0 if none
	HasImages bool
	EstTokens int // rough input token estimate
}

// HasTools reports whether the client declared any tools.
func (r *Request) HasTools() bool { return len(r.ToolNames) > 0 }

// ErrBadRequest marks a malformed client request.
var ErrBadRequest = errors.New("bad request")

// ParseChat parses an OpenAI Chat Completions request body.
func ParseChat(body []byte) (*Request, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("%w: body is not a JSON object", ErrBadRequest)
	}
	r := &Request{Raw: raw, ToolNames: map[string]bool{}}

	if v, ok := raw["model"]; ok {
		_ = json.Unmarshal(v, &r.Model)
	}
	if v, ok := raw["stream"]; ok {
		_ = json.Unmarshal(v, &r.Stream)
	}
	v, ok := raw["messages"]
	if !ok {
		return nil, fmt.Errorf("%w: messages is required", ErrBadRequest)
	}
	if err := json.Unmarshal(v, &r.Messages); err != nil {
		return nil, fmt.Errorf("%w: messages must be an array of messages", ErrBadRequest)
	}
	if len(r.Messages) == 0 {
		return nil, fmt.Errorf("%w: messages is empty", ErrBadRequest)
	}

	if v, ok := raw["tools"]; ok {
		var tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		}
		if err := json.Unmarshal(v, &tools); err == nil {
			for _, t := range tools {
				if t.Function.Name != "" {
					r.ToolNames[t.Function.Name] = true
				}
			}
		}
	}
	if v, ok := raw["response_format"]; ok {
		var rf struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(v, &rf) == nil && (rf.Type == "json_object" || rf.Type == "json_schema") {
			r.WantsJSON = true
		}
	}
	for _, k := range []string{"max_completion_tokens", "max_tokens"} {
		if v, ok := raw[k]; ok {
			var n int
			if json.Unmarshal(v, &n) == nil && n > 0 {
				r.MaxTokens = n
				break
			}
		}
	}

	chars := 0
	for _, m := range r.Messages {
		text, images := contentText(m.Content)
		chars += len(text)
		if images > 0 {
			r.HasImages = true
		}
		r.EstTokens += 4 + images*800
	}
	r.EstTokens += chars / 4
	return r, nil
}

// contentText returns the text of a message content (string or parts array)
// and the number of image parts.
func contentText(c json.RawMessage) (string, int) {
	c = bytes.TrimSpace(c)
	if len(c) == 0 || string(c) == "null" {
		return "", 0
	}
	if c[0] == '"' {
		var s string
		_ = json.Unmarshal(c, &s)
		return s, 0
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(c, &parts) != nil {
		return string(c), 0
	}
	var b strings.Builder
	images := 0
	for _, p := range parts {
		switch p.Type {
		case "text":
			b.WriteString(p.Text)
			b.WriteByte('\n')
		case "image_url", "input_image", "image":
			images++
		}
	}
	return b.String(), images
}

// LastUserText returns the text of the last user message, for classification.
func (r *Request) LastUserText() string {
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if r.Messages[i].Role == "user" {
			t, _ := contentText(r.Messages[i].Content)
			return t
		}
	}
	return ""
}

// BodyFor returns the request body to send upstream, with the model replaced.
func (r *Request) BodyFor(upstreamModel string) ([]byte, error) {
	b, _, err := r.BodyWith(upstreamModel, Upstream{})
	return b, err
}

// Upstream holds per-provider request adjustments.
type Upstream struct {
	Drop           []string // top-level fields to remove
	MaxTokensField string   // "max_tokens" or "max_completion_tokens"
	StreamUsage    bool     // ask for a final usage chunk on streams
}

// BodyWith returns the upstream body with provider adjustments applied.
// addedUsage reports that the router asked for the usage chunk itself, so
// the caller should not forward that chunk to a client that did not ask.
func (r *Request) BodyWith(upstreamModel string, u Upstream) (body []byte, addedUsage bool, err error) {
	out := make(map[string]json.RawMessage, len(r.Raw)+1)
	for k, v := range r.Raw {
		out[k] = v
	}
	m, _ := json.Marshal(upstreamModel)
	out["model"] = m
	for _, k := range u.Drop {
		delete(out, k)
	}
	if f := u.MaxTokensField; f != "" {
		var v json.RawMessage
		for _, k := range []string{"max_completion_tokens", "max_tokens"} {
			if x, ok := out[k]; ok {
				if v == nil {
					v = x
				}
				delete(out, k)
			}
		}
		if v != nil {
			out[f] = v
		}
	}
	if u.StreamUsage && r.Stream {
		opts := map[string]json.RawMessage{}
		if x, ok := out["stream_options"]; ok {
			_ = json.Unmarshal(x, &opts)
		}
		var already bool
		if x, ok := opts["include_usage"]; ok {
			_ = json.Unmarshal(x, &already)
		}
		if !already {
			opts["include_usage"] = json.RawMessage("true")
			b, _ := json.Marshal(opts)
			out["stream_options"] = b
			addedUsage = true
		}
	}
	body, err = json.Marshal(out)
	return body, addedUsage, err
}
