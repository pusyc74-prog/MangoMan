package router

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/catalogue"
	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/core"
	"github.com/pusyc74-prog/mangoman/internal/keys"
	"github.com/pusyc74-prog/mangoman/internal/quota"
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
	quirks  catalogue.Quirks
	account catalogue.Limits
	limits  catalogue.Limits
	local   bool
	prio    int
	noTeam  bool   // catalogue switch: team keys off for this provider
	trains  string // data policy: trains on data ("" = "no")
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
		// HTTP/2, as real providers speak: all requests share one connection.
		f.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f.calls.Add(1)
			// Own key "key-<id>", team keys "key-<id>-<name>".
			if a := r.Header.Get("Authorization"); a != "Bearer key-"+f.id && !strings.HasPrefix(a, "Bearer key-"+f.id+"-") {
				http.Error(w, "bad key", 401)
				return
			}
			f.handler(w, r)
		}))
		f.srv.EnableHTTP2 = true
		f.srv.StartTLS()
		t.Cleanup(f.srv.Close)
		speed := f.speed
		if speed == 0 {
			speed = 0.5
		}
		cat.Providers = append(cat.Providers, catalogue.Provider{
			ID: f.id, Name: f.id, BaseURL: f.srv.URL, Kind: "openai", NeedsKey: true, Speed: speed,
			Policy: catalogue.DataPolicy{Retention: "none", TrainsOnData: cmp.Or(f.trains, "no"), Jurisdiction: "US"},
			Quirks: f.quirks, AccountLimits: f.account, Local: f.local, Priority: f.prio, NoTeamKeys: f.noTeam,
		})
		cat.Models = append(cat.Models, catalogue.Model{
			Canonical: f.model, Provider: f.id, Upstream: f.model + "-up", Free: true, Context: 32000,
			Caps: []string{"tools", "json", "streaming"}, Limits: limitsOr(f.limits),
			Quality: map[string]float64{"default": f.quality},
		})
		store[f.id] = "key-" + f.id
	}
	data, _ := json.Marshal(&cat)
	parsed, err := catalogue.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	rt := New(parsed, keys.NewResolver(store, nil), &config.Config{MaxAttempts: 6, AllowWeaker: true})
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
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: status(500, nil)}
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

// Once the answer has started there is nothing to fail over to, so a long
// pause in the middle must be waited out, not cut off at the gap limit.
func TestStallAfterFirstWordsIsWaitedOut(t *testing.T) {
	pause := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", chunk("first words"))
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			return
		case <-time.After(400 * time.Millisecond):
		}
		sse(chunk(" and the rest"), "[DONE]")(w, r)
	}
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: pause}
	rt := setup(t, a)
	rt.StreamIdle, rt.StreamStall = 100*time.Millisecond, 3*time.Second
	w := do(t, rt, helloStream)
	if !strings.Contains(w.Body.String(), "and the rest") {
		t.Fatalf("the answer was cut off mid-stream: %s", w.Body)
	}
}

// With one model there is nothing to fail over to, so a stream that dies
// before saying a word must be asked again instead of killing the request.
func TestStreamErrorRetriesTheOnlyModel(t *testing.T) {
	var calls atomic.Int32
	flaky := func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: %s\n\n", roleChunk)
			w.(http.Flusher).Flush()
			if hj, ok := w.(http.Hijacker); ok {
				c, _, _ := hj.Hijack()
				c.Close()
			}
			return
		}
		sse(chunk("ok at last"), "[DONE]")(w, r)
	}
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: flaky}
	rt := setup(t, a)
	w := do(t, rt, helloStream)
	if !strings.Contains(w.Body.String(), "ok at last") {
		t.Fatalf("after %d calls got %v\n%s", calls.Load(), w.Header(), w.Body)
	}
}

// Measured on real free models: the model was overloaded and its only other
// provider was out of its daily limit, which ended whole tasks.
func TestOverloadedModelAskedAgainWhenTheOthersAreLimited(t *testing.T) {
	var calls atomic.Int32
	flaky := func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			sse(`{"error":{"message":"Service temporarily overloaded"}}`)(w, r)
			return
		}
		sse(chunk("ok at last"), "[DONE]")(w, r)
	}
	limited := func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"free-models-per-day"}}`, http.StatusTooManyRequests)
	}
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: flaky}
	b := &fake{id: "b", model: "m2", quality: 0.3, handler: limited}
	rt := setup(t, a, b)
	w := do(t, rt, helloStream)
	if !strings.Contains(w.Body.String(), "ok at last") || calls.Load() != 2 {
		t.Fatalf("after %d calls got %v\n%s", calls.Load(), w.Header(), w.Body)
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
	// Later requests stop spending their time on a model that stalls: it is
	// tried last for a while, and the time it spent counts in its speed.
	calls := a.calls.Load()
	start = time.Now()
	if w := do(t, rt, helloStream); w.Header().Get("X-MangoMan-Provider") != "b" || a.calls.Load() != calls || time.Since(start) > time.Second {
		t.Fatalf("the silent model was asked first again: %v", w.Header())
	}
	for _, s := range rt.Health.Snapshot() {
		if s.Target == "a/m1" && s.LatencyMS < 300 {
			t.Fatalf("the time spent stalling was not recorded: %v ms", s.LatencyMS)
		}
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

func TestQuirksAppliedUpstream(t *testing.T) {
	var got map[string]any
	capture := func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		okJSON("fine")(w, r)
	}
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: capture,
		quirks: catalogue.Quirks{DropParams: []string{"logit_bias"}, MaxTokensField: "max_completion_tokens"}}
	rt := setup(t, a)
	w := do(t, rt, `{"model":"free/auto","max_tokens":20,"logit_bias":{"1":2},"messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	if _, ok := got["logit_bias"]; ok {
		t.Fatal("logit_bias reached the provider")
	}
	if got["max_completion_tokens"] != float64(20) || got["model"] != "m1-up" {
		t.Fatalf("upstream body %v", got)
	}
}

