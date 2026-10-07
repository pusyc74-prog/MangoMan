package router

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/core"
	"github.com/pusyc74-prog/mangoman/internal/guard"
)

// stream relays an SSE response. Events are buffered until the first one that
// carries real output (content, reasoning or a tool call). Until then nothing
// has reached the client, so a failure or an empty stream fails over to the
// next candidate transparently. After that point the stream is committed: a
// later failure ends the stream with an error event (continuation is a P2
// research item).
func (rt *Router) stream(ctx context.Context, cancel context.CancelFunc, w http.ResponseWriter,
	req *core.Request, c Candidate, class string, n int, resp *http.Response, addedUsage bool) attemptResult {

	if mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mt != "text/event-stream" {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		rt.Breakers.Failure(c.Target())
		return attemptResult{outcome: "not_a_stream", status: resp.StatusCode, errMsg: upstreamMessage(data)}
	}

	body := newIdleReader(resp.Body, rt.StreamIdle, cancel)
	defer body.stop()
	br := bufio.NewReaderSize(body, 64<<10)

	flusher, _ := w.(http.Flusher)
	started := time.Now()
	var firstOut time.Duration
	var (
		pending   bytes.Buffer // events held before commit
		event     bytes.Buffer // current event
		committed bool
		sawDone   bool
		outChars  int
		usageTot  int
		finish    string
	)
	commit := func() {
		rt.setHeaders(w, c, class, n)
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("Connection", "keep-alive")
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(pending.Bytes())
		pending.Reset()
		if flusher != nil {
			flusher.Flush()
		}
		committed = true
		firstOut = time.Since(started)
		// The answer is on its way to the client, so there is no other model
		// to fall back to any more. A long pause now is worth waiting out
		// rather than killing the answer: measured on real free models, a
		// model writing a long structured answer goes quiet for over a
		// minute, and cutting it there lost whole tasks.
		body.grow(rt.StreamStall)
	}
	finishEvent := func() {
		if event.Len() == 0 {
			return
		}
		event.WriteByte('\n')
		if committed {
			_, _ = w.Write(event.Bytes())
			if flusher != nil {
				flusher.Flush()
			}
		} else {
			pending.Write(event.Bytes())
		}
		event.Reset()
	}

	var readErr error
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := bytes.TrimRight(line, "\r\n")
			if len(trimmed) == 0 {
				finishEvent()
			} else {
				if data, ok := sseData(trimmed); ok {
					if string(data) == "[DONE]" {
						sawDone = true
					} else {
						d := parseDelta(data)
						outChars += d.chars
						if d.usage > 0 {
							usageTot = d.usage
						}
						if addedUsage && d.usageOnly {
							continue // we asked for this chunk; the client did not
						}
						if d.finish != "" {
							finish = d.finish
						}
						if d.err != "" && !committed {
							// Error delivered inside a 200 stream before any output.
							rt.Breakers.Failure(c.Target())
							return attemptResult{outcome: "stream_error", status: 200, errMsg: d.err}
						}
						if !committed && d.meaningful {
							event.Write(line)
							finishEvent() // ensure this event goes out with the commit
							commit()
							continue
						}
					}
				}
				event.Write(line)
				if !bytes.HasSuffix(line, []byte("\n")) {
					event.WriteByte('\n')
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				readErr = err
			}
			break
		}
	}
	finishEvent()

	// The client hung up: not the provider's fault, nothing left to do.
	clientGone := readErr != nil && ctx.Err() != nil && !body.fired.Load()

	tokens := usageTot
	if tokens == 0 {
		tokens = req.EstTokens + outChars/4
	}

	if clientGone {
		rt.record(c, tokens)
		return attemptResult{done: true, outcome: "client_gone", tokens: tokens}
	}
	if !committed {
		if readErr != nil || !sawDone {
			rt.Breakers.Failure(c.Target())
			msg := "stream ended early"
			if readErr != nil {
				msg = readErr.Error()
			}
			return attemptResult{outcome: "stream_error", status: 200, errMsg: msg}
		}
		// Clean end with no output at all: an empty answer.
		rt.Breakers.Success(c.Target())
		rt.record(c, tokens)
		return attemptResult{outcome: "quality:" + guard.Empty, status: 200, errMsg: "empty stream", tokens: tokens}
	}

	rt.record(c, tokens)
	if readErr != nil || !sawDone {
		rt.Breakers.Failure(c.Target())
		msg := "upstream stream ended early"
		if readErr != nil {
			msg = "upstream stream failed: " + readErr.Error()
		}
		writeStreamError(w, flusher, msg)
		return attemptResult{done: true, outcome: "stream_broken_after_commit", status: 200, errMsg: msg, tokens: tokens}
	}
	rt.Breakers.Success(c.Target())
	out := "ok"
	if finish == "length" && req.MaxTokens == 0 {
		out = "ok_truncated" // already streamed; logged for the quality score
	}
	return attemptResult{done: true, outcome: out, status: 200, tokens: tokens, firstOut: firstOut}
}

