package ingress

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/breaker"
	"github.com/pusyc74-prog/mangoman/internal/catalogue"
	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/core"
	"github.com/pusyc74-prog/mangoman/internal/keys"
	"github.com/pusyc74-prog/mangoman/internal/quota"
	"github.com/pusyc74-prog/mangoman/internal/setup"
	"github.com/pusyc74-prog/mangoman/internal/store"
)

//go:embed ui
var uiFiles embed.FS

// Provider statuses shown on the dashboard.
const (
	PSConnected    = "connected"
	PSNotConnected = "not_connected"
	PSKeyRejected  = "key_rejected"
	PSExcluded     = "excluded"
	PSRunning      = "running"     // local Ollama with models
	PSNotRunning   = "not_running" // local Ollama absent
)

// DashProvider is one provider card.
type DashProvider struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	KeySource   string `json:"key_source,omitempty"` // "store" or "env"
	EnvVar      string `json:"env_var,omitempty"`
	SignupURL   string `json:"signup_url,omitempty"`
	Policy      string `json:"data_policy"`
	Trains      string `json:"trains_on_data"`
	Models      int    `json:"models"`
	Local       bool   `json:"local,omitempty"`
	NeedsKey    bool   `json:"needs_key"`
	BreakerOpen int    `json:"models_unavailable,omitempty"`
}

// DashModel is one row of the models table.
type DashModel struct {
	Provider     string           `json:"provider"`
	Model        string           `json:"model"`
	Upstream     string           `json:"upstream"`
	Connected    bool             `json:"connected"`
	Context      int              `json:"context"`
	Caps         []string         `json:"caps"`
	Limits       catalogue.Limits `json:"limits"`
	LimitsSource string           `json:"limits_source"` // "provider" (learned from headers) or "catalogue"
	Policy       string           `json:"data_policy"`
	Trains       string           `json:"trains_on_data"`
	ReqToday     int              `json:"requests_24h"`
	TokToday     int              `json:"tokens_24h"`
	BlockedUntil *time.Time       `json:"blocked_until,omitempty"`
	State        string           `json:"state"` // ready, cooling_down, rate_limited, not_connected
	Samples      int              `json:"samples"`
	OK           int              `json:"ok"`
	Failed       int              `json:"failed"`
	LatencyMS    float64          `json:"latency_ms"`
	LastUsed     *time.Time       `json:"last_used,omitempty"`
	LastOutcome  string           `json:"last_outcome,omitempty"`
}

// Overview is the dashboard's main payload.
type Overview struct {
	Version   string         `json:"version"`
	UptimeS   int            `json:"uptime_s"`
	Catalogue string         `json:"catalogue"`
	Port      int            `json:"port"`
	KeyStore  string         `json:"key_store"`
	Providers []DashProvider `json:"providers"`
	Models    []DashModel    `json:"models"`
	// Favorites is My list, in order: the router tries these first.
	Favorites []string `json:"favorites"`
	NewModels int      `json:"new_models"` // radar items marked new
}

func (s *Server) dashRoutes(mux *http.ServeMux) {
	sub, _ := fs.Sub(uiFiles, "ui")
	static := http.StripPrefix("/ui/", http.FileServer(http.FS(sub)))
	mux.Handle("GET /ui/", securityHeaders(static))
	mux.Handle("GET /ui", http.RedirectHandler("/ui/", http.StatusFound))
	mux.Handle("GET /mangoman/overview", s.auth(http.HandlerFunc(s.overview)))
	mux.Handle("GET /mangoman/activity", s.auth(http.HandlerFunc(s.activity)))
	mux.Handle("POST /mangoman/keys", s.auth(http.HandlerFunc(s.addKey)))
	mux.Handle("DELETE /mangoman/keys/{provider}", s.auth(http.HandlerFunc(s.removeKey)))
	mux.Handle("POST /mangoman/providers/{provider}/exclude", s.auth(http.HandlerFunc(s.setExcluded)))
	s.myListRoutes(mux)
}

