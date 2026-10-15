package ingress

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/core"
	"github.com/pusyc74-prog/mangoman/internal/guardian"
	"github.com/pusyc74-prog/mangoman/internal/qa"
)

// The Guardian page connects an app to Guardian and keeps
// watching it while MangoMan is open. An app with only a live address is
// watched: up, answering, fast enough, with the likely cause when it is
// not. An app whose code is a Git project on this computer is also fixed:
// Guardian works in a dev copy and asks before anything goes live.

// healState is the apps this MangoMan watches right now.
type healState struct {
	mu     sync.Mutex
	ctx    context.Context // ends when MangoMan stops; nil before StartHealing
	on     map[string]*watcher
	failed map[string]string // why a watch stopped on its own
}

type watcher struct {
	stop context.CancelFunc
	done chan struct{} // closed when Guardian has stopped
}

func watchList() string {
	d, _ := config.Dir()
	return filepath.Join(d, "guardian", "watch.json")
}

// StartHealing watches every app connected on the Guardian page,
// until ctx ends. It does nothing without Watch.
func (s *Server) StartHealing(ctx context.Context) {
	s.heal.mu.Lock()
	s.heal.ctx = ctx
	s.heal.mu.Unlock()
	for _, p := range guardian.Projects(watchList()) {
		s.watch(p)
	}
}

func (s *Server) watch(config string) {
	s.heal.mu.Lock()
	defer s.heal.mu.Unlock()
	if s.Watch == nil || s.heal.ctx == nil || s.heal.on[config] != nil {
		return
	}
	ctx, stop := context.WithCancel(s.heal.ctx)
	w := &watcher{stop: stop, done: make(chan struct{})}
	if s.heal.on == nil {
		s.heal.on, s.heal.failed = map[string]*watcher{}, map[string]string{}
	}
	delete(s.heal.failed, config)
	s.heal.on[config] = w
	go func() {
		defer close(w.done)
		err := s.Watch(ctx, config)
		failed := err != nil && ctx.Err() == nil
		stop()
		s.heal.mu.Lock()
		if s.heal.on[config] == w {
			delete(s.heal.on, config)
		}
		if failed {
			s.heal.failed[config] = err.Error()
		}
		s.heal.mu.Unlock()
	}()
}

// unwatch stops watching an app and waits up to a minute for Guardian to
// finish the step under way (a deploy runs to its end); false if it is
// still busy.
func (s *Server) unwatch(config string) bool {
	s.heal.mu.Lock()
	w := s.heal.on[config] // stays listed until it has stopped
	s.heal.mu.Unlock()
	if w == nil {
		return true
	}
	w.stop()
	select {
	case <-w.done:
		return true
	case <-time.After(time.Minute):
		return false
	}
}

func (s *Server) watching(config string) bool {
	s.heal.mu.Lock()
	defer s.heal.mu.Unlock()
	return s.heal.on[config] != nil
}

// stopped is why a watch ended on its own, or "".
func (s *Server) stopped(config string) string {
	s.heal.mu.Lock()
	defer s.heal.mu.Unlock()
	return s.heal.failed[config]
}

