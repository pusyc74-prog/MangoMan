package ingress

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/core"
	"github.com/pusyc74-prog/mangoman/internal/guardian"
)

// GuardianProject is one app Guardian looks after, for the dashboard.
type GuardianProject struct {
	App       string              `json:"app"`
	Config    string              `json:"config"`
	Incidents []guardian.Incident `json:"incidents"`
}

func (s *Server) guardianRoutes(mux *http.ServeMux) {
	mux.Handle("GET /mangoman/guardian", s.auth(http.HandlerFunc(s.guardianList)))
	mux.Handle("POST /mangoman/guardian/{action}", s.auth(http.HandlerFunc(s.guardianAct)))
}

func projectsList() string {
	d, _ := config.Dir()
	return filepath.Join(d, "guardian", "projects.json")
}

// guardianList shows what is under way or waiting, and the last day.
func (s *Server) guardianList(w http.ResponseWriter, _ *http.Request) {
	out := []GuardianProject{}
	for _, p := range guardian.Projects(projectsList()) {
		g, err := guardian.OpenProject(p)
		if err != nil {
			continue
		}
		gp := GuardianProject{App: g.Cfg.App, Config: p, Incidents: []guardian.Incident{}}
		for _, i := range g.Incidents() {
			if i.Status == "queued" || i.Status == "working" || i.Status == "ready" || i.Status == "deploying" ||
				i.Status == "needs_you" || time.Since(i.Opened) < 24*time.Hour {
				i.QA, i.Work = "", "" // long and local; the CLI shows them
				gp.Incidents = append(gp.Incidents, i)
			}
		}
		out = append(out, gp)
	}
	writeJSON(w, out)
}

// guardianAct approves or rejects a change. Approving deploys to
// production, which takes a while, so it runs as its own process.
func (s *Server) guardianAct(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	var in struct {
		Config string `json:"config"`
		ID     string `json:"id"`
	}
	if err := readJSON(r, &in); err != nil || in.ID == "" || (action != "approve" && action != "reject") {
		core.WriteError(w, http.StatusBadRequest, "bad_request", "send config and id to /mangoman/guardian/approve or /reject")
		return
	}
	if !slices.Contains(guardian.Projects(projectsList()), in.Config) {
		core.WriteError(w, http.StatusNotFound, "not_found", "no such Guardian project on this computer")
		return
	}
	self, err := os.Executable()
	if err != nil {
		core.WriteError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	cmd := exec.Command(self, "guardian", action, "--config", in.Config, in.ID)
	cmd.Dir = filepath.Dir(in.Config)
	if err := cmd.Start(); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	go func() { _ = cmd.Wait() }()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]string{"status": action + " started"})
}
