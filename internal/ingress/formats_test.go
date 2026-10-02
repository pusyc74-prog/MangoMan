package ingress

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/catalogue"
	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/keys"
	"github.com/pusyc74-prog/mangoman/internal/router"
)

// upstream is a fake OpenAI-compatible provider that records what it got.
type upstream struct {
	id      string
	quality float64
	handler http.HandlerFunc
	mu      sync.Mutex
	bodies  []map[string]any
}

func (u *upstream) last() map[string]any {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.bodies) == 0 {
		return nil
	}
	return u.bodies[len(u.bodies)-1]
}

func formatServer(t *testing.T, ups ...*upstream) http.Handler {
	t.Helper()
	cat := catalogue.Catalogue{Version: "test"}
	store := mem{}
	var client *http.Client
	for _, u := range ups {
		u := u
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			data, _ := io.ReadAll(r.Body)
			var m map[string]any
			_ = json.Unmarshal(data, &m)
			u.mu.Lock()
			u.bodies = append(u.bodies, m)
			u.mu.Unlock()
			u.handler(w, r)
		}))
		t.Cleanup(srv.Close)
		client = srv.Client()
		cat.Providers = append(cat.Providers, catalogue.Provider{ID: u.id, Name: u.id, BaseURL: srv.URL, Kind: "openai",
			NeedsKey: true, Speed: 0.5, Policy: catalogue.DataPolicy{Retention: "none", TrainsOnData: "no", Jurisdiction: "US"},
			Quirks: catalogue.Quirks{StreamUsage: true}})
		cat.Models = append(cat.Models, catalogue.Model{Canonical: "m-" + u.id, Provider: u.id, Upstream: "up-" + u.id,
			Free: true, Context: 32000, Caps: []string{"tools", "json", "streaming"}, Limits: catalogue.Limits{RPM: 100},
			Quality: map[string]float64{"default": u.quality}})
		store[u.id] = "k"
	}
	data, _ := json.Marshal(&cat)
	parsed, err := catalogue.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Port: 4141, Token: "tok", MaxAttempts: 6}
	rt := router.New(parsed, keys.NewResolver(store, nil), cfg)
	rt.Client.HTTP = client
	rt.StreamIdle = 2 * time.Second
	lastServer = &Server{Router: rt, Cfg: cfg, Version: "t", Started: time.Now()}
	return lastServer.Handler()
}

// lastServer is the server formatServer built most recently.
var lastServer *Server

func jsonReply(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}
}

func sseReply(events ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		for _, e := range events {
			fmt.Fprintf(w, "data: %s\n\n", e)
			f.Flush()
		}
	}
}

func errReply(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		fmt.Fprintf(w, `{"error":{"message":"upstream said %d"}}`, code)
	}
}

const textReply = `{"id":"c1","choices":[{"index":0,"message":{"role":"assistant","content":"Hello from the free model"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17}}`
const toolReply = `{"id":"c2","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Pune\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":9,"total_tokens":29}}`

var anthHdr = map[string]string{"x-api-key": "tok", "anthropic-version": "2023-06-01", "Content-Type": "application/json"}
var bearer = map[string]string{"Authorization": "Bearer tok", "Content-Type": "application/json"}

type sseEvent struct {
	Name string
	Data map[string]any
}

func readSSE(t *testing.T, body string) []sseEvent {
	t.Helper()
	var out []sseEvent
	var cur sseEvent
	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			cur.Name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &cur.Data); err != nil {
				t.Fatalf("bad event data %q: %v", line, err)
			}
		case line == "":
			if cur.Data != nil {
				out = append(out, cur)
			}
			cur = sseEvent{}
		}
	}
	return out
}

func names(evs []sseEvent) []string {
	var n []string
	for _, e := range evs {
		n = append(n, e.Name)
	}
	return n
}

// ---------- Anthropic Messages ----------