func TestStreamUsageChunkHiddenWhenWeAddedIt(t *testing.T) {
	var sawOpts bool
	h := func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		so, _ := body["stream_options"].(map[string]any)
		sawOpts = so["include_usage"] == true
		sse(chunk("hi"), stopChunk, `{"choices":[],"usage":{"total_tokens":77}}`, "[DONE]")(w, r)
	}
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: h, quirks: catalogue.Quirks{StreamUsage: true}}
	rt := setup(t, a)
	w := do(t, rt, helloStream)
	if !sawOpts {
		t.Fatal("include_usage not requested upstream")
	}
	if strings.Contains(w.Body.String(), `"choices":[]`) {
		t.Fatalf("usage-only chunk leaked to a client that did not ask:\n%s", w.Body)
	}
	if snap := rt.Quota.Snapshot(); len(snap) != 1 || snap[0].TokToday != 77 {
		t.Fatalf("exact usage not recorded: %+v", snap)
	}
	// A client that asked for usage gets the chunk.
	w = do(t, rt, `{"model":"free/auto","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`)
	if !strings.Contains(w.Body.String(), `"choices":[]`) {
		t.Fatalf("usage chunk missing for a client that asked:\n%s", w.Body)
	}
}

func TestErrorInside200FailsOver(t *testing.T) {
	errBody := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"error":{"code":429,"message":"free model rate limited upstream"}}`)
	}
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: errBody}
	b := &fake{id: "b", model: "m2", quality: 0.3, handler: okJSON("from b")}
	rt := setup(t, a, b)
	w := do(t, rt, hello)
	if w.Header().Get("X-MangoMan-Provider") != "b" {
		t.Fatalf("got %v %s", w.Header(), w.Body)
	}
	do(t, rt, hello)
	if a.calls.Load() != 1 {
		t.Fatal("429 inside a 200 should block the bucket")
	}
}

func TestGone410FailsOverWithoutStopping(t *testing.T) {
	// Two retired models (410) must not count as client errors that end the
	// request: the third candidate still answers.
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: status(410, nil)}
	b := &fake{id: "b", model: "m2", quality: 0.8, handler: status(410, nil)}
	c := &fake{id: "c", model: "m3", quality: 0.1, handler: okJSON("from c")}
	rt := setup(t, a, b, c)
	w := do(t, rt, hello)
	if w.Code != 200 || w.Header().Get("X-MangoMan-Provider") != "c" {
		t.Fatalf("got %d %v %s", w.Code, w.Header(), w.Body)
	}
	do(t, rt, hello)
	if a.calls.Load() != 1 || b.calls.Load() != 1 {
		t.Fatal("retired models should be avoided after a 410")
	}
}

func TestAccountLimitSharedAcrossModels(t *testing.T) {
	// Provider a allows 2 requests a day in total, across both its models.
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: okJSON("from a"), account: catalogue.Limits{RPD: 2}}
	b := &fake{id: "b", model: "m2", quality: 0.1, handler: okJSON("from b")}
	rt := setup(t, a, b)
	m := rt.Cat.AllModels()[0]
	m.Canonical, m.Upstream = "m1b", "m1b-up"
	rt.Cat.ReplaceProviderModels("a", append([]catalogue.Model{rt.Cat.AllModels()[0]}, m))
	got := []string{}
	for i := 0; i < 4; i++ {
		got = append(got, do(t, rt, hello).Header().Get("X-MangoMan-Provider"))
	}
	if strings.Join(got, ",") != "a,a,b,b" {
		t.Fatalf("providers used: %v (want a,a then b once the account cap is hit)", got)
	}
}

func TestMeasuredSpeedReranks(t *testing.T) {
	// a looks best on paper (quality and catalogue speed); b looks slow.
	a := &fake{id: "a", model: "m1", quality: 0.8, speed: 0.9, handler: okJSON("from a")}
	b := &fake{id: "b", model: "m2", quality: 0.75, speed: 0.3, handler: okJSON("from b")}
	rt := setup(t, a, b)
	fast := `{"model":"free/fast","messages":[{"role":"user","content":"hi"}]}`
	if w := do(t, rt, fast); w.Header().Get("X-MangoMan-Provider") != "a" {
		t.Fatalf("with no measurements the catalogue estimate should pick a, got %s", w.Header().Get("X-MangoMan-Provider"))
	}
	ca := Candidate{Model: catalogue.Model{Provider: "a", Canonical: "m1"}, Provider: catalogue.Provider{ID: "a"}}
	cb := Candidate{Model: catalogue.Model{Provider: "b", Canonical: "m2"}, Provider: catalogue.Provider{ID: "b"}}
	// In practice a takes 15 s and b 0.3 s.
	for i := 0; i < 3; i++ {
		rt.Health.Observe(ca, "ok", true, 15*time.Second)
		rt.Health.Observe(cb, "ok", true, 300*time.Millisecond)
	}
	// A new chat (the first one would stay on a).
	fast = `{"model":"free/fast","messages":[{"role":"user","content":"hello"}]}`
	if w := do(t, rt, fast); w.Header().Get("X-MangoMan-Provider") != "b" {
		t.Fatalf("measured speed should rerank to b, got %s", w.Header().Get("X-MangoMan-Provider"))
	}
	if sp, ok := rt.Health.Speed("a/m1"); !ok || sp > 0.3 {
		t.Fatalf("speed for slow model %v %v", sp, ok)
	}
	snap := rt.Health.Snapshot()
	if len(snap) != 2 || snap[0].Samples < 3 {
		t.Fatalf("snapshot %+v", snap)
	}
}

func TestForbiddenModelDoesNotDisableKey(t *testing.T) {
	forbidden := func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b.Model == "m1-up" {
			w.WriteHeader(403)
			io.WriteString(w, `{"error":{"message":"m1 is only available on agentic harnesses"}}`)
			return
		}
		okJSON("from m1b")(w, r)
	}
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: forbidden}
	rt := setup(t, a)
	m := rt.Cat.AllModels()[0]
	m.Canonical, m.Upstream, m.Quality = "m1b", "m1b-up", map[string]float64{"default": 0.5}
	rt.Cat.ReplaceProviderModels("a", append([]catalogue.Model{rt.Cat.AllModels()[0]}, m))
	w := do(t, rt, hello)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "from m1b") {
		t.Fatalf("other model on the same provider should answer: %d %s", w.Code, w.Body)
	}
	if k, _ := rt.Keys.Get("a"); k == "" || rt.Keys.Rejected("a") {
		t.Fatal("a 403 on one model must not disable the provider key")
	}
}

func TestMyListFirstThenFallsThrough(t *testing.T) {
	// The router alone would pick a (best quality). My list says c, then b.
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: okJSON("from a")}
	b := &fake{id: "b", model: "m2", quality: 0.5, handler: okJSON("from b")}
	c := &fake{id: "c", model: "m3", quality: 0.2, handler: status(429, map[string]string{"Retry-After": "600"})}
	rt := setup(t, a, b, c)
	rt.Cfg.SetFavorites([]string{"m3", "b/m2"})

	w := do(t, rt, hello)
	if w.Header().Get("X-MangoMan-Provider") != "b" || w.Header().Get("X-MangoMan-Attempts") != "2" {
		t.Fatalf("want c tried first (rate limited), then b from My list; got %v", w.Header())
	}
	// c is now exhausted: b answers on the first attempt.
	if w := do(t, rt, hello); w.Header().Get("X-MangoMan-Provider") != "b" || w.Header().Get("X-MangoMan-Attempts") != "1" {
		t.Fatalf("exhausted list entry should be skipped: %v", w.Header())
	}
	// b fails: the router's own choice (a) takes over.
	b.handler = status(503, nil)
	if w := do(t, rt, hello); w.Header().Get("X-MangoMan-Provider") != "a" {
		t.Fatalf("after My list is exhausted the router should pick a: %v", w.Header())
	}
	// An explicitly requested model still wins over My list.
	if w := do(t, rt, `{"model":"m1","messages":[{"role":"user","content":"hi"}]}`); w.Header().Get("X-MangoMan-Provider") != "a" || w.Header().Get("X-MangoMan-Attempts") != "1" {
		t.Fatalf("explicit model should be first: %v", w.Header())
	}
}

func TestMyListAsksBeforeLeaving(t *testing.T) {
	// My list holds b only. When b is busy the router must stop and ask
	// rather than use a, until the user allows other models.
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: okJSON("from a")}
	b := &fake{id: "b", model: "m2", quality: 0.8, handler: okJSON("from b")}
	rt := setup(t, a, b)
	rt.Cfg.AllowWeaker = false
	rt.Cfg.SetFavorites([]string{"m2"})
	if w := do(t, rt, hello); w.Header().Get("X-MangoMan-Provider") != "b" {
		t.Fatalf("My list entry should answer: %v", w.Header())
	}
	b.handler = status(429, map[string]string{"Retry-After": "600"})
	w := do(t, rt, hello)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "your preferred models are busy") || a.calls.Load() != 0 {
		t.Fatalf("should ask before leaving My list: %d %s (a calls %d)", w.Code, w.Body, a.calls.Load())
	}
	if o := rt.Outlook(); !o.StrongBusy || !o.HasList {
		t.Fatalf("outlook should say the preferred models are busy: %+v", o)
	}
	rt.AllowWeakerFor(time.Hour)
	if w := do(t, rt, hello); w.Header().Get("X-MangoMan-Provider") != "a" {
		t.Fatalf("allowed for an hour, a should answer: %v %s", w.Header(), w.Body)
	}
}

func TestMyListProviderPin(t *testing.T) {
	// Same model on two providers; the list pins provider b.
	a := &fake{id: "a", model: "m1", quality: 0.9, speed: 0.9, handler: okJSON("from a")}
	b := &fake{id: "b", model: "m1", quality: 0.9, speed: 0.1, handler: okJSON("from b")}
	rt := setup(t, a, b)
	rt.Cfg.SetFavorites([]string{"b/m1"})
	if w := do(t, rt, hello); w.Header().Get("X-MangoMan-Provider") != "b" {
		t.Fatalf("provider pin ignored: %v", w.Header())
	}
	rt.Cfg.SetFavorites([]string{"m1"})
	if w := do(t, rt, hello); w.Header().Get("X-MangoMan-Provider") != "a" {
		t.Fatalf("model-wide entry should use the best provider first: %v", w.Header())
	}
}

func TestStrictNeverSwitchesModel(t *testing.T) {
	limited := &fake{id: "p1", model: "alpha", quality: 0.9, handler: status(429, nil)}
	other := &fake{id: "p2", model: "beta", quality: 0.8, handler: okJSON("from beta")}
	rt := setup(t, limited, other)

	// Normal routing falls over to beta.
	if w := do(t, rt, `{"model":"alpha","messages":[{"role":"user","content":"hi"}]}`); w.Header().Get("X-MangoMan-Model") != "beta" {
		t.Fatalf("explicit model should fall back: %d %s", w.Code, w.Header())
	}
	rt.Quota = quota.New() // forget the block
	w := do(t, rt, `{"model":"strict/alpha","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" || other.calls.Load() != 1 {
		t.Fatalf("strict must not use beta: %d %s calls=%d", w.Code, w.Body, other.calls.Load())
	}
	// Strict on the same model served by two providers still fails over between them.
	a1 := &fake{id: "a1", model: "alpha", quality: 0.9, handler: status(500, nil)}
	a2 := &fake{id: "a2", model: "alpha", quality: 0.7, handler: okJSON("alpha on a2")}
	b := &fake{id: "b", model: "beta", quality: 0.95, handler: okJSON("beta")}
	rt = setup(t, a1, a2, b)
	w = do(t, rt, `{"model":"strict/alpha","messages":[{"role":"user","content":"hi"}]}`)
	if w.Header().Get("X-MangoMan-Provider") != "a2" || b.calls.Load() != 0 {
		t.Fatalf("strict should try alpha elsewhere: %s", w.Header())
	}
}

