package router

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/catalogue"
	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/core"
	"github.com/pusyc74-prog/mangoman/internal/keys"
)

type memStore map[string]string

func (m memStore) Get(p string) (string, error) {
	if v, ok := m[p]; ok {
		return v, nil
	}
	return "", keys.ErrNotFound
}
func (m memStore) Set(p, k string) error { m[p] = k; return nil }
func (m memStore) Delete(p string) error { delete(m, p); return nil }
func (memStore) Name() string            { return "mem" }

// fake is one fake upstream provider.
type fake struct {
	id      string
	model   string  // canonical model it serves
	quality float64 // quality score for every class
	speed   float64
	handler http.HandlerFunc
	calls   atomic.Int32
	srv     *httptest.Server
}

func okJSON(content string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"total_tokens":42}}`, content)
	}
}

func status(code int, extra map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		for k, v := range extra {
			w.Header().Set(k, v)
		}
		w.WriteHeader(code)
		fmt.Fprintf(w, `{"error":{"message":"upstream said %d"}}`, code)
	}
}

func sse(events ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		for _, e := range events {
			fmt.Fprintf(w, "data: %s\n\n", e)
			f.Flush()
		}
	}
}

func chunk(content string) string {
	b, _ := json.Marshal(content)
	return `{"choices":[{"index":0,"delta":{"content":` + string(b) + `},"finish_reason":null}]}`
}

const roleChunk = `{"choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`
const stopChunk = `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"total_tokens":10}}`

func setup(t *testing.T, fakes ...*fake) *Router {
	t.Helper()
	cat := catalogue.Catalogue{Version: "test"}
	store := memStore{}
	for _, f := range fakes {
		f := f
		f.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f.calls.Add(1)
			if r.Header.Get("Authorization") != "Bearer key-"+f.id {
				http.Error(w, "bad key", 401)
				return
			}
			f.handler(w, r)
		}))
		t.Cleanup(f.srv.Close)
		speed := f.speed
		if speed == 0 {
			speed = 0.5
		}
		cat.Providers = append(cat.Providers, catalogue.Provider{
			ID: f.id, Name: f.id, BaseURL: f.srv.URL, Kind: "openai", NeedsKey: true, Speed: speed,
			Policy: catalogue.DataPolicy{Retention: "none", TrainsOnData: "no", Jurisdiction: "US"},
		})
		cat.Models = append(cat.Models, catalogue.Model{
			Canonical: f.model, Provider: f.id, Upstream: f.model + "-up", Free: true, Context: 32000,
			Caps: []string{"tools", "json", "streaming"}, Limits: catalogue.Limits{RPM: 100},
			Quality: map[string]float64{"default": f.quality},
		})
		store[f.id] = "key-" + f.id
	}
	data, _ := json.Marshal(&cat)
	parsed, err := catalogue.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	rt := New(parsed, keys.NewResolver(store, nil), &config.Config{MaxAttempts: 6})
	if len(fakes) > 0 {
		rt.Client.HTTP = fakes[0].srv.Client() // all httptest TLS servers share one cert
	}
	rt.StreamIdle = 2 * time.Second
	return rt
}

