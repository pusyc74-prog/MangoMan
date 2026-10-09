package ingress

import (
	"bytes"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/core"
	"github.com/pusyc74-prog/mangoman/internal/pyenv"
	"github.com/pusyc74-prog/mangoman/internal/setup"
)

// The setup page (ui/setup.html): step 1 connects a key with the
// dashboard's own POST /mangoman/keys; step 2, "Get ready", shows every
// download's size first and then installs in the background while the page
// shows progress; step 3 opens the dashboard or the coding screen.

type readyState struct {
	mu          sync.Mutex
	running     bool
	what        string
	done, total int64
	err         string
}

// ReadyView is what the setup page shows.
type ReadyView struct {
	Parts    []setup.Part `json:"parts"`
	Total    int64        `json:"total"` // bytes, all parts
	Ready    bool         `json:"ready"` // nothing left to download
	Keys     int          `json:"keys"`  // cloud providers connected
	Running  bool         `json:"running"`
	Step     string       `json:"step,omitempty"`
	Done     int64        `json:"done,omitempty"` // bytes of this step's download
	Of       int64        `json:"of,omitempty"`
	Error    string       `json:"error,omitempty"`
	Projects string       `json:"projects"` // where the coding screen opens
}

func (s *Server) readyRoutes(mux *http.ServeMux) {
	mux.Handle("GET /mangoman/ready", s.auth(http.HandlerFunc(s.readyGet)))
	mux.Handle("POST /mangoman/ready", s.auth(http.HandlerFunc(s.readyStart)))
	mux.Handle("POST /mangoman/code/open", s.auth(http.HandlerFunc(s.codeOpen)))
}

func projectsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "MangoMan Projects")
}

func (s *Server) readyGet(w http.ResponseWriter, _ *http.Request) {
	home, err := config.Dir()
	if err != nil {
		core.WriteError(w, http.StatusInternalServerError, "no_home", err.Error())
		return
	}
	// Worked out each time: something may have been installed meanwhile
	// (mangoman ready in a terminal).
	parts := setup.ReadyParts(home)
	if parts == nil {
		parts = []setup.Part{}
	}
	st := &s.ready
	st.mu.Lock()
	v := ReadyView{Parts: parts, Ready: len(parts) == 0, Running: st.running, Step: st.what,
		Done: st.done, Of: st.total, Error: st.err, Projects: projectsDir()}
	st.mu.Unlock()
	for _, p := range v.Parts {
		v.Total += p.Bytes
	}
	for _, p := range s.Router.Cat.AllProviders() {
		if p.NeedsKey && s.Router.HasKey(p) {
			v.Keys++
		}
	}
	writeJSON(w, v)
}

func (s *Server) readyStart(w http.ResponseWriter, r *http.Request) {
	home, err := config.Dir()
	if err != nil {
		core.WriteError(w, http.StatusInternalServerError, "no_home", err.Error())
		return
	}
	st := &s.ready
	st.mu.Lock()
	if st.running {
		st.mu.Unlock()
		core.WriteError(w, http.StatusConflict, "already_running", "Get ready is already running")
		return
	}
	st.running, st.err, st.what, st.done, st.total = true, "", "Starting", 0, 0
	st.mu.Unlock()
	install := s.GetReady
	if install == nil {
		install = setup.Ready
	}
	go func() {
		err := install(home, func(what string, done, total int64) {
			st.mu.Lock()
			st.what, st.done, st.total = what, done, total
			st.mu.Unlock()
		})
		if err == nil {
			// This router starts pack work too (agents): from now on with
			// the new Python first in PATH.
			pyenv.Use(home)
		}
		st.mu.Lock()
		defer st.mu.Unlock()
		st.running = false
		if err != nil {
			st.err = err.Error()
		}
	}()
	s.readyGet(w, r)
}

// codeOpen starts the coding screen (mangoman code --ui) in the projects
// folder, made if missing. It opens in the browser by itself; one that
// stops within a few seconds failed, and its last words are the answer.
func (s *Server) codeOpen(w http.ResponseWriter, _ *http.Request) {
	dir := projectsDir()
	if dir == "" {
		core.WriteError(w, http.StatusInternalServerError, "no_folder", "could not find your home folder")
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "no_folder", err.Error())
		return
	}
	self, err := os.Executable()
	if err != nil {
		core.WriteError(w, http.StatusInternalServerError, "no_program", err.Error())
		return
	}
	var out bytes.Buffer
	cmd := exec.Command(self, "code", "--ui")
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, &out, &out
	if err := cmd.Start(); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "not_started", err.Error())
		return
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		msg := strings.TrimSpace(out.String())
		if i := strings.LastIndex(msg, "\n"); i >= 0 {
			msg = msg[i+1:]
		}
		if msg == "" && err != nil {
			msg = err.Error()
		}
		core.WriteError(w, http.StatusInternalServerError, "not_started", "the coding screen did not start: "+msg)
	case <-time.After(4 * time.Second):
		writeJSON(w, map[string]string{"folder": dir})
	}
}