func TestGroupOrderAndScope(t *testing.T) {
	a := &fake{id: "pa", model: "alpha", quality: 0.5, handler: status(429, nil)}
	b := &fake{id: "pb", model: "beta", quality: 0.6, handler: okJSON("beta")}
	c := &fake{id: "pc", model: "gamma", quality: 0.99, handler: okJSON("gamma")}
	rt := setup(t, a, b, c)
	rt.Cfg.SetGroup("Workflow", []string{"alpha", "pb/beta"})
	rt.Cfg.SetFavorites([]string{"gamma"}) // My list must not leak into a group
	w := do(t, rt, `{"model":"group/workflow","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != 200 || w.Header().Get("X-MangoMan-Model") != "beta" || a.calls.Load() != 1 || c.calls.Load() != 0 {
		t.Fatalf("group order: %d %s a=%d c=%d", w.Code, w.Header(), a.calls.Load(), c.calls.Load())
	}
	if w := do(t, rt, `{"model":"group/nope","messages":[{"role":"user","content":"hi"}]}`); w.Code != 404 || !strings.Contains(w.Body.String(), "no group called nope") {
		t.Fatalf("unknown group %d %s", w.Code, w.Body)
	}
	if w := do(t, rt, `{"model":"strict/typo-model","messages":[{"role":"user","content":"hi"}]}`); w.Code != 404 {
		t.Fatalf("unknown strict model %d %s", w.Code, w.Body)
	}
}

// fakeBrain answers fixed decisions and counts calls.
type fakeBrain struct {
	class  string
	nonAns bool
	conf   float64
	choose atomic.Int32
	yesno  atomic.Int32
}

func (f *fakeBrain) Choose(_ context.Context, kind, key, q string, opts []string) (string, float64, bool) {
	f.choose.Add(1)
	return f.class, f.conf, f.class != ""
}
func (f *fakeBrain) YesNo(_ context.Context, kind, key, q string) (bool, float64, bool) {
	f.yesno.Add(1)
	return f.nonAns, f.conf, true
}

func TestBrainClassifiesOnlyWhenRulesUnsure(t *testing.T) {
	a := &fake{id: "p1", model: "alpha", quality: 0.9, handler: okJSON("ok")}
	rt := setup(t, a)
	fb := &fakeBrain{class: "code", conf: 0.9}
	rt.Brain = fb
	w := do(t, rt, `{"model":"free/auto","messages":[{"role":"user","content":"tell me about mangoes"}]}`)
	if w.Header().Get("X-MangoMan-Class") != "code" || fb.choose.Load() != 1 {
		t.Fatalf("unsure request should ask the brain: class=%s calls=%d", w.Header().Get("X-MangoMan-Class"), fb.choose.Load())
	}
	// Sure by rules (tools / code hints / virtual model): no brain call.
	for _, body := range []string{
		`{"model":"free/auto","messages":[{"role":"user","content":"fix this python traceback"}]}`,
		`{"model":"free/writer","messages":[{"role":"user","content":"hello"}]}`,
		`{"model":"free/auto","tools":[{"type":"function","function":{"name":"f","parameters":{}}}],"messages":[{"role":"user","content":"hello"}]}`,
	} {
		do(t, rt, body)
	}
	if fb.choose.Load() != 1 {
		t.Fatalf("brain asked for confident requests: %d", fb.choose.Load())
	}
	// No confident answer: the rule result stands.
	rt.Brain = &fakeBrain{}
	if w := do(t, rt, `{"model":"free/auto","messages":[{"role":"user","content":"tell me about mangoes"}]}`); w.Header().Get("X-MangoMan-Class") != "writing" {
		t.Fatalf("fallback class %s", w.Header().Get("X-MangoMan-Class"))
	}
}

func TestBrainCatchesNonAnswer(t *testing.T) {
	refuse := okJSON("I'm sorry, but I can't help with that request.")
	good := okJSON("Mangoes are tropical stone fruits.")
	run := func(b Brain) (*httptest.ResponseRecorder, *fake, *fake) {
		r := &fake{id: "r1", model: "refuser", quality: 0.95, handler: refuse}
		g := &fake{id: "g1", model: "helper", quality: 0.5, handler: good}
		rt := setup(t, r, g)
		rt.Brain = b
		return do(t, rt, hello), r, g
	}
	w, _, g := run(&fakeBrain{nonAns: true, conf: 0.9})
	if w.Header().Get("X-MangoMan-Model") != "helper" || g.calls.Load() != 1 {
		t.Fatalf("non-answer should fail over: %s", w.Header())
	}
	w, _, g = run(&fakeBrain{nonAns: true, conf: 0.6})
	if w.Header().Get("X-MangoMan-Model") != "refuser" || g.calls.Load() != 0 {
		t.Fatalf("an unsure verdict must keep the answer: %s", w.Header())
	}
	w, _, _ = run(nil)
	if w.Header().Get("X-MangoMan-Model") != "refuser" {
		t.Fatal("without a brain nothing changes")
	}
	// A normal answer never reaches the brain.
	fb := &fakeBrain{nonAns: true, conf: 1}
	a := &fake{id: "p1", model: "alpha", quality: 0.9, handler: good}
	rt := setup(t, a)
	rt.Brain = fb
	do(t, rt, hello)
	if fb.yesno.Load() != 0 {
		t.Fatal("brain consulted for an ordinary answer")
	}
}

func TestInternalCallSkipsBrainAndMyList(t *testing.T) {
	fast := &fake{id: "f1", model: "fast-model", quality: 0.5, handler: okJSON(`{"answer":"code","confidence":0.9}`)}
	fav := &fake{id: "v1", model: "fav-model", quality: 0.4, handler: okJSON(`{"answer":"writing","confidence":0.9}`)}
	rt := setup(t, fast, fav)
	rt.Cfg.SetFavorites([]string{"fav-model"})
	fb := &fakeBrain{class: "code", conf: 0.9}
	rt.Brain = fb
	status, body, err := rt.InternalCall(context.Background(), []byte(`{"model":"free/fast","messages":[{"role":"user","content":"tell me about mangoes"}],"response_format":{"type":"json_object"}}`))
	if err != nil || status != 200 || !strings.Contains(string(body), `\"code\"`) {
		t.Fatalf("internal call %d %s %v", status, body, err)
	}
	if fb.choose.Load() != 0 || fav.calls.Load() != 0 {
		t.Fatalf("internal call used the brain (%d) or My list (%d)", fb.choose.Load(), fav.calls.Load())
	}
}

