// Package conformance is the Phase 1 exit-gate corpus: a fixed set of
// requests that every provider and model must handle the way OpenAI-compatible
// clients expect (plain chat, multi-turn, streaming, tool calls, tool results,
// JSON mode, truncation, errors). The same cases run offline against recorded
// behaviour in tests and live against real providers in `mangoman doctor`.
package conformance

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/guard"
)

// Status of one case.
type Status string

const (
	Pass Status = "pass"
	Warn Status = "warn" // works, but with a caveat worth knowing
	Fail Status = "fail"
	Skip Status = "skip"
)

// Short is a one-letter cell for tables.
func (s Status) Short() string {
	return map[Status]string{Pass: "P", Warn: "W", Fail: "F", Skip: "-"}[s]
}

// SendFunc sends one Chat Completions body and returns the raw response.
type SendFunc func(ctx context.Context, body []byte, stream bool) (*http.Response, error)

// Result is a parsed response.
type Result struct {
	Status      int
	Header      http.Header
	Raw         []byte // non-stream body, or the raw stream
	IsSSE       bool
	Events      int
	Done        bool // saw [DONE]
	Content     string
	ToolCalls   []guard.ToolCall
	Finish      string
	HasUsage    bool
	ErrMsg      string
	Latency     time.Duration
	FirstOutput time.Duration // streams: time to first content or tool delta
}

// Case is one conformance check.
type Case struct {
	ID         string
	Desc       string
	Needs      string // capability the model must declare: "tools", "json" or ""
	Stream     bool
	DirectOnly bool // only meaningful against a provider, not through the router
	Body       func(model string) map[string]any
	Check      func(r *Result) (Status, string)
}

// CaseResult is the outcome of one case.
type CaseResult struct {
	Case        string            `json:"case"`
	Status      Status            `json:"status"`
	Detail      string            `json:"detail,omitempty"`
	HTTP        int               `json:"http,omitempty"`
	LatencyMS   int64             `json:"latency_ms,omitempty"`
	FirstOutMS  int64             `json:"first_output_ms,omitempty"`
	RateHeaders map[string]string `json:"rate_headers,omitempty"`
}

var weatherTool = map[string]any{
	"type": "function",
	"function": map[string]any{
		"name":        "get_weather",
		"description": "Get the current weather for a city",
		"parameters": map[string]any{
			"type":       "object",
			"properties": map[string]any{"city": map[string]any{"type": "string"}},
			"required":   []string{"city"},
		},
	},
}

func user(s string) map[string]any { return map[string]any{"role": "user", "content": s} }

