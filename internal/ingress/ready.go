package ingress

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
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
	mux.Handle("GET /mangoman/files", s.auth(http.HandlerFunc(s.filesList)))
	mux.Handle("POST /mangoman/files/open", s.auth(http.HandlerFunc(s.filesOpen)))
	mux.Handle("POST /mangoman/files/upload", s.auth(http.HandlerFunc(s.filesUpload)))
}

// projects returns the projects folder, made if missing, or writes the error.
func projects(w http.ResponseWriter) string {
	dir := ProjectsDir()
	if dir == "" {
		core.WriteError(w, http.StatusInternalServerError, "no_folder", "could not find your home folder")
		return ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "no_folder", err.Error())
		return ""
	}
	return dir
}

// openable are the files My Files opens in their own app; anything else,
// say a script the model wrote, is opened from its folder by hand.
var openable = map[string]bool{".pdf": true, ".png": true, ".jpg": true, ".jpeg": true, ".svg": true, ".html": true,
	".md": true, ".txt": true, ".csv": true, ".json": true, ".docx": true, ".pptx": true, ".xlsx": true}

// entry is the path of name in dir when name is exactly one of dir's
// entries and a real folder or an openable file (not a link or a junction),
// else "".
func entry(dir, name string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.Name() != name {
			continue
		}
		path := filepath.Join(dir, name)
		fi, err := os.Lstat(path)
		if err == nil && (fi.IsDir() || fi.Mode().IsRegular() && openable[strings.ToLower(filepath.Ext(name))]) {
			return path
		}
	}
	return ""
}

// sameDir reports whether two paths are the same folder.
func sameDir(a, b string) bool {
	x, err := os.Stat(a)
	if err != nil {
		return false
	}
	y, err := os.Stat(b)
	return err == nil && os.SameFile(x, y)
}

// ProjectItem is one entry of the projects folder: a task's folder, or a
// file saved there before tasks had folders.
type ProjectItem struct {
	Name     string    `json:"name"`
	Dir      bool      `json:"dir"`
	Modified time.Time `json:"modified"`
}

// filesList is My Files: what is in the projects folder, newest first.
func (s *Server) filesList(w http.ResponseWriter, _ *http.Request) {
	dir := projects(w)
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		core.WriteError(w, http.StatusInternalServerError, "no_folder", err.Error())
		return
	}
	items := []ProjectItem{}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || strings.HasPrefix(e.Name(), ".") || e.Name() == "desktop.ini" {
			continue
		}
		items = append(items, ProjectItem{Name: e.Name(), Dir: e.IsDir(), Modified: info.ModTime()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Modified.After(items[j].Modified) })
	writeJSON(w, map[string]any{"folder": dir, "items": items})
}

// filesOpen shows the projects folder, or one entry of it ({"name"}), in
// Explorer, Finder or the Linux file manager; a document opens in its own
// app. Only an exact entry of the folder can be named (see entry).
func (s *Server) filesOpen(w http.ResponseWriter, r *http.Request) {
	dir := projects(w)
	if dir == "" {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if r.ContentLength != 0 {
		if err := readJSON(r, &in); err != nil {
			core.WriteError(w, http.StatusBadRequest, "bad_request", "send nothing, or {\"name\"}")
			return
		}
	}
	target := dir
	if in.Name != "" {
		if target = entry(dir, in.Name); target == "" {
			core.WriteError(w, http.StatusNotFound, "not_found", "open a folder, or a document such as a PDF or image, from the projects folder")
			return
		}
	}
	open := setup.OpenURL // Finder (open) or the Linux file manager (xdg-open)
	if runtime.GOOS == "windows" {
		open = func(dir string) error { return exec.Command("explorer", dir).Start() }
	}
	if err := open(target); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "not_opened", err.Error())
		return
	}
	writeJSON(w, map[string]string{"folder": dir})
}

// uploadable are the kinds of file an agent can be given from the dashboard.
var uploadable = map[string]bool{".xlsx": true, ".xls": true, ".csv": true, ".tsv": true, ".json": true, ".txt": true,
	".pdf": true, ".docx": true, ".pptx": true, ".png": true, ".jpg": true, ".jpeg": true, ".md": true}

