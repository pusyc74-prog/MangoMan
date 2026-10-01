package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