// securityHeaders lock the dashboard page down: own files only, no
// framing, no referrer, nothing loaded from the internet.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) providerStatus(p catalogue.Provider, modelCount int) DashProvider {
	pol := p.Policy
	d := DashProvider{ID: p.ID, Name: p.Name, SignupURL: p.SignupURL, Policy: pol.Label(), Trains: pol.TrainsOnData,
		Models: modelCount, Local: p.Local, NeedsKey: p.NeedsKey, EnvVar: s.Router.Keys.EnvName(p.ID)}
	switch {
	case s.Cfg.Excluded(p.ID):
		d.Status = PSExcluded
	case p.Local && modelCount > 0:
		d.Status = PSRunning
	case p.Local:
		d.Status = PSNotRunning
	case s.Router.Keys.Rejected(p.ID):
		d.Status = PSKeyRejected
	default:
		if k, src := s.Router.Keys.Get(p.ID); k != "" {
			d.Status, d.KeySource = PSConnected, string(src)
		} else {
			d.Status = PSNotConnected
		}
	}
	return d
}

func (s *Server) overview(w http.ResponseWriter, _ *http.Request) {
	rt := s.Router
	ov := Overview{Version: s.Version, UptimeS: int(time.Since(s.Started).Seconds()), Catalogue: rt.Cat.Version, Port: s.Cfg.Port,
		Favorites: nonNil(s.Cfg.GetFavorites()), NewModels: s.radarView().NewCount}
	if st := rt.Keys.Store(); st != nil {
		ov.KeyStore = st.Name()
	}
	models := rt.Cat.AllModels()
	counts := map[string]int{}
	for _, m := range models {
		counts[m.Provider]++
	}
	byProv := map[string]int{} // provider id -> index in ov.Providers
	for _, p := range rt.Cat.AllProviders() {
		byProv[p.ID] = len(ov.Providers)
		ov.Providers = append(ov.Providers, s.providerStatus(p, counts[p.ID]))
	}

	quotas := map[string]quota.Status{}
	for _, q := range rt.Quota.Snapshot() {
		quotas[q.Key.Provider+"/"+q.Key.Model] = q
	}
	health := map[string]int{}
	snaps := rt.Health.Snapshot()
	for i, h := range snaps {
		health[h.Target] = i
	}
	now := time.Now()
	for _, m := range models {
		p, _ := rt.Cat.Provider(m.Provider)
		pol := rt.Cat.PolicyFor(m)
		dp := &ov.Providers[byProv[m.Provider]]
		dm := DashModel{
			Provider: m.Provider, Model: m.Canonical, Upstream: m.Upstream, Context: m.Context, Caps: m.Caps,
			Policy: pol.Label(), Trains: pol.TrainsOnData, LimitsSource: "catalogue",
			Connected: dp.Status == PSConnected || dp.Status == PSRunning,
		}
		learned := rt.Quota.Learned(quota.Key{Provider: p.ID, Account: "default", Model: m.Canonical})
		dm.Limits = m.Limits
		if learned.RPM+learned.RPD+learned.TPM+learned.TPD > 0 {
			dm.LimitsSource = "provider"
			if learned.RPM > 0 {
				dm.Limits.RPM = learned.RPM
			}
			if learned.RPD > 0 {
				dm.Limits.RPD = learned.RPD
			}
			if learned.TPM > 0 {
				dm.Limits.TPM = learned.TPM
			}
			if learned.TPD > 0 {
				dm.Limits.TPD = learned.TPD
			}
		}
		if q, ok := quotas[m.Provider+"/"+m.Canonical]; ok {
			dm.ReqToday, dm.TokToday = q.ReqToday, q.TokToday
			if !q.BlockedUntil.IsZero() && q.BlockedUntil.After(now) {
				b := q.BlockedUntil
				dm.BlockedUntil = &b
			}
		}
		if i, ok := health[m.ID()]; ok {
			h := snaps[i]
			dm.Samples, dm.OK, dm.Failed, dm.LatencyMS, dm.LastOutcome = h.Samples, h.OK, h.Failed, h.LatencyMS, h.LastOut
			lu := h.LastUsed
			dm.LastUsed = &lu
		}
		switch {
		case !dm.Connected:
			dm.State = "not_connected"
		case dm.BlockedUntil != nil:
			dm.State = "rate_limited"
		case rt.Breakers.StateOf(m.ID()) == breaker.Open:
			dm.State = "cooling_down"
			dp.BreakerOpen++
		default:
			dm.State = "ready"
		}
		ov.Models = append(ov.Models, dm)
	}
	writeJSON(w, ov)
}

