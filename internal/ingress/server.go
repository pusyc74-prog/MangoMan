// Package ingress is the local HTTP endpoint clients talk to. It binds to
// 127.0.0.1 only and rejects requests without the local token, with a
// foreign Host header (DNS rebinding) or from a browser origin that is not
// allow-listed (CSRF).
package ingress

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/brain"
	"github.com/pusyc74-prog/mangoman/internal/breaker"
	"github.com/pusyc74-prog/mangoman/internal/classify"
	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/core"
	"github.com/pusyc74-prog/mangoman/internal/radar"
	"github.com/pusyc74-prog/mangoman/internal/router"
	"github.com/pusyc74-prog/mangoman/internal/setup"
)

const maxRequestBody = 32 << 20

// Server is the local endpoint.
type Server struct {
	Router  *router.Router
	Cfg     *config.Config
	Version string
	Started time.Time
	// UsagePath is the usage log the dashboard reads.
	UsagePath string
	// Validate checks keys added from the dashboard (default: ask the provider).
	Validate setup.Validator
	// SaveConfig persists settings changed from the dashboard (default: config.Save).
	SaveConfig func(*config.Config) error
	// Radar finds new free models to offer; nil turns the feature off.
	Radar *radar.Radar
	// Brain is the decision brain (also set on the router); nil = none.
	Brain *brain.Brain
}

// Handler returns the HTTP handler with all checks applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.Handle("POST /v1/chat/completions", s.auth(http.HandlerFunc(s.chat)))
	mux.Handle("GET /v1/models", s.auth(http.HandlerFunc(s.models)))
	s.formatRoutes(mux)
	mux.Handle("GET /mangoman/status", s.auth(http.HandlerFunc(s.status)))
	s.dashRoutes(mux)
	return s.guardHost(mux)
}

// Addr is the loopback address to listen on.
func (s *Server) Addr() string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(s.Cfg.Port))
}

// guardHost blocks DNS rebinding and cross-site browser requests.
func (s *Server) guardHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(r.Host); err == nil {
			host = h
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" && host != "[::1]" {
			core.WriteError(w, http.StatusForbidden, "bad_host", "requests must use 127.0.0.1 or localhost")
			return
		}
		if o := r.Header.Get("Origin"); o != "" && !s.originAllowed(o) {
			core.WriteError(w, http.StatusForbidden, "origin_not_allowed", "browser origin not allowed: add it to allowed_origins in config.json")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) originAllowed(o string) bool {
	if s.ownOrigin(o) {
		return true
	}
	for _, a := range s.Cfg.AllowedOrigins {
		if strings.EqualFold(a, o) {
			return true
		}
	}
	return false
}

// auth accepts the local token as a bearer token or x-api-key header (the
// latter is what Anthropic clients send).
func (s *Server) auth(next http.Handler) http.Handler {
	want := []byte(s.Cfg.Token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if got == "" {
			got = r.Header.Get("x-api-key")
		}
		if len(want) == 0 || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			core.WriteError(w, http.StatusUnauthorized, "invalid_local_token", "missing or wrong local token: see `mangoman init` output")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBody))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			core.WriteError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body over 32 MB")
			return
		}
		core.WriteError(w, http.StatusBadRequest, "bad_request", "could not read body")
		return
	}
	req, err := core.ParseChat(body)
	if err != nil {
		core.WriteError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	s.Router.Handle(w, r, req)
}

type modelEntry struct {
	ID        string   `json:"id"`
	Object    string   `json:"object"`
	Created   int64    `json:"created"`
	OwnedBy   string   `json:"owned_by"`
	Providers []string `json:"providers,omitempty"`
	Models    []string `json:"models,omitempty"` // groups: the models in the group
}

// models lists virtual models plus every canonical model the user can reach.
func (s *Server) models(w http.ResponseWriter, _ *http.Request) {
	now := s.Started.Unix()
	var out []modelEntry
	virt := make([]string, 0, len(classify.Virtual))
	for v := range classify.Virtual {
		virt = append(virt, v)
	}
	sort.Strings(virt)
	for _, v := range virt {
		out = append(out, modelEntry{ID: v, Object: "model", Created: now, OwnedBy: "mangoman"})
	}
	for _, g := range s.Cfg.GroupNames() {
		out = append(out, modelEntry{ID: router.GroupPrefix + g, Object: "model", Created: now, OwnedBy: "mangoman", Models: s.Cfg.Group(g)})
	}
	byModel := map[string][]string{}
	for _, m := range s.Router.Cat.AllModels() {
		p, ok := s.Router.Cat.Provider(m.Provider)
		if !ok || s.Cfg.Excluded(p.ID) || !m.Free {
			continue
		}
		if p.NeedsKey {
			if k, _ := s.Router.Keys.Get(p.ID); k == "" {
				continue
			}
		}
		byModel[m.Canonical] = append(byModel[m.Canonical], p.ID)
	}
	names := make([]string, 0, len(byModel))
	for n := range byModel {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		out = append(out, modelEntry{ID: n, Object: "model", Created: now, OwnedBy: "mangoman", Providers: byModel[n]})
	}
	writeJSON(w, map[string]any{"object": "list", "data": out})
}

// ProviderStatus is one row of /mangoman/status.
type ProviderStatus struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Connected  bool   `json:"connected"`
	KeySource  string `json:"key_source,omitempty"`
	Excluded   bool   `json:"excluded,omitempty"`
	Models     int    `json:"models"`
	OpenModels int    `json:"models_breaker_open,omitempty"`
	Policy     string `json:"data_policy"`
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	cat := s.Router.Cat
	counts := map[string]int{}
	open := map[string]int{}
	for _, m := range cat.AllModels() {
		counts[m.Provider]++
		if s.Router.Breakers.StateOf(m.ID()) == breaker.Open {
			open[m.Provider]++
		}
	}
	var ps []ProviderStatus
	for _, p := range cat.AllProviders() {
		st := ProviderStatus{ID: p.ID, Name: p.Name, Excluded: s.Cfg.Excluded(p.ID), Models: counts[p.ID], OpenModels: open[p.ID], Policy: p.Policy.Label()}
		if p.NeedsKey {
			if k, src := s.Router.Keys.Get(p.ID); k != "" {
				st.Connected, st.KeySource = true, string(src)
			}
		} else {
			st.Connected = counts[p.ID] > 0
		}
		ps = append(ps, st)
	}
	writeJSON(w, map[string]any{
		"version":   s.Version,
		"uptime_s":  int(time.Since(s.Started).Seconds()),
		"catalogue": cat.Version,
		"providers": ps,
		"quota":     s.Router.Quota.Snapshot(),
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