func TestHalfOpenProbeReleasedAfter429(t *testing.T) {
	var mode atomic.Int32 // 0: 503, 1: 429, 2: ok
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load() {
		case 0:
			status(503, nil)(w, r)
		case 1:
			status(429, map[string]string{"Retry-After": "1"})(w, r)
		default:
			okJSON("from a")(w, r)
		}
	}}
	rt := setup(t, a)
	now := time.Now()
	rt.Breakers.SetClock(func() time.Time { return now })
	rt.Quota.SetClock(func() time.Time { return now })
	for i := 0; i < 3; i++ {
		do(t, rt, hello)
	}
	now = now.Add(time.Hour) // half-open
	mode.Store(1)
	do(t, rt, hello) // the probe gets a 429
	now = now.Add(time.Hour)
	mode.Store(2)
	if w := do(t, rt, hello); w.Code != 200 {
		t.Fatalf("target stuck half-open: %d %s", w.Code, w.Body)
	}
}

func TestLocalContextTooSmallSaysHowToFix(t *testing.T) {
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: okJSON("x"), local: true}
	rt := setup(t, a)
	long := strings.Repeat("word ", 40000)
	body, _ := json.Marshal(map[string]any{"model": "free/auto", "messages": []map[string]string{{"role": "user", "content": long}}})
	w := do(t, rt, string(body))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "OLLAMA_CONTEXT_LENGTH=65536") {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
}

