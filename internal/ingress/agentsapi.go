package ingress

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/agents"
	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/core"
)

func (s *Server) agentRoutes(mux *http.ServeMux) {
	mux.Handle("GET /mangoman/agents", s.auth(http.HandlerFunc(s.listAgents)))
	mux.Handle("GET /mangoman/agents/market", s.auth(http.HandlerFunc(s.market)))
	mux.Handle("POST /mangoman/agents/install", s.auth(http.HandlerFunc(s.installAgent)))
	mux.Handle("DELETE /mangoman/agents/{name}", s.auth(http.HandlerFunc(s.removeAgent)))
}

// The marketplace index is fetched on request and kept for ten minutes.
var marketCache struct {
	sync.Mutex
	at  time.Time
	ix  agents.Index
	err error
}

func agentsDir(w http.ResponseWriter) (string, bool) {
	dir, err := config.Path("agents")
	if err != nil {
		core.WriteError(w, http.StatusInternalServerError, "no_config_dir", err.Error())
		return "", false
	}
	return dir, true
}

func (s *Server) listAgents(w http.ResponseWriter, _ *http.Request) {
	dir, ok := agentsDir(w)
	if !ok {
		return
	}
	list, err := agents.List(dir)
	if err != nil {
		core.WriteError(w, http.StatusInternalServerError, "agents_unreadable", err.Error())
		return
	}
	writeJSON(w, map[string]any{"installed": append([]agents.Manifest{}, list...)})
}

func (s *Server) market(w http.ResponseWriter, _ *http.Request) {
	marketCache.Lock()
	if time.Since(marketCache.at) > 10*time.Minute || marketCache.err != nil {
		marketCache.ix, marketCache.err = agents.DefaultRegistry().Fetch()
		marketCache.at = time.Now()
	}
	ix, err := marketCache.ix, marketCache.err
	marketCache.Unlock()
	if err != nil {
		writeJSON(w, map[string]any{"open": false, "message": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"open": true, "agents": append([]agents.Listing{}, ix.Agents...)})
}

func (s *Server) installAgent(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil || in.Name == "" {
		core.WriteError(w, http.StatusBadRequest, "bad_request", "send {\"name\": \"agent-name\"}")
		return
	}
	dir, ok := agentsDir(w)
	if !ok {
		return
	}
	l, err := agents.DefaultRegistry().InstallListed(in.Name, dir)
	if err != nil {
		core.WriteError(w, http.StatusBadRequest, "install_failed", err.Error())
		return
	}
	writeJSON(w, l)
}

func (s *Server) removeAgent(w http.ResponseWriter, r *http.Request) {
	dir, ok := agentsDir(w)
	if !ok {
		return
	}
	if err := agents.Remove(dir, r.PathValue("name")); err != nil {
		core.WriteError(w, http.StatusNotFound, "no_such_agent", err.Error())
		return
	}
	writeJSON(w, map[string]bool{"removed": true})
}