// Cases returns the full corpus in run order.
func Cases() []Case {
	return []Case{
		{
			ID: "basic", Desc: "plain chat completion",
			Body: func(m string) map[string]any {
				return map[string]any{"model": m, "messages": []any{user("Reply with exactly one word: pong")}}
			},
			Check: func(r *Result) (Status, string) {
				if s, d := needOK(r); s != Pass {
					return s, d
				}
				if strings.TrimSpace(r.Content) == "" {
					return Fail, "empty content"
				}
				var notes []string
				if !strings.Contains(strings.ToLower(r.Content), "pong") {
					notes = append(notes, "did not follow the instruction")
				}
				if r.Finish != "stop" {
					notes = append(notes, "finish_reason "+q(r.Finish))
				}
				if !r.HasUsage {
					notes = append(notes, "no usage block (quota counts will be estimates)")
				}
				return warnIf(notes)
			},
		},
		{
			ID: "multiturn", Desc: "system prompt and earlier turns are honoured",
			Body: func(m string) map[string]any {
				return map[string]any{"model": m, "messages": []any{
					map[string]any{"role": "system", "content": "You are a terse assistant. Answer in one word."},
					user("My name is Ada."),
					map[string]any{"role": "assistant", "content": "Noted."},
					user("What is my name?"),
				}}
			},
			Check: func(r *Result) (Status, string) {
				if s, d := needOK(r); s != Pass {
					return s, d
				}
				if strings.TrimSpace(r.Content) == "" {
					return Fail, "empty content"
				}
				if !strings.Contains(strings.ToLower(r.Content), "ada") {
					return Warn, "lost earlier context"
				}
				return Pass, ""
			},
		},
		{
			ID: "stream", Desc: "server-sent events with content deltas and [DONE]", Stream: true,
			Body: func(m string) map[string]any {
				return map[string]any{"model": m, "stream": true, "messages": []any{user("Count from 1 to 5, separated by spaces.")}}
			},
			Check: func(r *Result) (Status, string) {
				if s, d := needOK(r); s != Pass {
					return s, d
				}
				if !r.IsSSE {
					return Fail, "response is not text/event-stream"
				}
				if strings.TrimSpace(r.Content) == "" {
					return Fail, "no content deltas"
				}
				if !r.Done {
					return Fail, "stream did not end with [DONE]"
				}
				if r.Finish == "" {
					return Warn, "no finish_reason in stream"
				}
				return Pass, ""
			},
		},
		{
			ID: "tools", Desc: "model calls a declared tool with valid JSON arguments", Needs: "tools",
			Body: func(m string) map[string]any {
				return map[string]any{"model": m, "tools": []any{weatherTool}, "tool_choice": "auto",
					"messages": []any{user("What is the weather in Pune right now? Use the get_weather tool.")}}
			},
			Check: checkToolCall,
		},
		{
			ID: "tool_result", Desc: "model reads a tool result and answers", Needs: "tools",
			Body: func(m string) map[string]any {
				return map[string]any{"model": m, "tools": []any{weatherTool}, "messages": []any{
					user("What is the weather in Pune?"),
					map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{
						"id": "call_1", "type": "function",
						"function": map[string]any{"name": "get_weather", "arguments": `{"city":"Pune"}`},
					}}},
					map[string]any{"role": "tool", "tool_call_id": "call_1", "content": `{"temp_c":31,"sky":"clear"}`},
				}}
			},
			Check: func(r *Result) (Status, string) {
				if s, d := needOK(r); s != Pass {
					return s, d
				}
				if strings.TrimSpace(r.Content) == "" {
					if len(r.ToolCalls) > 0 {
						return Warn, "called the tool again instead of answering"
					}
					return Fail, "empty content"
				}
				if !strings.Contains(r.Content, "31") {
					return Warn, "answer did not use the tool result"
				}
				return Pass, ""
			},
		},
		{
			ID: "stream_tools", Desc: "tool call assembled from stream deltas", Needs: "tools", Stream: true,
			Body: func(m string) map[string]any {
				return map[string]any{"model": m, "stream": true, "tools": []any{weatherTool}, "tool_choice": "auto",
					"messages": []any{user("What is the weather in Pune right now? Use the get_weather tool.")}}
			},
			Check: func(r *Result) (Status, string) {
				if s, d := needOK(r); s != Pass {
					return s, d
				}
				if !r.Done {
					return Fail, "stream did not end with [DONE]"
				}
				return checkToolCall(r)
			},
		},
		{
			ID: "json", Desc: "JSON mode returns parseable JSON", Needs: "json",
			Body: func(m string) map[string]any {
				return map[string]any{"model": m, "response_format": map[string]any{"type": "json_object"}, "messages": []any{
					map[string]any{"role": "system", "content": "Respond only with a JSON object."},
					user("Return a JSON object with keys name and age for Ada Lovelace, age 36."),
				}}
			},
			Check: func(r *Result) (Status, string) {
				if s, d := needOK(r); s != Pass {
					return s, d
				}
				raw := strings.TrimSpace(r.Content)
				stripped := guard.StripFences(raw)
				var obj map[string]any
				if json.Unmarshal([]byte(stripped), &obj) != nil {
					return Fail, "not valid JSON: " + clip(raw, 80)
				}
				var notes []string
				if stripped != raw {
					notes = append(notes, "wrapped JSON in a code fence")
				}
				if _, ok := obj["name"]; !ok {
					notes = append(notes, "missing key name")
				}
				if _, ok := obj["age"]; !ok {
					notes = append(notes, "missing key age")
				}
				return warnIf(notes)
			},
		},
		{
			ID: "truncation", Desc: "max_tokens is respected and reported as finish_reason length",
			Body: func(m string) map[string]any {
				return map[string]any{"model": m, "max_tokens": 16, "messages": []any{user("Count from 1 to 200, separated by commas.")}}
			},
			Check: func(r *Result) (Status, string) {
				if r.Status == 400 {
					return Warn, "rejected max_tokens 16: " + clip(r.ErrMsg, 80)
				}
				if s, d := needOK(r); s != Pass {
					return s, d
				}
				if r.Finish != "length" {
					return Warn, "finish_reason " + q(r.Finish) + ", want length"
				}
				return Pass, ""
			},
		},
		{
			ID: "bad_model", Desc: "unknown model returns an error, not a silent substitute", DirectOnly: true,
			Body: func(string) map[string]any {
				return map[string]any{"model": "mangoman-conformance-no-such-model", "messages": []any{user("hi")}}
			},
			Check: func(r *Result) (Status, string) {
				switch {
				case r.Status == 400 || r.Status == 404 || r.Status == 422:
					return Pass, fmt.Sprintf("HTTP %d: %s", r.Status, clip(r.ErrMsg, 60))
				case r.Status == 200:
					return Fail, "returned 200: provider substitutes unknown models"
				default:
					return Warn, fmt.Sprintf("unexpected HTTP %d", r.Status)
				}
			},
		},
	}
}