func do(t *testing.T, rt *Router, body string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := core.ParseChat([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rt.Handle(w, r, req)
	return w
}

const hello = `{"model":"free/auto","messages":[{"role":"user","content":"hello there"}]}`
const helloStream = `{"model":"free/auto","stream":true,"messages":[{"role":"user","content":"hello there"}]}`

func TestSuccessPicksBestAndLabels(t *testing.T) {
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: okJSON("from a")}
	b := &fake{id: "b", model: "m2", quality: 0.3, handler: okJSON("from b")}
	rt := setup(t, a, b)
	w := do(t, rt, hello)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "from a") {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	if w.Header().Get("X-MangoMan-Provider") != "a" || w.Header().Get("X-MangoMan-Attempts") != "1" {
		t.Fatalf("headers %v", w.Header())
	}
	if !strings.Contains(w.Header().Get("X-MangoMan-Data-Policy"), "no training") {
		t.Fatalf("policy header missing: %v", w.Header())
	}
	if b.calls.Load() != 0 {
		t.Fatal("b should not be called")
	}
}

func TestFailoverOn429BlocksBucket(t *testing.T) {
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: status(429, map[string]string{"Retry-After": "120"})}
	b := &fake{id: "b", model: "m2", quality: 0.3, handler: okJSON("from b")}
	rt := setup(t, a, b)
	w := do(t, rt, hello)
	if w.Code != 200 || w.Header().Get("X-MangoMan-Provider") != "b" || w.Header().Get("X-MangoMan-Attempts") != "2" {
		t.Fatalf("got %d %v %s", w.Code, w.Header(), w.Body)
	}
	// The blocked bucket is skipped without a call on the next request.
	do(t, rt, hello)
	if a.calls.Load() != 1 {
		t.Fatalf("a called %d times, want 1", a.calls.Load())
	}
}

func TestFailoverOn5xxOpensBreaker(t *testing.T) {
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: status(503, nil)}
	b := &fake{id: "b", model: "m2", quality: 0.3, handler: okJSON("from b")}
	rt := setup(t, a, b)
	for i := 0; i < 5; i++ {
		if w := do(t, rt, hello); w.Code != 200 {
			t.Fatalf("request %d: %d %s", i, w.Code, w.Body)
		}
	}
	if got := a.calls.Load(); got != 3 {
		t.Fatalf("a called %d times, want 3 (breaker opens after 3)", got)
	}
}

func TestSameModelFirst(t *testing.T) {
	// a and c serve m1, b serves m2 with a score between them.
	a := &fake{id: "a", model: "m1", quality: 0.9, speed: 0.9, handler: status(500, nil)}
	b := &fake{id: "b", model: "m2", quality: 0.8, speed: 0.9, handler: okJSON("from b")}
	c := &fake{id: "c", model: "m1", quality: 0.9, speed: 0.1, handler: okJSON("from c")}
	rt := setup(t, a, b, c)
	w := do(t, rt, hello)
	if w.Header().Get("X-MangoMan-Provider") != "c" {
		t.Fatalf("want same model on provider c first, got %s", w.Header().Get("X-MangoMan-Provider"))
	}
	if b.calls.Load() != 0 {
		t.Fatal("different model tried before same model on another provider")
	}
}

func TestExplicitModelPinned(t *testing.T) {
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: okJSON("from a")}
	b := &fake{id: "b", model: "m2", quality: 0.1, handler: okJSON("from b")}
	rt := setup(t, a, b)
	w := do(t, rt, `{"model":"m2","messages":[{"role":"user","content":"hi"}]}`)
	if w.Header().Get("X-MangoMan-Provider") != "b" {
		t.Fatalf("explicit model ignored: %v", w.Header())
	}
}

func TestEmptyAnswerFailsOver(t *testing.T) {
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: okJSON("   ")}
	b := &fake{id: "b", model: "m2", quality: 0.3, handler: okJSON("real answer")}
	rt := setup(t, a, b)
	w := do(t, rt, hello)
	if !strings.Contains(w.Body.String(), "real answer") {
		t.Fatalf("got %s", w.Body)
	}
}

