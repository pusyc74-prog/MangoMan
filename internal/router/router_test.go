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
	quirks  catalogue.Quirks
	account catalogue.Limits
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
			Quirks: f.quirks, AccountLimits: f.account,
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