// healConnect connects an app: a name, its live address and, for fixes,
// the folder with its code.
func (s *Server) healConnect(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name    string `json:"name"`
		URL     string `json:"url"`
		Folder  string `json:"folder"`
		Builder string `json:"builder"`
	}
	if err := readJSON(r, &in); err != nil {
		core.WriteError(w, http.StatusBadRequest, "bad_request", "send name, url and folder")
		return
	}
	in.Name, in.URL, in.Folder = strings.TrimSpace(in.Name), strings.TrimSpace(in.URL), strings.TrimSpace(in.Folder)
	if in.Name == "" || len(in.Name) > 60 {
		core.WriteError(w, http.StatusBadRequest, "bad_name", "Give the app a short name.")
		return
	}
	u, err := url.Parse(in.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		core.WriteError(w, http.StatusBadRequest, "bad_url", "Give the full address, starting with https://")
		return
	}
	if len(in.Builder) > 40 {
		core.WriteError(w, http.StatusBadRequest, "bad_builder", "Name the tool in a few words.")
		return
	}
	// Up and fast every run; every page in a browser and the certificate
	// and domain dates once a day.
	cfg := &guardian.Config{App: in.Name, Builder: strings.TrimSpace(in.Builder), Prod: guardian.Env{URL: u.String()},
		Checks: []guardian.Check{{Name: "site", URL: u.String(), MaxMS: 5000},
			{Name: "pages", Browse: u.String(), Every: "24h"}, {Name: "dates", Expiry: u.String(), Every: "24h"}}}
	var path string
	if in.Folder == "" {
		d, _ := config.Dir()
		name := guardian.ProjectName(in.Name)
		if strings.Trim(name, "-") == "" { // a name in another script
			name = fmt.Sprintf("app-%08x", crc32.ChecksumIEEE([]byte(in.Name)))
		}
		dir := filepath.Join(d, "guardian", "apps", name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			core.WriteError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		path = filepath.Join(dir, "guardian.json")
	} else {
		dir := in.Folder
		if !filepath.IsAbs(dir) {
			dir = entry(ProjectsDir(), dir) // a folder picked from the Projects list
		}
		if st, err := os.Stat(filepath.Join(dir, ".git")); dir == "" || err != nil || !st.IsDir() {
			core.WriteError(w, http.StatusBadRequest, "not_git",
				"That folder is not a Git project. Fixes need the code in Git; leave the folder empty to watch the address only.")
			return
		}
		path = filepath.Join(dir, "guardian.json")
		cfg.Repo = "."
		cfg.ProdBranch = liveBranch(dir)
		if cfg.ProdBranch == "" {
			core.WriteError(w, http.StatusBadRequest, "no_branch", "Could not tell which branch is live. Switch the folder to its main branch and try again.")
			return
		}
		// Guardian's safe copy is the dev branch. One with work of its own
		// would go live with the first approved fix.
		if exec.Command("git", "-C", dir, "rev-parse", "--verify", "--quiet", "dev").Run() == nil &&
			exec.Command("git", "-C", dir, "merge-base", "--is-ancestor", "dev", cfg.ProdBranch).Run() != nil {
			core.WriteError(w, http.StatusBadRequest, "dev_branch",
				"This project has a branch called dev with changes that are not live. Guardian uses dev for its safe copy: rename that branch first.")
			return
		}
		var prod guardian.Env
		cfg.Dev, prod = guardian.Hosting(dir)
		if cfg.Dev.URL != "" {
			cfg.Dev = guardian.Env{} // Docker: a second stack would clash with production's ports
		}
		cfg.Prod.Deploy, cfg.Prod.Rollback = prod.Deploy, prod.Rollback
	}
	// A project's own guardian.json is kept as it is: the owner wrote it.
	_, err = os.Stat(path)
	kept := err == nil && in.Folder != ""
	if !kept {
		if err := guardian.Save(path, cfg); err != nil {
			core.WriteError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
	}
	if _, err := guardian.Load(path); err != nil {
		core.WriteError(w, http.StatusBadRequest, "bad_config", err.Error())
		return
	}
	guardian.Register(projectsList(), path)
	guardian.Register(watchList(), path)
	// A reconnect starts again with the new settings, once the old watch
	// has stopped: two at once would work on the same incidents.
	if !s.unwatch(path) {
		core.WriteError(w, http.StatusConflict, "busy", "Guardian is still finishing a step for this app. Try again in a minute.")
		return
	}
	s.watch(path)
	branch := cfg.ProdBranch
	if kept {
		branch = "" // the project's own guardian.json says
	}
	writeJSON(w, map[string]any{"config": path, "watching": s.watching(path), "kept": kept, "branch": branch})
}

// liveBranch is the branch production runs from: main or master when there
// is one, else the branch the folder is on.
func liveBranch(dir string) string {
	for _, b := range []string{"main", "master"} {
		if exec.Command("git", "-C", dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+b).Run() == nil {
			return b
		}
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if b := strings.TrimSpace(string(out)); err == nil && b != "HEAD" {
		return b
	}
	return ""
}

// healDisconnect stops watching an app. Its files stay where they are.
func (s *Server) healDisconnect(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Config string `json:"config"`
	}
	if err := readJSON(r, &in); err != nil || in.Config == "" {
		core.WriteError(w, http.StatusBadRequest, "bad_request", "send config")
		return
	}
	if !slices.Contains(guardian.Projects(projectsList()), in.Config) {
		core.WriteError(w, http.StatusNotFound, "not_found", "no such app on this computer")
		return
	}
	s.unwatch(in.Config)
	guardian.Unregister(watchList(), in.Config)
	guardian.Unregister(projectsList(), in.Config)
	writeJSON(w, map[string]string{"status": "disconnected"})
}

// journeyPrompt turns the owner's words into steps a browser can follow.
const journeyPrompt = `Turn this description of a path through a website into steps for a browser test.
Reply with only a JSON array. Each step is one of:
{"go": "/path"} to open a page of the site
{"click": "visible text of a button or link"}
{"fill": "label or placeholder of a field", "with": "text to type"}
{"see": "text that must appear"}
Rules: at most 12 steps; end with a "see" step that proves the path worked; use made-up test data such as test@example.com; never pay, place a real order or send a real message, stop at the last step before that and check what is shown. Do not invent text the description does not suggest.

Site: %s
Description: %s`

// healJourney adds a journey to an app: the owner describes it in plain
// words, a model writes the steps, and they are tried once right away.
func (s *Server) healJourney(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Config string `json:"config"`
		Text   string `json:"text"`
	}
	if err := readJSON(r, &in); err != nil || in.Config == "" || strings.TrimSpace(in.Text) == "" || len(in.Text) > 600 {
		core.WriteError(w, http.StatusBadRequest, "bad_request", "Describe the path in a sentence or two.")
		return
	}
	if !slices.Contains(guardian.Projects(projectsList()), in.Config) {
		core.WriteError(w, http.StatusNotFound, "not_found", "no such app on this computer")
		return
	}
	cfg, err := guardian.Load(in.Config)
	if err != nil {
		core.WriteError(w, http.StatusBadRequest, "bad_config", err.Error())
		return
	}
	if s.Ask == nil {
		core.WriteError(w, http.StatusServiceUnavailable, "no_model", "Journeys need a model: connect a provider first.")
		return
	}
	ans, err := s.Ask(fmt.Sprintf(journeyPrompt, cfg.Prod.URL, strings.TrimSpace(in.Text)))
	if err != nil {
		core.WriteError(w, http.StatusBadGateway, "model_failed", "No AI model answered. Check Connections has a working provider, then try again.")
		return
	}
	steps, err := parseSteps(ans, cfg.Prod.URL)
	if err != nil {
		core.WriteError(w, http.StatusBadGateway, "bad_steps", "Could not turn that into steps ("+err.Error()+"). Try saying it step by step.")
		return
	}
	name := "journey: " + strings.TrimSpace(in.Text)
	if r := []rune(name); len(r) > 60 {
		name = string(r[:57]) + "..."
	}
	for _, k := range cfg.Checks {
		if k.Name == name {
			core.WriteError(w, http.StatusConflict, "exists", "That journey is already watched.")
			return
		}
	}
	why, err := qa.Journey(r.Context(), cfg.Prod.URL, steps, filepath.Join(filepath.Dir(in.Config), ".guardian", "shots", "journey-try.png"))
	if err != nil {
		core.WriteError(w, http.StatusServiceUnavailable, "no_browser", "Could not open a browser to try it: "+err.Error())
		return
	}
	cfg.Checks = append(cfg.Checks, guardian.Check{Name: name, Journey: steps, Every: "1h"})
	if err := guardian.Save(in.Config, cfg); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.restart(in.Config)
	say := make([]string, len(steps))
	for i, st := range steps {
		say[i] = st.Say()
	}
	writeJSON(w, map[string]any{"name": name, "steps": say, "works": why == "", "problem": why})
}

