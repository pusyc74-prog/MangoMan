// Package adapt translates other client formats to and from the router's
// internal format (OpenAI Chat Completions). A request is converted before
// it reaches the router; the router's reply is converted on the way out by a
// Transcoder, so failover, the quality guard and quota tracking work the same
// for every format.
package adapt

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

// Event is one server-sent event in the client's format.
type Event struct {
	Name string // SSE "event:" field; empty for data-only events
	Data any    // marshalled to JSON
}

// Codec converts router output into one client format.
type Codec interface {
	// JSON converts a complete (non-stream) router reply. status is the
	// router's HTTP status; non-2xx bodies are OpenAI-style errors.
	JSON(status int, body []byte) (int, []byte)
	// Chunk converts one Chat Completions stream chunk.
	Chunk(data []byte) []Event
	// StreamError converts an error the router reported mid-stream.
	StreamError(msg string) []Event
	// Done closes the stream (the router sent [DONE]).
	Done() []Event
}

// Transcoder is an http.ResponseWriter placed between the router and the
// client. Streams are converted event by event as they arrive; complete
// replies are buffered and converted when Finish is called.
type Transcoder struct {
	w      http.ResponseWriter
	codec  Codec
	status int
	stream bool
	buf    bytes.Buffer // JSON body, or a partial SSE line
	ended  bool
	wrote  bool
}

// NewTranscoder wraps w.
func NewTranscoder(w http.ResponseWriter, c Codec) *Transcoder {
	return &Transcoder{w: w, codec: c}
}

// Header returns the client response headers (the router's X-MangoMan
// headers pass through).
func (t *Transcoder) Header() http.Header { return t.w.Header() }

// WriteHeader records the status; streams start immediately.
func (t *Transcoder) WriteHeader(code int) {
	if t.status != 0 {
		return
	}
	t.status = code
	ct := t.w.Header().Get("Content-Type")
	if code == http.StatusOK && strings.HasPrefix(ct, "text/event-stream") {
		t.stream = true
		t.w.Header().Del("Content-Length")
		t.w.WriteHeader(code)
		t.wrote = true
	}
}

// Write takes router output.
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.status == 0 {
		t.WriteHeader(http.StatusOK)
	}
	if !t.stream {
		t.buf.Write(p)
		return len(p), nil
	}
	t.buf.Write(p)
	for {
		data := t.buf.Bytes()
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		line := string(bytes.TrimRight(data[:i], "\r"))
		t.buf.Next(i + 1)
		t.line(line)
	}
	return len(p), nil
}

// Flush passes flushes through to the client.
func (t *Transcoder) Flush() {
	if f, ok := t.w.(http.Flusher); ok {
		f.Flush()
	}
}

func (t *Transcoder) line(line string) {
	if !strings.HasPrefix(line, "data:") || t.ended {
		return
	}
	data := strings.TrimSpace(line[5:])
	if data == "[DONE]" {
		t.emit(t.codec.Done())
		t.ended = true
		return
	}
	var probe struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(data), &probe) == nil && probe.Error != nil {
		t.emit(t.codec.StreamError(probe.Error.Message))
		t.ended = true
		return
	}
	t.emit(t.codec.Chunk([]byte(data)))
}

func (t *Transcoder) emit(evs []Event) {
	if len(evs) == 0 {
		return
	}
	var b bytes.Buffer
	for _, e := range evs {
		d, _ := json.Marshal(e.Data)
		if e.Name != "" {
			b.WriteString("event: " + e.Name + "\n")
		}
		b.WriteString("data: ")
		b.Write(d)
		b.WriteString("\n\n")
	}
	_, _ = t.w.Write(b.Bytes())
	t.Flush()
}

// Finish writes a buffered reply, or closes a stream that ended without a
// final event. Call it after the router returns.
func (t *Transcoder) Finish() {
	if t.stream {
		if !t.ended {
			t.emit(t.codec.StreamError("stream ended unexpectedly"))
			t.ended = true
		}
		return
	}
	if t.status == 0 || t.wrote {
		return // nothing written (the client went away)
	}
	status, body := t.codec.JSON(t.status, t.buf.Bytes())
	h := t.w.Header()
	h.Set("Content-Type", "application/json")
	h.Del("Content-Length")
	t.w.WriteHeader(status)
	_, _ = t.w.Write(body)
	t.wrote = true
}

// chatResponse is the part of a Chat Completions reply the codecs need.
type chatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Created int64  `json:"created"`
	Choices []struct {
		Message struct {
			Content          json.RawMessage `json:"content"`
			Reasoning        string          `json:"reasoning"`
			ReasoningContent string          `json:"reasoning_content"`
			ToolCalls        []chatToolCall  `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage chatUsage `json:"usage"`
}

type chatUsage struct {
	Prompt     int `json:"prompt_tokens"`
	Completion int `json:"completion_tokens"`
	Total      int `json:"total_tokens"`
}

type chatToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// chatChunk is one Chat Completions stream chunk.
type chatChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content          *string        `json:"content"`
			Reasoning        *string        `json:"reasoning"`
			ReasoningContent *string        `json:"reasoning_content"`
			ToolCalls        []chatToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *chatUsage `json:"usage"`
}

// openAIError reads an OpenAI-style error body.
func openAIError(body []byte) (typ, msg string) {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil || e.Error.Message == "" {
		return "api_error", strings.TrimSpace(string(body))
	}
	return e.Error.Type, e.Error.Message
}

// contentString returns the text of a chat message content (string or parts).
func contentString(c json.RawMessage) string {
	c = bytes.TrimSpace(c)
	if len(c) == 0 || string(c) == "null" {
		return ""
	}
	if c[0] == '"' {
		var s string
		_ = json.Unmarshal(c, &s)
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(c, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" || p.Type == "output_text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func raw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