func TestInvalidJSONFailsOverAndFallbackLabelled(t *testing.T) {
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: okJSON("not json")}
	b := &fake{id: "b", model: "m2", quality: 0.3, handler: okJSON("```json\n{\"ok\":true}\n```")}
	rt := setup(t, a, b)
	body := `{"model":"free/auto","response_format":{"type":"json_object"},"messages":[{"role":"user","content":"give json"}]}`
	if w := do(t, rt, body); w.Header().Get("X-MangoMan-Provider") != "b" {
		t.Fatalf("want b, got %v %s", w.Header(), w.Body)
	}
	// When every answer fails the guard, the first is returned and labelled.
	b.handler = okJSON("also not json")
	rt2 := setup(t, &fake{id: "a", model: "m1", quality: 0.9, handler: okJSON("not json")},
		&fake{id: "b", model: "m2", quality: 0.3, handler: okJSON("also not json")})
	w := do(t, rt2, body)
	if w.Code != 200 || w.Header().Get("X-MangoMan-Guard") != "invalid_json" {
		t.Fatalf("got %d %v", w.Code, w.Header())
	}
}

func TestAllRateLimitedReturns429WithRetryAfter(t *testing.T) {
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: status(429, map[string]string{"Retry-After": "30"})}
	rt := setup(t, a)
	w := do(t, rt, hello)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("got %d %v", w.Code, w.Header())
	}
	// Second request: pre-flight already knows, no upstream call.
	w = do(t, rt, hello)
	if w.Code != 429 || a.calls.Load() != 1 {
		t.Fatalf("got %d, calls %d", w.Code, a.calls.Load())
	}
}

func TestNoKeys(t *testing.T) {
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: okJSON("x")}
	rt := setup(t, a)
	rt.Keys = keys.NewResolver(memStore{}, nil)
	w := do(t, rt, hello)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "no_providers_connected") {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
}

func TestKeyRejectedFailsOver(t *testing.T) {
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: okJSON("x")}
	b := &fake{id: "b", model: "m2", quality: 0.3, handler: okJSON("from b")}
	rt := setup(t, a, b)
	rt.Keys = keys.NewResolver(memStore{"a": "wrong", "b": "key-b"}, nil)
	w := do(t, rt, hello)
	if w.Header().Get("X-MangoMan-Provider") != "b" {
		t.Fatalf("got %v", w.Header())
	}
	do(t, rt, hello)
	if a.calls.Load() != 1 {
		t.Fatal("rejected key should be disabled")
	}
}

func TestClientErrorStopsAfterTwo(t *testing.T) {
	bad := status(400, nil)
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: bad}
	b := &fake{id: "b", model: "m2", quality: 0.5, handler: bad}
	c := &fake{id: "c", model: "m3", quality: 0.1, handler: okJSON("x")}
	rt := setup(t, a, b, c)
	w := do(t, rt, hello)
	if w.Code != 400 || c.calls.Load() != 0 {
		t.Fatalf("got %d, c calls %d", w.Code, c.calls.Load())
	}
}

