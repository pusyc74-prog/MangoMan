package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/pusyc74-prog/mangoman/internal/guardian"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/catalogue"
	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/keys"
	"github.com/pusyc74-prog/mangoman/internal/router"
	"github.com/pusyc74-prog/mangoman/internal/store"
)

func dashServer(t *testing.T, st mem) (http.Handler, *Server) {
	t.Helper()
	cat, err := catalogue.Seed()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Port: 4141, Token: "tok"}
	rt := router.New(cat, keys.NewResolver(st, map[string]string{"nvidia": "TEST_NVIDIA_KEY"}), cfg)
	logPath := filepath.Join(t.TempDir(), "usage.jsonl")
	l, _ := store.OpenLog(logPath)
	l.Add(store.Event{Time: time.Now(), RequestID: "r1", Provider: "groq", Model: "gpt-oss-120b", Outcome: "ok", Attempt: 1, LatencyMS: 420, Tokens: 90})
	l.Close()
	s := &Server{Router: rt, Cfg: cfg, Version: "t", Started: time.Now(), UsagePath: logPath,
		Validate: func(_ context.Context, p catalogue.Provider, key string) error {
			if key == "bad" {
				return errors.New("rejected the key (HTTP 401)")
			}
			return nil
		},
		SaveConfig: func(*config.Config) error { return nil },
	}
	return s.Handler(), s
}

var authz = map[string]string{"Authorization": "Bearer tok", "Content-Type": "application/json"}

func TestOverview(t *testing.T) {
	h, _ := dashServer(t, mem{"groq": "k"})
	w := call(h, "GET", "/mangoman/overview", "127.0.0.1:4141", authz, "")
	var ov Overview
	if err := json.Unmarshal(w.Body.Bytes(), &ov); err != nil || w.Code != 200 {
		t.Fatalf("%d %v %s", w.Code, err, w.Body)
	}
	st := map[string]string{}
	for _, p := range ov.Providers {
		st[p.ID] = p.Status
	}
	if st["groq"] != PSConnected || st["nvidia"] != PSNotConnected || st["ollama"] != PSNotRunning {
		t.Fatalf("statuses %v", st)
	}
	ready, notConnected := 0, 0
	for _, m := range ov.Models {
		switch m.State {
		case "ready":
			ready++
			if m.Provider != "groq" {
				t.Fatalf("only groq models should be ready: %+v", m)
			}
		case "not_connected":
			notConnected++
		}
	}
	if ready == 0 || notConnected == 0 {
		t.Fatalf("ready %d, not connected %d", ready, notConnected)
	}
	if strings.Contains(w.Body.String(), `"k"`) {
		t.Fatal("overview leaks a key")
	}
}

func TestActivity(t *testing.T) {
	h, _ := dashServer(t, mem{})
	w := call(h, "GET", "/mangoman/activity?hours=24", "127.0.0.1:4141", authz, "")
	var a store.Activity
	if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil || a.Requests != 1 || len(a.Recent) != 1 || len(a.Hourly) != 1 {
		t.Fatalf("%v %+v", err, a)
	}
}

func TestAddRemoveKeyAndExclude(t *testing.T) {
	st := mem{}
	h, s := dashServer(t, st)
	// The dashboard page itself calls from its own origin; that must work.
	own := map[string]string{"Authorization": "Bearer tok", "Content-Type": "application/json", "Origin": "http://127.0.0.1:4141"}

	w := call(h, "POST", "/mangoman/keys", "127.0.0.1:4141", own, `{"provider":"groq","key":"bad"}`)
	if w.Code != 422 || st["groq"] != "" {
		t.Fatalf("bad key: %d %s", w.Code, w.Body)
	}
	w = call(h, "POST", "/mangoman/keys", "127.0.0.1:4141", own, `{"provider":"groq","key":"gsk_ok"}`)
	if w.Code != 200 || st["groq"] != "gsk_ok" || !strings.Contains(w.Body.String(), `"connected"`) {
		t.Fatalf("add: %d %s %v", w.Code, w.Body, st)
	}
	if k, _ := s.Router.Keys.Get("groq"); k != "gsk_ok" {
		t.Fatal("router does not see the new key")
	}
	w = call(h, "POST", "/mangoman/providers/groq/exclude", "127.0.0.1:4141", own, `{"excluded":true}`)
	if w.Code != 200 || !s.Cfg.Excluded("groq") || !strings.Contains(w.Body.String(), `"excluded"`) {
		t.Fatalf("exclude: %d %s", w.Code, w.Body)
	}
	call(h, "POST", "/mangoman/providers/groq/exclude", "127.0.0.1:4141", own, `{"excluded":false}`)
	w = call(h, "DELETE", "/mangoman/keys/groq", "127.0.0.1:4141", own, "")
	if w.Code != 200 || st["groq"] != "" {
		t.Fatalf("remove: %d %s", w.Code, w.Body)
	}
	if k, _ := s.Router.Keys.Get("groq"); k != "" {
		t.Fatal("router still has the removed key")
	}
}