func limitsOr(l catalogue.Limits) catalogue.Limits {
	if l == (catalogue.Limits{}) {
		return catalogue.Limits{RPM: 100}
	}
	return l
}

func TestRequestBiggerThanMinuteCapSkipsModelAndSaysWhatToConnect(t *testing.T) {
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: okJSON("x"), limits: catalogue.Limits{RPM: 30, TPM: 8000}}
	b := &fake{id: "b", model: "m2", quality: 0.5, handler: okJSON("from b"), limits: catalogue.Limits{RPM: 30, TPM: 60000}}
	long := strings.Repeat("word ", 9000)
	body, _ := json.Marshal(map[string]any{"model": "free/auto", "messages": []map[string]string{{"role": "user", "content": long}}})
	rt := setup(t, a, b)
	if w := do(t, rt, string(body)); w.Code != 200 || a.calls.Load() != 0 || !strings.Contains(w.Body.String(), "from b") {
		t.Fatalf("got %d %s; small-cap model called %d times", w.Code, w.Body, a.calls.Load())
	}
	only := setup(t, &fake{id: "c", model: "m3", quality: 0.9, handler: okJSON("x"), limits: catalogue.Limits{RPM: 30, TPM: 8000}})
	if w := do(t, only, string(body)); w.Code != 400 || !strings.Contains(w.Body.String(), "Cerebras") {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
}

