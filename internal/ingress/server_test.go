package ingress

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/catalogue"
	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/keys"
	"github.com/pusyc74-prog/mangoman/internal/router"
)

type mem map[string]string

func (m mem) Get(p string) (string, error) {
	if v, ok := m[p]; ok {
		return v, nil
	}
	return "", keys.ErrNotFound
}
func (m mem) Set(p, k string) error { m[p] = k; return nil }
func (m mem) Delete(p string) error { delete(m, p); return nil }
func (mem) Name() string            { return "mem" }

func server(t *testing.T) http.Handler {
	t.Helper()
	cat, err := catalogue.Seed()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Port: 4141, Token: "tok", AllowedOrigins: []string{"http://localhost:5173"}}
	rt := router.New(cat, keys.NewResolver(mem{"groq": "k"}, nil), cfg)
	return (&Server{Router: rt, Cfg: cfg, Version: "test", Started: time.Now()}).Handler()
}

func call(h http.Handler, method, path, host string, hdr map[string]string, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = host
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestSecurityChecks(t *testing.T) {
	h := server(t)
	auth := map[string]string{"Authorization": "Bearer tok"}
	cases := []struct {
		name string
		host string
		hdr  map[string]string
		want int
	}{
		{"no token", "127.0.0.1:4141", nil, 401},
		{"wrong token", "127.0.0.1:4141", map[string]string{"Authorization": "Bearer nope"}, 401},
		{"ok bearer", "127.0.0.1:4141", auth, 200},
		{"ok x-api-key", "localhost:4141", map[string]string{"x-api-key": "tok"}, 200},
		{"dns rebinding", "evil.example:4141", auth, 403},
		{"foreign origin", "127.0.0.1:4141", map[string]string{"Authorization": "Bearer tok", "Origin": "https://evil.example"}, 403},
		{"allowed origin", "127.0.0.1:4141", map[string]string{"Authorization": "Bearer tok", "Origin": "http://localhost:5173"}, 200},
	}
	for _, c := range cases {
		if w := call(h, "GET", "/v1/models", c.host, c.hdr, ""); w.Code != c.want {
			t.Errorf("%s: got %d want %d (%s)", c.name, w.Code, c.want, w.Body)
		}
	}
	if w := call(h, "GET", "/healthz", "127.0.0.1:4141", nil, ""); w.Code != 200 {
		t.Errorf("healthz %d", w.Code)
	}
}

func TestModelsListsVirtualAndConnected(t *testing.T) {
	h := server(t)
	w := call(h, "GET", "/v1/models", "127.0.0.1:4141", map[string]string{"Authorization": "Bearer tok"}, "")
	var out struct {
		Data []struct {
			ID        string   `json:"id"`
			Providers []string `json:"providers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	ids := map[string][]string{}
	for _, d := range out.Data {
		ids[d.ID] = d.Providers
	}
	if _, ok := ids["free/auto"]; !ok {
		t.Fatal("virtual model missing")
	}
	if p := ids["gpt-oss-120b"]; len(p) != 1 || p[0] != "groq" {
		t.Fatalf("only connected providers should be listed, got %v", p)
	}
}

func TestBadChatRequest(t *testing.T) {
	h := server(t)
	w := call(h, "POST", "/v1/chat/completions", "127.0.0.1:4141", map[string]string{"Authorization": "Bearer tok"}, `{"model":"x"}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "messages is required") {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
}

func TestStatus(t *testing.T) {
	h := server(t)
	w := call(h, "GET", "/mangoman/status", "127.0.0.1:4141", map[string]string{"Authorization": "Bearer tok"}, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"groq"`) || strings.Contains(w.Body.String(), `"k"`) {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
}