func TestEnvKeyCannotBeRemovedFromDashboard(t *testing.T) {
	t.Setenv("TEST_NVIDIA_KEY", "from-env")
	h, _ := dashServer(t, mem{})
	w := call(h, "DELETE", "/mangoman/keys/nvidia", "127.0.0.1:4141", authz, "")
	if w.Code != 409 || !strings.Contains(w.Body.String(), "TEST_NVIDIA_KEY") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestDashboardSecurity(t *testing.T) {
	h, _ := dashServer(t, mem{})
	// Another website cannot add keys, even with the token.
	evil := map[string]string{"Authorization": "Bearer tok", "Content-Type": "application/json", "Origin": "https://evil.example"}
	if w := call(h, "POST", "/mangoman/keys", "127.0.0.1:4141", evil, `{"provider":"groq","key":"x"}`); w.Code != 403 {
		t.Fatalf("cross-site add key: %d", w.Code)
	}
	// No token, no data.
	if w := call(h, "GET", "/mangoman/overview", "127.0.0.1:4141", nil, ""); w.Code != 401 {
		t.Fatalf("overview without token: %d", w.Code)
	}
	// The page loads without a token but with strict headers.
	w := call(h, "GET", "/ui/", "127.0.0.1:4141", nil, "")
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("ui: %d %v", w.Code, w.Header())
	}
}

func TestAgentsAPI(t *testing.T) {
	t.Setenv("MANGOMAN_HOME", t.TempDir())
	t.Setenv("MANGOMAN_REGISTRY", "file:///nonexistent/")
	t.Setenv("MANGOMAN_REGISTRY_KEY", "")
	h, _ := dashServer(t, mem{})
	do := func(method, path, body string) *httptest.ResponseRecorder {
		return call(h, method, path, "127.0.0.1:4141", authz, body)
	}
	w := do("GET", "/mangoman/agents", "")
	if w.Code != 200 || !strings.Contains(strings.ReplaceAll(w.Body.String(), " ", ""), `"installed":[]`) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	w = do("GET", "/mangoman/agents/market", "")
	if w.Code != 200 || !strings.Contains(strings.ReplaceAll(w.Body.String(), " ", ""), `"open":false`) || !strings.Contains(w.Body.String(), "not open yet") {
		t.Fatalf("market: %d %s", w.Code, w.Body)
	}
	if w = do("DELETE", "/mangoman/agents/nothing-here", ""); w.Code != 404 {
		t.Fatalf("remove missing: %d", w.Code)
	}
	if w = do("POST", "/mangoman/agents/install", `{}`); w.Code != 400 {
		t.Fatalf("install without a name: %d", w.Code)
	}
}

func TestGuardianApprovals(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MANGOMAN_HOME", home)
	proj := t.TempDir()
	cfgPath := filepath.Join(proj, "guardian.json")
	os.WriteFile(cfgPath, []byte(`{"app":"shop","repo":"."}`), 0o644)
	os.MkdirAll(filepath.Join(proj, ".guardian"), 0o700)
	os.WriteFile(filepath.Join(proj, ".guardian", "incidents.json"),
		[]byte(`{"a":{"id":"a","kind":"fix","check":"site","problem":"down","status":"ready","opened":"2026-10-05T08:00:00Z","qa":"long report"}}`), 0o600)
	guardian.Register(filepath.Join(home, "guardian", "projects.json"), cfgPath)
	h, _ := dashServer(t, mem{})
	w := call(h, "GET", "/mangoman/guardian", "127.0.0.1:4141", authz, "")
	var got []GuardianProject
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || len(got) != 1 || got[0].App != "shop" || len(got[0].Incidents) != 1 || got[0].Incidents[0].QA != "" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := call(h, "POST", "/mangoman/guardian/approve", "127.0.0.1:4141", authz, `{"config":"/etc/other.json","id":"a"}`); w.Code != 404 {
		t.Fatalf("only registered projects: %d", w.Code)
	}
	if w := call(h, "POST", "/mangoman/guardian/deploy", "127.0.0.1:4141", authz, `{"config":"`+cfgPath+`","id":"a"}`); w.Code != 400 {
		t.Fatalf("only approve and reject: %d", w.Code)
	}
}