func TestWeakModelNeedsPermission(t *testing.T) {
	strong := &fake{id: "s", model: "big", quality: 0.85, handler: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, `{"error":{"message":"slow down"}}`, 429)
	}}
	weak := &fake{id: "w", model: "small", quality: 0.6, handler: okJSON("from small")}
	rt := setup(t, strong, weak)
	rt.Cfg.AllowWeaker = false
	body := `{"messages":[{"role":"user","content":"hi"}]}`
	w := do(t, rt, body)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "only_weaker_models") || weak.calls.Load() != 0 {
		t.Fatalf("should stop and ask, not use the weak model: %d %s (weak calls %d)", w.Code, w.Body, weak.calls.Load())
	}
	// Allowed for this request by header, then for an hour from the dashboard.
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("X-MangoMan-Allow-Weaker", "1")
	req, _ := core.ParseChat([]byte(body))
	rec := httptest.NewRecorder()
	rt.Handle(rec, r, req)
	if rec.Header().Get("X-MangoMan-Weaker") != "1" || rec.Header().Get("X-MangoMan-Provider") != "w" {
		t.Fatalf("header should allow the weak model: %v %s", rec.Header(), rec.Body)
	}
	rt.AllowWeakerFor(time.Hour)
	if w := do(t, rt, body); w.Header().Get("X-MangoMan-Provider") != "w" {
		t.Fatalf("allowed for an hour: %v %s", w.Header(), w.Body)
	}
	if o := rt.Outlook(); !o.StrongBusy || o.WeakerUntil.IsZero() || len(o.Next) == 0 || o.Last == nil || !o.Last.Weak {
		t.Fatalf("outlook: %+v", o)
	}
}

func TestChatStaysOnItsModel(t *testing.T) {
	a := &fake{id: "a", model: "m1", quality: 0.8, handler: okJSON("from a")}
	b := &fake{id: "b", model: "m2", quality: 0.8, handler: okJSON("from b")}
	rt := setup(t, a, b)
	chat := `{"messages":[{"role":"user","content":"build me a shop"}]}`
	first := do(t, rt, chat).Header().Get("X-MangoMan-Provider")
	// Make the other model look better; the ongoing chat must not move.
	other := map[string]*fake{"a": b, "b": a}[first]
	c := Candidate{Model: catalogue.Model{Provider: other.id, Canonical: other.model}, Provider: catalogue.Provider{ID: other.id}}
	for i := 0; i < 3; i++ {
		rt.Health.Observe(c, "ok", true, 100*time.Millisecond)
	}
	more := `{"messages":[{"role":"user","content":"build me a shop"},{"role":"assistant","content":"ok"},{"role":"user","content":"add a cart"}]}`
	if got := do(t, rt, more).Header().Get("X-MangoMan-Provider"); got != first {
		t.Fatalf("chat moved from %s to %s", first, got)
	}
}

func TestProviderPriorityAndRoom(t *testing.T) {
	z := &fake{id: "z", model: "m1", quality: 0.8, prio: 2, handler: okJSON("z")}
	c := &fake{id: "c", model: "m2", quality: 0.78, prio: 1, handler: okJSON("c")}
	rt := setup(t, z, c)
	if got := do(t, rt, `{"messages":[{"role":"user","content":"a"}]}`).Header().Get("X-MangoMan-Provider"); got != "c" {
		t.Fatalf("equal-skill models: priority 1 first, got %s", got)
	}
	// A request that would fill most of c's minute allowance goes to z,
	// which has room for the chat to grow.
	c.limits = catalogue.Limits{TPM: 12000}
	rt = setup(t, z, c)
	big := `{"max_tokens":7000,"messages":[{"role":"user","content":"b"}]}`
	if got := do(t, rt, big).Header().Get("X-MangoMan-Provider"); got != "z" {
		t.Fatalf("tight model should go after a roomy one, got %s", got)
	}
}

// addTeamKey adds a teammate's key for provider id, as the dashboard does.
func addTeamKey(t *testing.T, rt *Router, id, name string) {
	t.Helper()
	if err := rt.Keys.Store().Set(keys.Name(id, name), "key-"+id+"-"+name); err != nil {
		t.Fatal(err)
	}
	rt.Cfg.SetTeamKey(id, name, true)
}

// keyRecorder answers every request and records which key was used.
func keyRecorder(seen *[]string, mu *sync.Mutex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*seen = append(*seen, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		mu.Unlock()
		okJSON("hi")(w, r)
	}
}

func TestTeamKeysTakeTurns(t *testing.T) {
	var seen []string
	var mu sync.Mutex
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: keyRecorder(&seen, &mu)}
	rt := setup(t, a)
	addTeamKey(t, rt, "a", "ravi")
	addTeamKey(t, rt, "a", "asha")
	for range 6 {
		if w := do(t, rt, hello); w.Code != 200 {
			t.Fatalf("got %d %s", w.Code, w.Body)
		}
	}
	count := map[string]int{}
	for _, k := range seen {
		count[k]++
	}
	if len(seen) != 6 || count["key-a"] != 2 || count["key-a-ravi"] != 2 || count["key-a-asha"] != 2 {
		t.Fatalf("keys should take turns, two requests each: %v", seen)
	}
}

func TestTeamKeyTakesOverTheSameModel(t *testing.T) {
	ownFull := func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer key-a" {
			status(429, map[string]string{"Retry-After": "60"})(w, r)
			return
		}
		okJSON("from ravi")(w, r)
	}
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: ownFull}
	b := &fake{id: "b", model: "m2", quality: 0.5, handler: okJSON("other model")}
	rt := setup(t, a, b)
	addTeamKey(t, rt, "a", "ravi")
	for range 3 {
		w := do(t, rt, hello)
		if w.Header().Get("X-MangoMan-Model") != "m1" || w.Header().Get("X-MangoMan-Team-Key") != "ravi" {
			t.Fatalf("a full key should hand over to the teammate's key on the same model: %v %s", w.Header(), w.Body)
		}
	}
}