func (s *Server) activity(w http.ResponseWriter, r *http.Request) {
	hours := 24
	if v, err := strconv.Atoi(r.URL.Query().Get("hours")); err == nil && v > 0 && v <= 24*30 {
		hours = v
	}
	if s.UsagePath == "" {
		writeJSON(w, store.Activity{})
		return
	}
	a, err := store.Analyze(s.UsagePath, time.Now().Add(-time.Duration(hours)*time.Hour), 50)
	if err != nil {
		core.WriteError(w, http.StatusInternalServerError, "usage_unreadable", err.Error())
		return
	}
	writeJSON(w, a)
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func (s *Server) addKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Provider string `json:"provider"`
		Key      string `json:"key"`
	}
	if err := readJSON(r, &in); err != nil {
		core.WriteError(w, http.StatusBadRequest, "bad_request", "send {\"provider\":..., \"key\":...}")
		return
	}
	st := s.Router.Keys.Store()
	if st == nil {
		core.WriteError(w, http.StatusServiceUnavailable, "no_key_store", "no key store available")
		return
	}
	validate := s.Validate
	if validate == nil {
		validate = s.Router.Client.ValidateKey
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	p, err := setup.ConnectKey(ctx, s.Router.Cat, st, validate, in.Provider, in.Key)
	if err != nil {
		core.WriteError(w, http.StatusUnprocessableEntity, "key_not_connected", err.Error())
		return
	}
	s.Router.Keys.Forget(p.ID)
	writeJSON(w, s.providerStatus(p, s.modelCount(p.ID)))
}

func (s *Server) removeKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("provider")
	p, ok := s.Router.Cat.Provider(id)
	if !ok {
		core.WriteError(w, http.StatusNotFound, "unknown_provider", "unknown provider "+id)
		return
	}
	if _, src := s.Router.Keys.Get(id); src == keys.FromEnv {
		core.WriteError(w, http.StatusConflict, "key_from_environment",
			"this key comes from the "+s.Router.Keys.EnvName(id)+" environment variable; unset it to remove it")
		return
	}
	st := s.Router.Keys.Store()
	if st == nil {
		core.WriteError(w, http.StatusServiceUnavailable, "no_key_store", "no key store available")
		return
	}
	if err := st.Delete(id); err != nil && !errors.Is(err, keys.ErrNotFound) {
		core.WriteError(w, http.StatusInternalServerError, "key_not_removed", err.Error())
		return
	}
	s.Router.Keys.Forget(id)
	writeJSON(w, s.providerStatus(p, s.modelCount(id)))
}

func (s *Server) setExcluded(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("provider")
	p, ok := s.Router.Cat.Provider(id)
	if !ok {
		core.WriteError(w, http.StatusNotFound, "unknown_provider", "unknown provider "+id)
		return
	}
	var in struct {
		Excluded bool `json:"excluded"`
	}
	if err := readJSON(r, &in); err != nil {
		core.WriteError(w, http.StatusBadRequest, "bad_request", "send {\"excluded\": true|false}")
		return
	}
	s.Cfg.SetExcluded(id, in.Excluded)
	save := s.SaveConfig
	if save == nil {
		save = config.Save
	}
	if err := save(s.Cfg); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "config_not_saved", err.Error())
		return
	}
	writeJSON(w, s.providerStatus(p, s.modelCount(id)))
}

func (s *Server) modelCount(provider string) int {
	n := 0
	for _, m := range s.Router.Cat.AllModels() {
		if m.Provider == provider {
			n++
		}
	}
	return n
}

// ownOrigins are the browser origins of the dashboard itself.
func (s *Server) ownOrigin(o string) bool {
	port := strconv.Itoa(s.Cfg.Port)
	for _, h := range []string{"127.0.0.1", "localhost"} {
		if strings.EqualFold(o, "http://"+h+":"+port) {
			return true
		}
	}
	return false
}