// healRemove takes a journey off an app.
func (s *Server) healRemove(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Config string `json:"config"`
		Name   string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil || in.Config == "" || in.Name == "" {
		core.WriteError(w, http.StatusBadRequest, "bad_request", "send config and name")
		return
	}
	if !slices.Contains(guardian.Projects(projectsList()), in.Config) {
		core.WriteError(w, http.StatusNotFound, "not_found", "no such app on this computer")
		return
	}
	cfg, err := guardian.Load(in.Config)
	if err != nil {
		core.WriteError(w, http.StatusBadRequest, "bad_config", err.Error())
		return
	}
	n := len(cfg.Checks)
	cfg.Checks = slices.DeleteFunc(cfg.Checks, func(k guardian.Check) bool { return k.Name == in.Name && len(k.Journey) > 0 })
	if len(cfg.Checks) == n {
		core.WriteError(w, http.StatusNotFound, "not_found", "no such journey")
		return
	}
	if err := guardian.Save(in.Config, cfg); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	s.restart(in.Config)
	writeJSON(w, map[string]string{"status": "removed"})
}

// restart picks up a changed guardian.json when the app is watched here:
// the old watch finishes its step, then a new one starts (unless the app
// was disconnected meanwhile).
func (s *Server) restart(config string) {
	s.heal.mu.Lock()
	w := s.heal.on[config]
	s.heal.mu.Unlock()
	if w == nil {
		return
	}
	w.stop()
	go func() {
		<-w.done
		if slices.Contains(guardian.Projects(watchList()), config) {
			s.watch(config)
		}
	}()
}

// parseSteps reads the model's JSON steps, with or without a code fence.
func parseSteps(ans, base string) ([]qa.Step, error) {
	start, end := strings.Index(ans, "["), strings.LastIndex(ans, "]")
	if start < 0 || end < start {
		return nil, fmt.Errorf("no list of steps")
	}
	var steps []qa.Step
	dec := json.NewDecoder(strings.NewReader(ans[start : end+1]))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&steps); err != nil {
		return nil, err
	}
	if len(steps) == 0 || len(steps) > 12 {
		return nil, fmt.Errorf("%d steps; 1 to 12 expected", len(steps))
	}
	return steps, qa.CheckSteps(base, steps)
}