func TestRejectedTeamKeyLeavesTheOthers(t *testing.T) {
	ravi := func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer key-a-ravi" {
			status(401, nil)(w, r)
			return
		}
		okJSON("ok")(w, r)
	}
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: ravi}
	rt := setup(t, a)
	addTeamKey(t, rt, "a", "ravi")
	for range 3 {
		if w := do(t, rt, hello); w.Code != 200 || w.Header().Get("X-MangoMan-Provider") != "a" {
			t.Fatalf("one bad team key must not take the provider down: %d %v", w.Code, w.Header())
		}
	}
	if !rt.Keys.Rejected("a#ravi") || rt.Keys.Rejected("a") {
		t.Fatal("only Ravi's key should be marked rejected")
	}
}

func TestTeamKeysSwitchedOffForAProvider(t *testing.T) {
	var seen []string
	var mu sync.Mutex
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: keyRecorder(&seen, &mu), noTeam: true}
	rt := setup(t, a)
	addTeamKey(t, rt, "a", "ravi")
	for range 4 {
		do(t, rt, hello)
	}
	for _, k := range seen {
		if k != "key-a" {
			t.Fatalf("team keys are off for this provider, yet %s was used", k)
		}
	}
}

// A request carrying a key goes first to a provider that does not train on
// data, for the same model; without a key the usual order holds.
func TestSecretPrefersPrivateProvider(t *testing.T) {
	a := &fake{id: "a", model: "m1", quality: 0.9, prio: 1, trains: "yes", handler: okJSON("from a")}
	b := &fake{id: "b", model: "m1", quality: 0.9, prio: 2, handler: okJSON("from b")}
	rt := setup(t, a, b)
	if w := do(t, rt, hello); w.Header().Get("X-MangoMan-Provider") != "a" {
		t.Fatalf("without a secret the usual order holds: %v", w.Header())
	}
	key := `{"model":"free/auto","messages":[{"role":"user","content":"debug this: GROQ_API_KEY=gsk_` + strings.Repeat("a1B2", 10) + `"}]}`
	w := do(t, rt, key)
	if w.Header().Get("X-MangoMan-Provider") != "b" || w.Header().Get("X-MangoMan-Secret") != "1" {
		t.Fatalf("a request with a key should go to the provider that does not train: %v", w.Header())
	}
	if !strings.Contains(w.Body.String(), "from b") {
		t.Fatal("the request must still be answered")
	}
}

// Your own key being rejected must not stop a teammate's key for the same
// provider from answering.
func TestOwnKeyRejectedTeamKeyStillWorks(t *testing.T) {
	ownBad := func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer key-a" {
			status(401, nil)(w, r)
			return
		}
		okJSON("from ravi")(w, r)
	}
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: ownBad}
	rt := setup(t, a)
	addTeamKey(t, rt, "a", "ravi")
	for range 2 {
		w := do(t, rt, `{"model":"strict/m1","messages":[{"role":"user","content":"hi"}]}`)
		if w.Code != 200 || w.Header().Get("X-MangoMan-Team-Key") != "ravi" {
			t.Fatalf("the teammate's key should answer: %d %v %s", w.Code, w.Header(), w.Body)
		}
	}
}

// A model silent past the limit before its first word is not asked again:
// waiting three times over only delays the error.
func TestSilentOnlyModelNotRetried(t *testing.T) {
	stall := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: stall}
	rt := setup(t, a)
	rt.StreamIdle = 200 * time.Millisecond
	do(t, rt, helloStream)
	if n := a.calls.Load(); n != 1 {
		t.Fatalf("a silent model was asked %d times", n)
	}
}

// Measured on real free models: a coding model ended some turns with only
// its reasoning, no words and no tool call, and the agent stopped the task.
func TestReasoningOnlyStreamIsNoAnswer(t *testing.T) {
	think := `{"choices":[{"index":0,"delta":{"reasoning_content":"let me think"},"finish_reason":null}]}`
	stop := `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: sse(think, stop, "[DONE]")}
	rt := setup(t, a)
	w := do(t, rt, helloStream)
	// The client gets an error, not a clean end, so an agent run continues.
	if body := w.Body.String(); strings.Contains(body, "[DONE]") || !strings.Contains(body, "upstream_stream_error") {
		t.Fatalf("reasoning-only answer ended cleanly:\n%s", body)
	}
	for _, s := range rt.Health.Snapshot() {
		if s.Target == "a/m1" && (s.LastOut != "no_answer" || s.OK != 0) {
			t.Fatalf("reasoning alone counted as an answer: %+v", s)
		}
	}
	// Reasoning followed by words is a normal answer.
	b := &fake{id: "b", model: "m2", quality: 0.9, handler: sse(think, chunk("done"), stop, "[DONE]")}
	rt = setup(t, b)
	do(t, rt, helloStream)
	if s := rt.Health.Snapshot(); len(s) != 1 || s[0].LastOut != "ok" {
		t.Fatalf("got %+v", s)
	}
}

// Measured on a free model with a broken chat template: noise with its own
// control tokens in it, passed on as a normal answer.
func TestLeakedControlTokensAreGarbled(t *testing.T) {
	stop := `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: sse(chunk("考生 skill盖 sudo"), chunk("<|close|> Reiframe"), stop, "[DONE]")}
	b := &fake{id: "b", model: "m2", quality: 0.3, handler: sse(chunk("from b"), "[DONE]")}
	rt := setup(t, a, b)
	w := do(t, rt, helloStream)
	// Stopped mid-answer: the client gets an error, never the leaked token.
	if body := w.Body.String(); strings.Contains(body, "<|close|>") || !strings.Contains(body, "upstream_stream_error") {
		t.Fatalf("garbled answer not stopped:\n%s", body)
	}
	// The garbling model is tried last from now on.
	if w := do(t, rt, helloStream); w.Header().Get("X-MangoMan-Provider") != "b" {
		t.Fatalf("garbling model still first: %v", w.Header())
	}
	// Garbage in the very first words: the next model answers this request.
	c := &fake{id: "c", model: "m3", quality: 0.9, handler: sse(chunk("<|sep|> noise"), "[DONE]")}
	d := &fake{id: "d", model: "m4", quality: 0.3, handler: sse(chunk("from d"), "[DONE]")}
	rt = setup(t, c, d)
	if w := do(t, rt, helloStream); !strings.Contains(w.Body.String(), "from d") {
		t.Fatalf("no fail-over:\n%s", w.Body)
	}
}