func TestTeamKeysOnTheDashboard(t *testing.T) {
	st := mem{}
	h, s := dashServer(t, st)
	// Bad names and bad keys are refused before anything is saved.
	if w := call(h, "POST", "/mangoman/keys", "127.0.0.1:4141", authz, `{"provider":"zen","key":"k","team":"Ravi Kumar!"}`); w.Code != 400 {
		t.Fatalf("bad name: %d %s", w.Code, w.Body)
	}
	if w := call(h, "POST", "/mangoman/keys", "127.0.0.1:4141", authz, `{"provider":"zen","key":"bad","team":"ravi"}`); w.Code != 422 || len(s.Cfg.GetTeamKeys("zen")) != 0 {
		t.Fatalf("rejected key must not be listed: %d %v", w.Code, s.Cfg.GetTeamKeys("zen"))
	}
	// A teammate's key alone connects the provider.
	w := call(h, "POST", "/mangoman/keys", "127.0.0.1:4141", authz, `{"provider":"zen","key":"zen-ravi","team":"Ravi"}`)
	var p DashProvider
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if st["zen#ravi"] != "zen-ravi" || p.Status != PSConnected || p.KeySource != "team" ||
		len(p.TeamKeys) != 1 || p.TeamKeys[0].Name != "ravi" || p.TeamKeys[0].Status != TKWorking {
		t.Fatalf("team key not shown as connected: %+v store=%v", p, st)
	}
	var ov Overview
	_ = json.Unmarshal(call(h, "GET", "/mangoman/overview", "127.0.0.1:4141", authz, "").Body.Bytes(), &ov)
	if ov.TeamKeyNotice == "" {
		t.Fatal("the dashboard needs the team key notice")
	}
	// Your own key turned down: still connected through the team, and it says so.
	s.Router.Keys.Disable("zen")
	zen, _ := s.Router.Cat.Provider("zen")
	if p := s.providerStatus(zen, 0); p.Status != PSConnected || p.KeySource != "team" || !p.OwnRejected {
		t.Fatalf("own key rejected with team keys working: %+v", p)
	}
	// Removing it takes it out of the store and the list.
	// Without the team key, the rejected own key shows again.
	w = call(h, "DELETE", "/mangoman/keys/zen/team/ravi", "127.0.0.1:4141", authz, "")
	p = DashProvider{}
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	if _, ok := st["zen#ravi"]; ok || len(s.Cfg.GetTeamKeys("zen")) != 0 || p.Status != PSKeyRejected || len(p.TeamKeys) != 0 {
		t.Fatalf("team key not removed: %d %+v store=%v", w.Code, p, st)
	}
}

func TestPersonToken(t *testing.T) {
	h, s := dashServer(t, mem{})
	s.Cfg.SetPerson("ravi", "tok-ravi")
	ok := map[string]string{"Authorization": "Bearer tok-ravi"}
	if w := call(h, "GET", "/v1/models", "127.0.0.1:4141", ok, ""); w.Code != 200 {
		t.Fatalf("a person's token should work: %d", w.Code)
	}
	// A person's token reaches only the models: not settings, keys or the workspace.
	for _, path := range []string{"/mangoman/overview", "/mangoman/code/info", "/mangoman/status"} {
		if w := call(h, "GET", path, "127.0.0.1:4141", ok, ""); w.Code != 401 {
			t.Fatalf("a person's token must not open %s: %d", path, w.Code)
		}
	}
	bad := map[string]string{"Authorization": "Bearer tok-nobody"}
	if w := call(h, "GET", "/v1/models", "127.0.0.1:4141", bad, ""); w.Code != 401 {
		t.Fatalf("an unknown token must be refused: %d", w.Code)
	}
	s.Cfg.SetPerson("ravi", "")
	if w := call(h, "GET", "/v1/models", "127.0.0.1:4141", ok, ""); w.Code != 401 {
		t.Fatalf("a removed person's token must stop working: %d", w.Code)
	}
}
