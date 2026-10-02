package ingress

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/core"
	"github.com/pusyc74-prog/mangoman/internal/radar"
)

// RadarView is the floating "new models" list.
type RadarView struct {
	LastScan *time.Time   `json:"last_scan,omitempty"`
	NewCount int          `json:"new_count"`
	Items    []radar.Item `json:"items"`
	Enabled  bool         `json:"enabled"`
}

func (s *Server) myListRoutes(mux *http.ServeMux) {
	mux.Handle("GET /mangoman/favorites", s.auth(http.HandlerFunc(s.getFavorites)))
	mux.Handle("PUT /mangoman/favorites", s.auth(http.HandlerFunc(s.putFavorites)))
	mux.Handle("GET /mangoman/groups", s.auth(http.HandlerFunc(s.getGroups)))
	mux.Handle("PUT /mangoman/groups/{name}", s.auth(http.HandlerFunc(s.putGroup)))
	mux.Handle("GET /mangoman/radar", s.auth(http.HandlerFunc(s.getRadar)))
	mux.Handle("POST /mangoman/radar/scan", s.auth(http.HandlerFunc(s.scanRadar)))
	mux.Handle("POST /mangoman/radar/add", s.auth(http.HandlerFunc(s.addFromRadar)))
}

func (s *Server) save() error {
	if s.SaveConfig != nil {
		return s.SaveConfig(s.Cfg)
	}
	return config.Save(s.Cfg)
}

func (s *Server) getFavorites(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string][]string{"models": nonNil(s.Cfg.GetFavorites())})
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// knownModel reports whether a My list entry names a catalogue model, by
// canonical name or provider/canonical.
func (s *Server) knownModel(entry string) bool {
	for _, m := range s.Router.Cat.AllModels() {
		if strings.EqualFold(entry, m.Canonical) || strings.EqualFold(entry, m.ID()) {
			return true
		}
	}
	return false
}

func (s *Server) putFavorites(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Models []string `json:"models"`
	}
	if err := readJSON(r, &in); err != nil {
		core.WriteError(w, http.StatusBadRequest, "bad_request", "send {\"models\": [\"provider/model\", ...]}")
		return
	}
	if len(in.Models) > 50 {
		core.WriteError(w, http.StatusBadRequest, "too_many", "My list holds up to 50 models")
		return
	}
	for _, m := range in.Models {
		if !s.knownModel(strings.TrimSpace(m)) {
			core.WriteError(w, http.StatusUnprocessableEntity, "unknown_model", "no model called "+m+" in the catalogue")
			return
		}
	}
	s.Cfg.SetFavorites(in.Models)
	if err := s.save(); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "config_not_saved", err.Error())
		return
	}
	s.getFavorites(w, r)
}

func (s *Server) radarView() RadarView {
	v := RadarView{Items: []radar.Item{}}
	if s.Radar == nil {
		return v
	}
	v.Enabled = true
	items, last := s.Radar.Offer()
	if items != nil {
		v.Items = items
	}
	if !last.IsZero() {
		v.LastScan = &last
	}
	for _, it := range items {
		if it.New {
			v.NewCount++
		}
	}
	return v
}

func (s *Server) getRadar(w http.ResponseWriter, _ *http.Request) { writeJSON(w, s.radarView()) }

func (s *Server) scanRadar(w http.ResponseWriter, r *http.Request) {
	if s.Radar == nil {
		core.WriteError(w, http.StatusServiceUnavailable, "radar_off", "the new-model radar is not running")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	if _, err := s.Radar.Scan(ctx); err != nil {
		if s.Router.Logf != nil {
			s.Router.Logf("radar: %v", err)
		}
	}
	writeJSON(w, s.radarView())
}

// addFromRadar adds a model the radar found to the catalogue and to the end
// of My list. Only models a provider actually listed can be added.
func (s *Server) addFromRadar(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Provider string `json:"provider"`
		Upstream string `json:"upstream"`
	}
	if err := readJSON(r, &in); err != nil || in.Provider == "" || in.Upstream == "" {
		core.WriteError(w, http.StatusBadRequest, "bad_request", "send {\"provider\":..., \"upstream\":...}")
		return
	}
	cat := s.Router.Cat
	if !cat.HasUpstream(in.Provider, in.Upstream) {
		if s.Radar == nil || !s.Radar.Listed(in.Provider, in.Upstream) {
			core.WriteError(w, http.StatusNotFound, "not_listed", "the provider does not list "+in.Upstream)
			return
		}
		cat.AddModel(cat.DiscoveredModel(in.Provider, in.Upstream))
		s.Cfg.AddCustomModel(config.CustomModel{Provider: in.Provider, Upstream: in.Upstream,
			Added: time.Now().UTC().Format(time.RFC3339)})
	}
	id := ""
	for _, m := range cat.AllModels() {
		if m.Provider == in.Provider && m.Upstream == in.Upstream {
			id = m.ID()
		}
	}
	fav := s.Cfg.GetFavorites()
	found := false
	for _, f := range fav {
		if strings.EqualFold(f, id) {
			found = true
		}
	}
	if !found {
		s.Cfg.SetFavorites(append(fav, id))
	}
	if err := s.save(); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "config_not_saved", err.Error())
		return
	}
	writeJSON(w, map[string]any{"added": id, "models": nonNil(s.Cfg.GetFavorites())})
}

func (s *Server) groupsView() map[string][]string {
	out := map[string][]string{}
	for _, n := range s.Cfg.GroupNames() {
		out[n] = s.Cfg.Group(n)
	}
	return out
}

func (s *Server) getGroups(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{"groups": s.groupsView()})
}

// putGroup creates, replaces or (with an empty list) deletes a group.
func (s *Server) putGroup(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(strings.TrimSpace(r.PathValue("name")))
	if name == "" || len(name) > 40 || strings.ContainsAny(name, "/ ") {
		core.WriteError(w, http.StatusBadRequest, "bad_group_name", "a group name is one word, up to 40 characters")
		return
	}
	var in struct {
		Models []string `json:"models"`
	}
	if err := readJSON(r, &in); err != nil {
		core.WriteError(w, http.StatusBadRequest, "bad_request", "send {\"models\": [\"provider/model\", ...]}")
		return
	}
	if len(in.Models) > 20 {
		core.WriteError(w, http.StatusBadRequest, "too_many", "a group holds up to 20 models")
		return
	}
	for _, m := range in.Models {
		if !s.knownModel(strings.TrimSpace(m)) {
			core.WriteError(w, http.StatusUnprocessableEntity, "unknown_model", "no model called "+m+" in the catalogue")
			return
		}
	}
	s.Cfg.SetGroup(name, in.Models)
	if err := s.save(); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "config_not_saved", err.Error())
		return
	}
	s.getGroups(w, r)
}
