package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/catalogue"
	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/ingress"
	"github.com/pusyc74-prog/mangoman/internal/keys"
	"github.com/pusyc74-prog/mangoman/internal/router"
)

func direct(srv *httptest.Server) SendFunc {
	return func(ctx context.Context, body []byte, stream bool) (*http.Response, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/chat/completions", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		return srv.Client().Do(req)
	}
}

func TestReferenceProviderPassesAll(t *testing.T) {
	srv := httptest.NewServer(Reference(false, nil))
	defer srv.Close()
	for _, c := range Cases() {
		res := Run(context.Background(), direct(srv), c, "ref-model")
		if res.Status != Pass {
			t.Errorf("%s: %s %s", c.ID, res.Status, res.Detail)
		}
	}
}

func TestChecksCatchSloppyProvider(t *testing.T) {
	srv := httptest.NewServer(Reference(true, nil))
	defer srv.Close()
	want := map[string]Status{"tools": Fail, "json": Fail, "stream": Fail, "stream_tools": Fail, "basic": Pass}
	for _, c := range Cases() {
		w, ok := want[c.ID]
		if !ok {
			continue
		}
		if res := Run(context.Background(), direct(srv), c, "ref-model"); res.Status != w {
			t.Errorf("%s: got %s (%s), want %s", c.ID, res.Status, res.Detail, w)
		}
	}
}

func TestRateHeadersCaptured(t *testing.T) {
	srv := httptest.NewServer(Reference(false, nil))
	defer srv.Close()
	res := Run(context.Background(), direct(srv), Cases()[0], "m")
	if res.RateHeaders["x-ratelimit-remaining-requests"] != "99" {
		t.Fatalf("got %v", res.RateHeaders)
	}
}

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

// TestThroughRouter replays the corpus through the real local endpoint and
// router against the reference provider: the router must not break any
// behaviour a client relies on (streaming, tool deltas, JSON, finish reasons).
func TestThroughRouter(t *testing.T) {
	up := httptest.NewTLSServer(Reference(false, nil))
	defer up.Close()
	cat := catalogue.Catalogue{Version: "t",
		Providers: []catalogue.Provider{{ID: "ref", Name: "ref", BaseURL: up.URL, Kind: "openai", NeedsKey: true, Speed: 0.5,
			Quirks: catalogue.Quirks{StreamUsage: true}}},
		Models: []catalogue.Model{{Canonical: "ref-model", Provider: "ref", Upstream: "ref-model", Free: true, Context: 32000,
			Caps: []string{"tools", "json", "streaming"}, Quality: map[string]float64{"default": 0.8}}},
	}
	data, _ := json.Marshal(&cat)
	parsed, err := catalogue.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Port: 4141, Token: "tok", MaxAttempts: 6}
	rt := router.New(parsed, keys.NewResolver(mem{"ref": "k"}, nil), cfg)
	rt.Client.HTTP = up.Client()
	h := (&ingress.Server{Router: rt, Cfg: cfg, Version: "t", Started: time.Now()}).Handler()
	local := httptest.NewServer(h)
	defer local.Close()

	send := func(ctx context.Context, body []byte, stream bool) (*http.Response, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, local.URL+"/v1/chat/completions", bytes.NewReader(body))
		req.Host = "127.0.0.1:4141"
		req.Header.Set("Authorization", "Bearer tok")
		req.Header.Set("Content-Type", "application/json")
		return local.Client().Do(req)
	}
	for _, c := range Cases() {
		if c.DirectOnly {
			continue
		}
		if res := Run(context.Background(), send, c, "free/auto"); res.Status != Pass {
			t.Errorf("%s through router: %s %s", c.ID, res.Status, res.Detail)
		}
	}
}
