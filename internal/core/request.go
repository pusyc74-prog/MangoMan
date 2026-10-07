// Package core holds the internal request model shared by every ingress
// adapter and the router. In Phase 1 the canonical wire shape is OpenAI Chat
// Completions, because every launch provider speaks it natively; the other
// client formats (Anthropic Messages, OpenAI Responses) translate into it.
package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
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
	// KeepUsage forwards the usage chunk the router asks providers for, even
	// though the client did not ask (format adapters read it).
	KeepUsage bool
	// Internal marks the router's own requests (decision brain): no brain
	// calls of their own, My list not applied.
	Internal bool
	// AllowWeaker lets this request fall to clearly weaker models when the
	// strong ones are used up (header X-MangoMan-Allow-Weaker: 1).
	AllowWeaker bool
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

// FirstUserText returns the text of the first user message: a stable key
// for one conversation, since agents resend the history on every turn.
func (r *Request) FirstUserText() string {
	for _, m := range r.Messages {
		if m.Role == "user" {
			t, _ := contentText(m.Content)
			return t
		}
	}
	return ""
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
			addedUsage = !r.KeepUsage
		}
	}
	body, err = json.Marshal(out)
	return body, addedUsage, err
}

// TokenParts estimates, in tokens, where a request's input goes: the
// instructions (system), the tool definitions, the user's messages, the
// model's own earlier turns (with the tool calls it made) and tool results
// read back. Counts only, never content.
func (r *Request) TokenParts() map[string]int {
	parts := map[string]int{}
	if v, ok := r.Raw["tools"]; ok {
		parts["tool_definitions"] = len(v) / 4
	}
	var msgs []json.RawMessage
	_ = json.Unmarshal(r.Raw["messages"], &msgs)
	for _, m := range msgs {
		var h struct {
			Role string `json:"role"`
		}
		_ = json.Unmarshal(m, &h)
		key := "user"
		switch h.Role {
		case "system", "developer":
			key = "instructions"
		case "assistant":
			key = "earlier_answers"
		case "tool":
			key = "tool_results"
		}
		parts[key] += len(m) / 4
	}
	return parts
}

// secretRE matches the shapes of common API keys, tokens and private keys.
// Shapes only: long, prefixed strings, so ordinary text and code rarely match.
var secretRE = regexp.MustCompile(`nvapi-[A-Za-z0-9_-]{30,}|gsk_[A-Za-z0-9]{30,}|sk-or-v1-[a-f0-9]{40,}|` +
	`sk-ant-[A-Za-z0-9_-]{30,}|sk-(?:proj-)?[A-Za-z0-9_-]{32,}|csk-[a-z0-9]{30,}|ghp_[A-Za-z0-9]{36}|` +
	`github_pat_[A-Za-z0-9_]{40,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{35}|xox[baprs]-[A-Za-z0-9-]{10,}|` +
	`mm-local-[a-f0-9]{48}|-----BEGIN [A-Z ]*PRIVATE KEY-----`)

// HasSecret reports whether the request seems to carry an API key, token or
// private key (pasted by the user, or read from a file by a coding tool).
func (r *Request) HasSecret() bool { return secretRE.Match(r.Raw["messages"]) }

type personKey struct{}

// WithPerson marks a request context with the person who sent it (their
// local token), for usage per person.
func WithPerson(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, personKey{}, name)
}

// Person is the name WithPerson set, or "".
func Person(ctx context.Context) string {
	name, _ := ctx.Value(personKey{}).(string)
	return name
}