const maxUpload = 50 << 20

var notWord = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// filesUpload puts a file the user picked on an agent's card into a task
// folder in MangoMan Projects, named after the file, so the agent starts with
// it instead of asking for a path (owner, 15 Oct: the data analyst asked
// three times for the file's path). The file's name comes as ?name=, its
// bytes as the body.
func (s *Server) filesUpload(w http.ResponseWriter, r *http.Request) {
	dir := projects(w)
	if dir == "" {
		return
	}
	name := filepath.Base(strings.ReplaceAll(r.URL.Query().Get("name"), "\\", "/"))
	ext := strings.ToLower(filepath.Ext(name))
	if name == "." || name == "/" || strings.HasPrefix(name, ".") || !uploadable[ext] {
		core.WriteError(w, http.StatusBadRequest, "bad_file", "Choose a spreadsheet, document, PDF or picture.")
		return
	}
	task := strings.Trim(notWord.ReplaceAllString(strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name))), "-"), "-")
	if task == "" {
		task = "upload"
	}
	folder := filepath.Join(dir, task)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	path := filepath.Join(folder, name)
	f, err := os.CreateTemp(folder, ".upload-*")
	if err != nil {
		core.WriteError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	_, err = io.Copy(f, http.MaxBytesReader(w, r.Body, maxUpload))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
		core.WriteError(w, http.StatusBadRequest, "not_saved", "The file was not saved (at most 50 MB): "+err.Error())
		return
	}
	writeJSON(w, map[string]string{"path": path, "folder": folder})
}

// ProjectsDir is where the coding screen opens from the setup page and the
// dashboard, one folder per task inside it.
func ProjectsDir() string {
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
		Done: st.done, Of: st.total, Error: st.err, Projects: ProjectsDir()}
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

// codeOpen starts the coding screen's workspace (mangoman code --ui) in the
// projects folder, made if missing; the page that asked opens the coding
// screen itself, which waits for the workspace. One that stops within a few
// seconds failed, and its last words are the answer. A workspace already
// open there, or still starting, is used as it is: every click starting
// another left OpenCode servers running unseen (found 15 Oct).
func (s *Server) codeOpen(w http.ResponseWriter, _ *http.Request) {
	dir := projects(w)
	if dir == "" {
		return
	}
	s.code.mu.Lock()
	var other string // a workspace someone opened in another folder
	if s.code.b != nil && !sameDir(s.code.b.dir, dir) {
		other = s.code.b.dir
	}
	open := s.code.b != nil && other == ""
	starting := s.code.b == nil && time.Since(s.code.starting) < 90*time.Second
	if !open && !starting && other == "" {
		s.code.starting = time.Now()
	}
	s.code.mu.Unlock()
	if other != "" {
		core.WriteError(w, http.StatusConflict, "other_workspace", "a coding workspace is open in "+other+" (from mangoman code --ui): close it there, then try again")
		return
	}
	if open || starting {
		writeJSON(w, map[string]string{"folder": dir})
		return
	}
	self, err := os.Executable()
	if err != nil {
		core.WriteError(w, http.StatusInternalServerError, "no_program", err.Error())
		return
	}
	var out bytes.Buffer
	cmd := exec.Command(self, "code", "--ui", "--no-browser")
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, &out, &out
	if err := cmd.Start(); err != nil {
		s.code.mu.Lock()
		s.code.starting = time.Time{}
		s.code.mu.Unlock()
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
		s.code.mu.Lock()
		s.code.starting = time.Time{}
		s.code.mu.Unlock()
		core.WriteError(w, http.StatusInternalServerError, "not_started", "the coding screen did not start: "+msg)
	case <-time.After(4 * time.Second):
		// A workspace that stops before attaching frees the next click at once.
		go func() {
			<-exited
			s.code.mu.Lock()
			if s.code.b == nil {
				s.code.starting = time.Time{}
			}
			s.code.mu.Unlock()
		}()
		writeJSON(w, map[string]string{"folder": dir})
	}
}