func TestMessagesText(t *testing.T) {
	up := &upstream{id: "a", quality: 0.9, handler: jsonReply(textReply)}
	h := formatServer(t, up)
	w := call(h, "POST", "/v1/messages", "127.0.0.1:4141", anthHdr,
		`{"model":"claude-sonnet-4-5","max_tokens":256,"system":[{"type":"text","text":"Be brief."}],"messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var m struct {
		Type, Role, Model string
		StopReason        string `json:"stop_reason"`
		Content           []struct{ Type, Text string }
		Usage             struct {
			In  int `json:"input_tokens"`
			Out int `json:"output_tokens"`
		}
	}
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	if m.Type != "message" || m.Role != "assistant" || m.Model != "claude-sonnet-4-5" || m.StopReason != "end_turn" ||
		len(m.Content) != 1 || m.Content[0].Text != "Hello from the free model" || m.Usage.In != 12 || m.Usage.Out != 5 {
		t.Fatalf("message %+v", m)
	}
	if w.Header().Get("X-MangoMan-Provider") != "a" {
		t.Fatal("router headers should pass through")
	}
	got := up.last()
	msgs := got["messages"].([]any)
	if sys := msgs[0].(map[string]any); sys["role"] != "system" || sys["content"] != "Be brief." {
		t.Fatalf("system not translated: %v", msgs)
	}
	if got["max_tokens"].(float64) != 256 || got["model"] != "up-a" {
		t.Fatalf("upstream body %v", got)
	}
}

func TestMessagesToolRoundTrip(t *testing.T) {
	up := &upstream{id: "a", quality: 0.9, handler: jsonReply(toolReply)}
	h := formatServer(t, up)
	body := `{"model":"claude-opus-4","max_tokens":1024,
	 "tools":[{"name":"get_weather","description":"Weather","input_schema":{"type":"object","properties":{"city":{"type":"string"}}}},
	          {"type":"web_search_20250305","name":"web_search"}],
	 "messages":[
	  {"role":"user","content":"weather in Pune and Delhi?"},
	  {"role":"assistant","content":[{"type":"thinking","thinking":"hm"},{"type":"text","text":"Checking."},{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"Delhi"}}]},
	  {"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"31C"}]},{"type":"text","text":"now Pune"}]}
	 ]}`
	w := call(h, "POST", "/v1/messages", "127.0.0.1:4141", anthHdr, body)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var m struct {
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type, ID, Name string
			Input          map[string]any
		}
	}
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	if m.StopReason != "tool_use" || len(m.Content) != 1 || m.Content[0].Type != "tool_use" ||
		m.Content[0].ID != "call_1" || m.Content[0].Input["city"] != "Pune" {
		t.Fatalf("tool use %s", w.Body)
	}
	got := up.last()
	tools := got["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["function"].(map[string]any)["name"] != "get_weather" {
		t.Fatalf("tools %v", tools)
	}
	msgs := got["messages"].([]any)
	roles := []string{}
	for _, x := range msgs {
		roles = append(roles, x.(map[string]any)["role"].(string))
	}
	if strings.Join(roles, ",") != "user,assistant,tool,user" {
		t.Fatalf("roles %v", roles)
	}
	asst := msgs[1].(map[string]any)
	tc := asst["tool_calls"].([]any)[0].(map[string]any)
	if asst["content"] != "Checking." || tc["id"] != "toolu_1" || tc["function"].(map[string]any)["arguments"] != `{"city":"Delhi"}` {
		t.Fatalf("assistant %v", asst)
	}
	if tool := msgs[2].(map[string]any); tool["tool_call_id"] != "toolu_1" || tool["content"] != "31C" {
		t.Fatalf("tool %v", tool)
	}
}

const usageChunk = `{"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`

func TestMessagesStreamTextThenTool(t *testing.T) {
	up := &upstream{id: "a", quality: 0.9, handler: sseReply(
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"Let me check"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"."}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_9","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Pune\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		usageChunk, "[DONE]")}
	h := formatServer(t, up)
	w := call(h, "POST", "/v1/messages", "127.0.0.1:4141", anthHdr,
		`{"model":"claude-sonnet-4-5","max_tokens":256,"stream":true,"tools":[{"name":"get_weather","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"weather?"}]}`)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("%d %s", w.Code, w.Header())
	}
	evs := readSSE(t, w.Body.String())
	want := "message_start,content_block_start,content_block_delta,content_block_delta,content_block_stop," +
		"content_block_start,content_block_delta,content_block_delta,content_block_stop,message_delta,message_stop"
	if got := strings.Join(names(evs), ","); got != want {
		t.Fatalf("events\n got %s\nwant %s", got, want)
	}
	var text, args string
	for _, e := range evs {
		if e.Name == "content_block_delta" {
			d := e.Data["delta"].(map[string]any)
			text += fmt.Sprint(d["text"])
			if d["type"] == "input_json_delta" {
				args += d["partial_json"].(string)
			}
		}
	}
	if !strings.HasPrefix(text, "Let me check.") || args != `{"city":"Pune"}` {
		t.Fatalf("text %q args %q", text, args)
	}
	tool := evs[5].Data["content_block"].(map[string]any)
	if tool["type"] != "tool_use" || tool["id"] != "call_9" || evs[5].Data["index"].(float64) != 1 {
		t.Fatalf("tool block %v", evs[5].Data)
	}
	md := evs[9].Data
	if md["delta"].(map[string]any)["stop_reason"] != "tool_use" || md["usage"].(map[string]any)["output_tokens"].(float64) != 7 {
		t.Fatalf("message_delta %v", md)
	}
	if !up.last()["stream_options"].(map[string]any)["include_usage"].(bool) {
		t.Fatal("usage should be requested for providers that support it")
	}
}

func TestMessagesFailoverAndExhausted(t *testing.T) {
	bad := &upstream{id: "bad", quality: 0.95, handler: errReply(429)}
	good := &upstream{id: "good", quality: 0.5, handler: jsonReply(textReply)}
	h := formatServer(t, bad, good)
	w := call(h, "POST", "/v1/messages", "127.0.0.1:4141", anthHdr,
		`{"model":"claude-haiku-4-5","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != 200 || w.Header().Get("X-MangoMan-Provider") != "good" || !strings.Contains(w.Body.String(), `"type":"message"`) {
		t.Fatalf("failover %d %s %s", w.Code, w.Header(), w.Body)
	}

	only := &upstream{id: "only", quality: 0.9, handler: errReply(429)}
	h = formatServer(t, only)
	w = call(h, "POST", "/v1/messages", "127.0.0.1:4141", anthHdr,
		`{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`)
	var e struct {
		Type  string
		Error struct{ Type, Message string }
	}
	_ = json.Unmarshal(w.Body.Bytes(), &e)
	if w.Code != 429 || e.Type != "error" || e.Error.Type != "rate_limit_error" || w.Header().Get("Retry-After") == "" {
		t.Fatalf("exhausted %d %s", w.Code, w.Body)
	}
}

func TestMessagesStreamFailsOverBeforeOutput(t *testing.T) {
	empty := &upstream{id: "empty", quality: 0.95, handler: sseReply(`{"choices":[{"index":0,"delta":{"role":"assistant"}}]}`, "[DONE]")}
	good := &upstream{id: "good", quality: 0.5, handler: sseReply(
		`{"choices":[{"index":0,"delta":{"content":"ok"}}]}`, `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`, "[DONE]")}
	h := formatServer(t, empty, good)
	w := call(h, "POST", "/v1/messages", "127.0.0.1:4141", anthHdr,
		`{"model":"claude-sonnet-4-5","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	evs := readSSE(t, w.Body.String())
	if w.Header().Get("X-MangoMan-Provider") != "good" || len(evs) == 0 || evs[0].Name != "message_start" ||
		evs[len(evs)-1].Name != "message_stop" {
		t.Fatalf("stream failover: %s %v", w.Header(), names(evs))
	}
	if n := strings.Count(w.Body.String(), "message_start"); n != 2 { // event name + type field, once
		t.Fatalf("message_start sent %d times", n/2)
	}
}

func TestMessagesBadRequestAndCountTokens(t *testing.T) {
	h := formatServer(t, &upstream{id: "a", quality: 0.9, handler: jsonReply(textReply)})
	w := call(h, "POST", "/v1/messages", "127.0.0.1:4141", anthHdr, `{"model":"x","messages":[]}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"invalid_request_error"`) {
		t.Fatalf("bad request %d %s", w.Code, w.Body)
	}
	w = call(h, "POST", "/v1/messages/count_tokens", "127.0.0.1:4141", anthHdr,
		`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"`+strings.Repeat("word ", 400)+`"}]}`)
	var c struct {
		N int `json:"input_tokens"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &c)
	if w.Code != 200 || c.N < 400 || c.N > 700 {
		t.Fatalf("count_tokens %d %s", w.Code, w.Body)
	}
	if w := call(h, "POST", "/v1/messages", "127.0.0.1:4141", map[string]string{"x-api-key": "nope"}, `{}`); w.Code != 401 {
		t.Fatalf("messages must need the token: %d", w.Code)
	}
}

// ---------- OpenAI Responses ----------

func TestResponsesTextAndTools(t *testing.T) {
	up := &upstream{id: "a", quality: 0.9, handler: jsonReply(textReply)}
	h := formatServer(t, up)
	w := call(h, "POST", "/v1/responses", "127.0.0.1:4141", bearer,
		`{"model":"free/auto","instructions":"Be brief.","input":"hi","max_output_tokens":100}`)
	var r struct {
		Object, Status, Model string
		Output                []struct {
			Type, Role string
			Content    []struct{ Type, Text string }
		}
		Usage struct {
			In  int `json:"input_tokens"`
			Out int `json:"output_tokens"`
		}
	}
	_ = json.Unmarshal(w.Body.Bytes(), &r)
	if w.Code != 200 || r.Object != "response" || r.Status != "completed" || len(r.Output) != 1 ||
		r.Output[0].Content[0].Type != "output_text" || r.Output[0].Content[0].Text != "Hello from the free model" || r.Usage.Out != 5 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	got := up.last()
	if got["max_tokens"].(float64) != 100 || got["messages"].([]any)[0].(map[string]any)["content"] != "Be brief." {
		t.Fatalf("upstream %v", got)
	}

	// A Codex-style turn: history with a function call and its output, plus
	// a freeform custom tool.
	up.handler = jsonReply(`{"choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_p","type":"function","function":{"name":"apply_patch","arguments":"{\"input\":\"*** Begin Patch\"}"}}]},"finish_reason":"tool_calls"}]}`)
	w = call(h, "POST", "/v1/responses", "127.0.0.1:4141", bearer, `{"model":"gpt-5-codex","stream":false,
	 "tools":[{"type":"function","name":"shell","parameters":{"type":"object","properties":{"command":{"type":"array"}}}},
	          {"type":"custom","name":"apply_patch","description":"Edit files"},{"type":"web_search"}],
	 "input":[
	  {"type":"message","role":"developer","content":[{"type":"input_text","text":"You are Codex."}]},
	  {"type":"message","role":"user","content":[{"type":"input_text","text":"list files"}]},
	  {"type":"reasoning","summary":[]},
	  {"type":"function_call","call_id":"c1","name":"shell","arguments":"{\"command\":[\"ls\"]}"},
	  {"type":"function_call","call_id":"c2","name":"shell","arguments":"{\"command\":[\"pwd\"]}"},
	  {"type":"function_call_output","call_id":"c1","output":"a.go"},
	  {"type":"function_call_output","call_id":"c2","output":"/src"},
	  {"type":"message","role":"user","content":"now patch it"}]}`)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var r2 struct {
		Output []map[string]any
	}
	_ = json.Unmarshal(w.Body.Bytes(), &r2)
	if len(r2.Output) != 1 || r2.Output[0]["type"] != "custom_tool_call" || r2.Output[0]["input"] != "*** Begin Patch" || r2.Output[0]["call_id"] != "call_p" {
		t.Fatalf("custom tool call %s", w.Body)
	}
	got = up.last()
	if got["model"] != "up-a" {
		t.Fatalf("gpt-5-codex should route like free/coder, upstream got %v", got["model"])
	}
	msgs := got["messages"].([]any)
	roles := []string{}
	for _, x := range msgs {
		roles = append(roles, x.(map[string]any)["role"].(string))
	}
	if strings.Join(roles, ",") != "system,user,assistant,tool,tool,user" {
		t.Fatalf("roles %v", roles)
	}
	if calls := msgs[2].(map[string]any)["tool_calls"].([]any); len(calls) != 2 {
		t.Fatalf("parallel calls should be one assistant turn: %v", msgs[2])
	}
	if n := len(got["tools"].([]any)); n != 2 {
		t.Fatalf("hosted tools should be dropped, got %d tools", n)
	}
}

func TestResponsesStream(t *testing.T) {
	up := &upstream{id: "a", quality: 0.9, handler: sseReply(
		`{"choices":[{"index":0,"delta":{"content":"Running "}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"ls"}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_s","type":"function","function":{"name":"shell","arguments":"{\"command\":"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"[\"ls\"]}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`, usageChunk, "[DONE]")}
	h := formatServer(t, up)
	w := call(h, "POST", "/v1/responses", "127.0.0.1:4141", bearer,
		`{"model":"free/coder","stream":true,"tools":[{"type":"function","name":"shell","parameters":{"type":"object"}}],"input":"list"}`)
	evs := readSSE(t, w.Body.String())
	want := "response.created,response.in_progress,response.output_item.added,response.content_part.added," +
		"response.output_text.delta,response.output_text.delta,response.output_text.done,response.content_part.done,response.output_item.done," +
		"response.output_item.added,response.function_call_arguments.delta,response.function_call_arguments.delta," +
		"response.function_call_arguments.done,response.output_item.done,response.completed"
	if got := strings.Join(names(evs), ","); got != want {
		t.Fatalf("events\n got %s\nwant %s", got, want)
	}
	for i, e := range evs {
		if e.Data["type"] != e.Name || int(e.Data["sequence_number"].(float64)) != i {
			t.Fatalf("event %d type/sequence mismatch: %v", i, e.Data)
		}
	}
	final := evs[len(evs)-1].Data["response"].(map[string]any)
	out := final["output"].([]any)
	call := out[1].(map[string]any)
	if final["status"] != "completed" || len(out) != 2 || call["arguments"] != `{"command":["ls"]}` || call["call_id"] != "call_s" ||
		out[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"] != "Running ls" ||
		final["usage"].(map[string]any)["output_tokens"].(float64) != 7 {
		t.Fatalf("final response %v", final)
	}
}

func TestResponsesErrors(t *testing.T) {
	h := formatServer(t, &upstream{id: "only", quality: 0.9, handler: errReply(429)})
	w := call(h, "POST", "/v1/responses", "127.0.0.1:4141", bearer, `{"model":"free/auto","input":"hi","previous_response_id":"resp_1"}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "previous_response_id") {
		t.Fatalf("previous_response_id %d %s", w.Code, w.Body)
	}
	w = call(h, "POST", "/v1/responses", "127.0.0.1:4141", bearer, `{"model":"free/auto","input":"hi"}`)
	if w.Code != 429 || !strings.Contains(w.Body.String(), "free_capacity_exhausted") {
		t.Fatalf("exhausted %d %s", w.Code, w.Body)
	}
}

func TestBrainAPI(t *testing.T) {
	up := &upstream{id: "a", quality: 0.9, handler: jsonReply(`{"choices":[{"index":0,"message":{"role":"assistant","content":"{\"answer\":\"code\",\"confidence\":0.88}"},"finish_reason":"stop"}]}`)}
	h0 := formatServer(t, up)
	_ = h0
	// Rebuild with a brain attached.
	srv := lastServer
	br := BrainFromConfig(srv.Cfg, srv.Router.InternalCall)
	srv.Router.Brain, srv.Brain = br, br
	srv.SaveConfig = func(*config.Config) error { return nil }
	h := srv.Handler()

	w := call(h, "POST", "/mangoman/brain/test", "127.0.0.1:4141", bearer, `{"question":"which?","options":["code","writing"]}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"answer": "code"`) || !strings.Contains(w.Body.String(), `"decided": true`) {
		t.Fatalf("test %d %s", w.Code, w.Body)
	}
	if up.last()["response_format"] == nil {
		t.Fatal("brain should ask for JSON")
	}
	w = call(h, "PUT", "/mangoman/brain", "127.0.0.1:4141", bearer, `{"enabled":false}`)
	if w.Code != 200 || !srv.Cfg.GetBrain().Off || br.Enabled() {
		t.Fatalf("off %d %s", w.Code, w.Body)
	}
	if w := call(h, "PUT", "/mangoman/brain", "127.0.0.1:4141", bearer, `{"model":"group/missing"}`); w.Code != 422 {
		t.Fatalf("bad engine accepted: %d", w.Code)
	}
	w = call(h, "PUT", "/mangoman/brain", "127.0.0.1:4141", bearer, `{"enabled":true,"model":"strict/m-a"}`)
	if w.Code != 200 || srv.Cfg.GetBrain().Model != "strict/m-a" || !strings.Contains(w.Body.String(), `"model": "strict/m-a"`) {
		t.Fatalf("set model %d %s", w.Code, w.Body)
	}
	w = call(h, "GET", "/mangoman/overview", "127.0.0.1:4141", bearer, "")
	if !strings.Contains(w.Body.String(), `"brain"`) {
		t.Fatal("overview should include brain stats")
	}
}