// QuickIDs is the smaller set for a fast check.
var QuickIDs = []string{"basic", "stream", "tools", "json"}

func checkToolCall(r *Result) (Status, string) {
	if s, d := needOK(r); s != Pass {
		return s, d
	}
	if len(r.ToolCalls) == 0 {
		return Fail, "no tool call (content: " + clip(r.Content, 60) + ")"
	}
	tc := r.ToolCalls[0]
	if tc.Function.Name != "get_weather" {
		return Fail, "called unknown tool " + q(tc.Function.Name)
	}
	var args map[string]any
	if json.Unmarshal([]byte(tc.Function.Arguments), &args) != nil {
		return Fail, "arguments are not valid JSON: " + clip(tc.Function.Arguments, 60)
	}
	city, _ := args["city"].(string)
	if !strings.Contains(strings.ToLower(city), "pune") {
		return Warn, "city argument " + q(city)
	}
	if r.Finish != "tool_calls" && r.Finish != "stop" {
		return Warn, "finish_reason " + q(r.Finish)
	}
	return Pass, ""
}

func needOK(r *Result) (Status, string) {
	if r.Status == 429 {
		return Skip, "rate limited"
	}
	if r.Status != 200 {
		return Fail, fmt.Sprintf("HTTP %d: %s", r.Status, clip(r.ErrMsg, 100))
	}
	if r.ErrMsg != "" {
		return Fail, "error in body: " + clip(r.ErrMsg, 100)
	}
	return Pass, ""
}

func warnIf(notes []string) (Status, string) {
	if len(notes) == 0 {
		return Pass, ""
	}
	return Warn, strings.Join(notes, "; ")
}

