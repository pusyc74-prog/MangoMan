package ingress

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/brain"
	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/core"
)

func (s *Server) brainRoutes(mux *http.ServeMux) {
	mux.Handle("GET /mangoman/brain", s.auth(http.HandlerFunc(s.getBrain)))
	mux.Handle("PUT /mangoman/brain", s.auth(http.HandlerFunc(s.putBrain)))
	mux.Handle("POST /mangoman/brain/test", s.auth(http.HandlerFunc(s.testBrain)))
}

func (s *Server) brainStats() *brain.Stats {
	if s.Brain == nil {
		return nil
	}
	st := s.Brain.Snapshot()
	return &st
}

func (s *Server) getBrain(w http.ResponseWriter, _ *http.Request) {
	if s.Brain == nil {
		core.WriteError(w, http.StatusServiceUnavailable, "brain_unavailable", "the decision brain is not running")
		return
	}
	writeJSON(w, s.brainStats())
}

// putBrain switches the brain on or off, or changes its engine.
func (s *Server) putBrain(w http.ResponseWriter, r *http.Request) {
	if s.Brain == nil {
		core.WriteError(w, http.StatusServiceUnavailable, "brain_unavailable", "the decision brain is not running")
		return
	}
	var in struct {
		Enabled *bool   `json:"enabled"`
		Model   *string `json:"model"`
	}
	if err := readJSON(r, &in); err != nil {
		core.WriteError(w, http.StatusBadRequest, "bad_request", "send {\"enabled\": true|false} and/or {\"model\": \"free/fast\"}")
		return
	}
	cfg := s.Cfg.GetBrain()
	if in.Model != nil {
		m := strings.TrimSpace(*in.Model)
		if m == "" {
			m = brain.DefaultModel
		}
		if msg := s.Router.ScopeProblem(m); msg != "" {
			core.WriteError(w, http.StatusUnprocessableEntity, "unknown_model", msg)
			return
		}
		s.Brain.SetModel(m)
		cfg.Model = m
		if m == brain.DefaultModel {
			cfg.Model = ""
		}
	}
	if in.Enabled != nil {
		s.Brain.SetEnabled(*in.Enabled)
		cfg.Off = !*in.Enabled
	}
	s.Cfg.SetBrain(cfg)
	if err := s.save(); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "config_not_saved", err.Error())
		return
	}
	writeJSON(w, s.brainStats())
}

// testBrain asks the brain one question, for trying an engine.
func (s *Server) testBrain(w http.ResponseWriter, r *http.Request) {
	if s.Brain == nil {
		core.WriteError(w, http.StatusServiceUnavailable, "brain_unavailable", "the decision brain is not running")
		return
	}
	var in struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Question) == "" {
		core.WriteError(w, http.StatusBadRequest, "bad_request", "send {\"question\": \"...\", \"options\": [\"a\", \"b\"]}")
		return
	}
	if len(in.Options) == 0 {
		in.Options = []string{"yes", "no"}
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	tb := &brain.Brain{Call: s.Brain.Call, Model: s.Brain.Snapshot().Model, Budget: 8 * time.Second, MinConf: 0.01}
	a, conf, ok := tb.Choose(ctx, "test", "", in.Question, in.Options)
	out := map[string]any{"answer": a, "confidence": conf, "decided": ok, "latency_ms": time.Since(start).Milliseconds(), "model": tb.Snapshot().Model}
	if !ok {
		if rec := tb.Snapshot().Recent; len(rec) > 0 {
			out["fallback"] = rec[0].Fallback
		}
	}
	writeJSON(w, out)
}

// brainFromConfig builds the brain for a router from saved settings.
func BrainFromConfig(cfg *config.Config, call brain.Caller) *brain.Brain {
	bc := cfg.GetBrain()
	b := &brain.Brain{Call: call, Model: bc.Model}
	b.SetEnabled(!bc.Off)
	return b
}