// Measured on NVIDIA: about one request in five came back overloaded, too
// few in a row to trip the breaker, so every request tried the busy model
// first. A model that says it is overloaded is tried last for two minutes,
// but stays in the list.
func TestOverloadedModelTriedLast(t *testing.T) {
	var busy atomic.Bool
	busy.Store(true)
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: func(w http.ResponseWriter, r *http.Request) {
		if busy.Load() {
			sse(`{"error":{"message":"Service temporarily overloaded"}}`)(w, r)
			return
		}
		sse(chunk("from a"), "[DONE]")(w, r)
	}}
	b := &fake{id: "b", model: "m2", quality: 0.3, handler: sse(chunk("from b"), "[DONE]")}
	rt := setup(t, a, b)
	do(t, rt, helloStream)
	busy.Store(false)
	before := a.calls.Load()
	if w := do(t, rt, helloStream); w.Header().Get("X-MangoMan-Provider") != "b" || a.calls.Load() != before {
		t.Fatalf("the busy model was asked first: %v", w.Header())
	}
	// Alone in the list, it is still asked.
	var n atomic.Int32
	only := &fake{id: "c", model: "m3", quality: 0.9, handler: func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			sse(`{"error":{"message":"overloaded"}}`)(w, r)
			return
		}
		sse(chunk("from c"), "[DONE]")(w, r)
	}}
	rt = setup(t, only)
	do(t, rt, helloStream)
	before = only.calls.Load()
	do(t, rt, helloStream)
	if only.calls.Load() == before {
		t.Fatal("the only model was skipped")
	}
}

func TestBusyCountsRequests(t *testing.T) {
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: okJSON("hi")}
	rt := setup(t, a)
	do(t, rt, hello)
	if n, idle := rt.Busy(); n != 0 || idle > time.Second {
		t.Fatalf("in flight %d, idle %v", n, idle)
	}
}

func TestTryingSaysWhichModelAndWhy(t *testing.T) {
	var rt *Router
	var seen Attempt
	var inFlight bool
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: sse(`{"error":{"message":"Service temporarily overloaded"}}`)}
	b := &fake{id: "b", model: "m2", quality: 0.3, handler: func(w http.ResponseWriter, r *http.Request) {
		seen, inFlight = rt.Trying()
		sse(chunk("from b"), "[DONE]")(w, r)
	}}
	rt = setup(t, a, b)
	do(t, rt, helloStream)
	if !inFlight || seen.Model != "m2" || seen.Number != 2 || seen.After != "m1" || seen.Why != "busy" || seen.Answering {
		t.Fatalf("got %+v (in flight %v)", seen, inFlight)
	}
	if _, ok := rt.Trying(); ok {
		t.Fatal("nothing should be in flight after the answer")
	}
}

func TestTryingPrefersTheRequestStillWaiting(t *testing.T) {
	rt := setup(t)
	title, main := &core.Request{}, &core.Request{}
	now := time.Now()
	rt.active.set(main, func(a *Attempt) {
		*a = Attempt{Model: "big", Since: now.Add(-20 * time.Second), After: "other", Why: "busy"}
	})
	rt.active.set(title, func(a *Attempt) { *a = Attempt{Model: "small", Since: now, Answering: true} })
	rt.active.inFlight.Store(2)
	if a, ok := rt.Trying(); !ok || a.Model != "big" || a.After != "other" {
		t.Fatalf("got %+v", a)
	}
	rt.active.done(main)
	if a, _ := rt.Trying(); a.Model != "small" {
		t.Fatalf("got %+v", a)
	}
}

// Measured once: NVIDIA's model was cooling off after errors and OpenRouter
// was used up until its daily reset, so the client was told to wait hours
// and a coding task sat for 17 minutes. The wait is now the cool-off.
func TestRetryAfterCountsModelsCoolingOff(t *testing.T) {
	a := &fake{id: "a", model: "m1", quality: 0.9, handler: status(500, nil)}
	b := &fake{id: "b", model: "m2", quality: 0.3, handler: okJSON("from b")}
	rt := setup(t, a, b)
	for i := 0; i < 3; i++ {
		rt.Breakers.Failure("a/m1")
	}
	rt.Quota.Block(quota.Key{Provider: "b", Account: keys.Own, Model: "m2"}, time.Now().Add(5*time.Hour))
	w := do(t, rt, hello)
	secs, _ := strconv.Atoi(w.Header().Get("Retry-After"))
	if w.Code != http.StatusTooManyRequests || secs < 1 || secs > 5*60 {
		t.Fatalf("%d, Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}
	if _, idle := rt.Busy(); idle != 0 {
		t.Fatalf("a client told to wait counts as a stall: idle %v", idle)
	}
}