func writeStreamError(w io.Writer, f http.Flusher, msg string) {
	b, _ := json.Marshal(map[string]any{"error": map[string]any{"message": msg, "type": "upstream_stream_error"}})
	_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
	if f != nil {
		f.Flush()
	}
}

func sseData(line []byte) ([]byte, bool) {
	if !bytes.HasPrefix(line, []byte("data:")) {
		return nil, false
	}
	return bytes.TrimSpace(line[5:]), true
}

type delta struct {
	meaningful bool
	chars      int
	finish     string
	usage      int
	usageOnly  bool
	err        string
}

func parseDelta(data []byte) delta {
	var ch struct {
		Choices []struct {
			Delta struct {
				Content          *string           `json:"content"`
				Reasoning        *string           `json:"reasoning"`
				ReasoningContent *string           `json:"reasoning_content"`
				ToolCalls        []json.RawMessage `json:"tool_calls"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			Total int `json:"total_tokens"`
		} `json:"usage"`
		Error any `json:"error"`
	}
	var d delta
	if json.Unmarshal(data, &ch) != nil {
		return d
	}
	if ch.Error != nil {
		b, _ := json.Marshal(ch.Error)
		d.err = upstreamMessage([]byte(`{"error":` + string(b) + `}`))
		return d
	}
	if ch.Usage != nil {
		d.usage = ch.Usage.Total
		d.usageOnly = len(ch.Choices) == 0
	}
	for _, c := range ch.Choices {
		for _, s := range []*string{c.Delta.Content, c.Delta.Reasoning, c.Delta.ReasoningContent} {
			if s != nil && strings.TrimSpace(*s) != "" {
				d.meaningful = true
				d.chars += len(*s)
			}
		}
		if len(c.Delta.ToolCalls) > 0 {
			d.meaningful = true
			for _, tc := range c.Delta.ToolCalls {
				d.chars += len(tc)
			}
		}
		if c.FinishReason != nil {
			d.finish = *c.FinishReason
		}
	}
	return d
}

// idleReader cancels the attempt when the upstream sends nothing for too long.
type idleReader struct {
	r       io.Reader
	timeout time.Duration
	timer   *time.Timer
	once    sync.Once
	fired   atomic.Bool
}

func newIdleReader(r io.Reader, d time.Duration, cancel context.CancelFunc) *idleReader {
	if d <= 0 {
		d = 60 * time.Second
	}
	ir := &idleReader{r: r, timeout: d}
	ir.timer = time.AfterFunc(d, func() { ir.fired.Store(true); cancel() })
	return ir
}

// grow lengthens the gap allowed from here on. Called on the reading
// goroutine, so timeout needs no lock.
func (i *idleReader) grow(d time.Duration) {
	if d <= i.timeout {
		return
	}
	i.timeout = d
	i.timer.Reset(d)
}

func (i *idleReader) Read(p []byte) (int, error) {
	n, err := i.r.Read(p)
	if n > 0 {
		i.timer.Reset(i.timeout)
	}
	return n, err
}

func (i *idleReader) stop() { i.once.Do(func() { i.timer.Stop() }) }