func q(s string) string {
	if s == "" {
		return `""`
	}
	return s
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// Run sends one case and checks it.
func Run(ctx context.Context, send SendFunc, c Case, model string) CaseResult {
	body, _ := json.Marshal(c.Body(model))
	start := time.Now()
	resp, err := send(ctx, body, c.Stream)
	if err != nil {
		return CaseResult{Case: c.ID, Status: Fail, Detail: "request failed: " + clip(err.Error(), 100)}
	}
	defer resp.Body.Close()
	r := Parse(resp, start)
	st, detail := c.Check(r)
	return CaseResult{
		Case: c.ID, Status: st, Detail: detail, HTTP: r.Status,
		LatencyMS: r.Latency.Milliseconds(), FirstOutMS: r.FirstOutput.Milliseconds(),
		RateHeaders: RateHeaders(resp.Header),
	}
}

// RateHeaders picks rate-limit related headers, so limits can be learned.
func RateHeaders(h http.Header) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "ratelimit") || strings.Contains(lk, "rate-limit") || lk == "retry-after" {
			out[lk] = strings.Join(v, ",")
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Parse reads a response, streaming or not, into a Result.
func Parse(resp *http.Response, start time.Time) *Result {
	r := &Result{Status: resp.StatusCode, Header: resp.Header}
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mt == "text/event-stream" && resp.StatusCode == 200 {
		r.IsSSE = true
		parseStream(resp.Body, r, start)
		r.Latency = time.Since(start)
		return r
	}
	r.Raw, _ = io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	r.Latency = time.Since(start)
	var body struct {
		Choices []struct {
			Message struct {
				Content   *string          `json:"content"`
				ToolCalls []guard.ToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *json.RawMessage `json:"usage"`
		Error any              `json:"error"`
	}
	if json.Unmarshal(r.Raw, &body) != nil {
		if resp.StatusCode != 200 {
			r.ErrMsg = clip(string(r.Raw), 200)
		} else {
			r.ErrMsg = "unparseable body"
		}
		return r
	}
	if body.Error != nil {
		r.ErrMsg = errorText(body.Error)
	}
	if len(body.Choices) > 0 {
		c := body.Choices[0]
		if c.Message.Content != nil {
			r.Content = *c.Message.Content
		}
		r.ToolCalls = c.Message.ToolCalls
		r.Finish = c.FinishReason
	}
	r.HasUsage = body.Usage != nil && string(*body.Usage) != "null"
	return r
}

func errorText(e any) string {
	switch v := e.(type) {
	case string:
		return v
	case map[string]any:
		if m, ok := v["message"].(string); ok {
			return m
		}
	}
	b, _ := json.Marshal(e)
	return string(b)
}

type toolDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func parseStream(body io.Reader, r *Result, start time.Time) {
	var raw bytes.Buffer
	sc := bufio.NewScanner(io.TeeReader(io.LimitReader(body, 8<<20), &raw))
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	var content strings.Builder
	calls := map[int]*guard.ToolCall{}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		r.Events++
		if data == "[DONE]" {
			r.Done = true
			continue
		}
		var ch struct {
			Choices []struct {
				Delta struct {
					Content   *string     `json:"content"`
					ToolCalls []toolDelta `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *json.RawMessage `json:"usage"`
			Error any              `json:"error"`
		}
		if json.Unmarshal([]byte(data), &ch) != nil {
			continue
		}
		if ch.Error != nil {
			r.ErrMsg = errorText(ch.Error)
		}
		if ch.Usage != nil && string(*ch.Usage) != "null" {
			r.HasUsage = true
		}
		for _, c := range ch.Choices {
			if c.Delta.Content != nil && *c.Delta.Content != "" {
				if r.FirstOutput == 0 {
					r.FirstOutput = time.Since(start)
				}
				content.WriteString(*c.Delta.Content)
			}
			for _, td := range c.Delta.ToolCalls {
				if r.FirstOutput == 0 {
					r.FirstOutput = time.Since(start)
				}
				tc, ok := calls[td.Index]
				if !ok {
					tc = &guard.ToolCall{}
					calls[td.Index] = tc
				}
				if td.Function.Name != "" {
					tc.Function.Name = td.Function.Name
				}
				tc.Function.Arguments += td.Function.Arguments
			}
			if c.FinishReason != nil && *c.FinishReason != "" {
				r.Finish = *c.FinishReason
			}
		}
	}
	r.Content = content.String()
	idx := make([]int, 0, len(calls))
	for i := range calls {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for _, i := range idx {
		r.ToolCalls = append(r.ToolCalls, *calls[i])
	}
	r.Raw = raw.Bytes()
}