func TestToolsFilterAndBadToolCall(t *testing.T) {
	badTool := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"1","type":"function","function":{"name":"nope","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
	}
	goodTool := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Pune\"}"}}]},"finish_reason":"tool_calls"}]}`)
	}
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: badTool}
	b := &fake{id: "b", model: "m2", quality: 0.3, handler: goodTool}
	rt := setup(t, a, b)
	body := `{"model":"free/auto","tools":[{"type":"function","function":{"name":"get_weather","parameters":{}}}],"messages":[{"role":"user","content":"weather?"}]}`
	w := do(t, rt, body)
	if w.Header().Get("X-MangoMan-Provider") != "b" || !strings.Contains(w.Body.String(), "get_weather") {
		t.Fatalf("got %v %s", w.Header(), w.Body)
	}
}

func TestStreamRelays(t *testing.T) {
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: sse(roleChunk, chunk("Hel"), chunk("lo"), stopChunk, "[DONE]")}
	rt := setup(t, a)
	w := do(t, rt, helloStream)
	out := w.Body.String()
	if w.Code != 200 || w.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("got %d %v", w.Code, w.Header())
	}
	for _, want := range []string{`"role":"assistant"`, `"Hel"`, `"lo"`, "data: [DONE]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stream missing %s:\n%s", want, out)
		}
	}
	if strings.Count(out, "\n\n") < 5 {
		t.Fatalf("events not separated:\n%q", out)
	}
}

func TestStreamEmptyFailsOver(t *testing.T) {
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: sse(roleChunk, stopChunk, "[DONE]")}
	b := &fake{id: "b", model: "m2", quality: 0.3, handler: sse(roleChunk, chunk("from b"), stopChunk, "[DONE]")}
	rt := setup(t, a, b)
	w := do(t, rt, helloStream)
	if w.Header().Get("X-MangoMan-Provider") != "b" || !strings.Contains(w.Body.String(), "from b") {
		t.Fatalf("got %v\n%s", w.Header(), w.Body)
	}
	if strings.Count(w.Body.String(), `"role":"assistant"`) != 1 {
		t.Fatalf("events from the failed attempt leaked:\n%s", w.Body)
	}
}

func TestStreamErrorBeforeFirstTokenFailsOver(t *testing.T) {
	broken := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", roleChunk)
		w.(http.Flusher).Flush()
		// Connection drops without [DONE].
		hj, _ := w.(http.Hijacker)
		if hj != nil {
			c, _, _ := hj.Hijack()
			c.Close()
		}
	}
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: broken}
	b := &fake{id: "b", model: "m2", quality: 0.3, handler: sse(chunk("ok from b"), "[DONE]")}
	rt := setup(t, a, b)
	w := do(t, rt, helloStream)
	if w.Header().Get("X-MangoMan-Provider") != "b" || !strings.Contains(w.Body.String(), "ok from b") {
		t.Fatalf("got %v\n%s", w.Header(), w.Body)
	}
}

func TestStreamErrorEventFailsOver(t *testing.T) {
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: sse(`{"error":{"message":"overloaded"}}`)}
	b := &fake{id: "b", model: "m2", quality: 0.3, handler: sse(chunk("ok"), "[DONE]")}
	rt := setup(t, a, b)
	w := do(t, rt, helloStream)
	if w.Header().Get("X-MangoMan-Provider") != "b" {
		t.Fatalf("got %v\n%s", w.Header(), w.Body)
	}
}

func TestStreamIdleTimeout(t *testing.T) {
	stall := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: stall}
	b := &fake{id: "b", model: "m2", quality: 0.3, handler: sse(chunk("ok"), "[DONE]")}
	rt := setup(t, a, b)
	rt.StreamIdle = 300 * time.Millisecond
	start := time.Now()
	w := do(t, rt, helloStream)
	if w.Header().Get("X-MangoMan-Provider") != "b" || time.Since(start) > 3*time.Second {
		t.Fatalf("got %v after %s", w.Header(), time.Since(start))
	}
}

func TestContextTooLongFiltered(t *testing.T) {
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: okJSON("x")}
	rt := setup(t, a)
	long := strings.Repeat("word ", 40000) // about 50k tokens, model context is 32k
	body, _ := json.Marshal(map[string]any{"model": "free/auto", "messages": []map[string]string{{"role": "user", "content": long}}})
	w := do(t, rt, string(body))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "no_model_fits") {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
}

func TestUnreachableProviderSkipsItsOtherModels(t *testing.T) {
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: okJSON("x")}
	b := &fake{id: "b", model: "m2", quality: 0.3, handler: okJSON("from b")}
	rt := setup(t, a, b)
	// Add a second model on provider a, then take a's host down.
	m := rt.Cat.AllModels()[0]
	m.Canonical, m.Upstream = "m1b", "m1b-up"
	rt.Cat.ReplaceProviderModels("a", append([]catalogue.Model{rt.Cat.AllModels()[0]}, m))
	a.srv.Close()
	w := do(t, rt, hello)
	if w.Header().Get("X-MangoMan-Provider") != "b" || w.Header().Get("X-MangoMan-Attempts") != "2" {
		t.Fatalf("want b on attempt 2, got %v", w.Header())
	}
}
