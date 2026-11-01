package ingress

import (
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/core"
	"github.com/pusyc74-prog/mangoman/internal/setup"
)

// The files a step of the coding chat made, handed over in the chat: the
// agent writes them itself or a pack's script builds them (a resume PDF, a
// website's HTML), so the chat lists what changed in the workspace's folder
// during the step. Only documents My Files would open (not .json, the
// agents' working data), only inside the workspace's folder, never through
// a link or a junction.

const (
	maxStepFiles = 12       // newest first: a step that wrote more is a build, see Changes
	maxWalked    = 20000    // entries looked at, so a huge folder cannot stall the chat
	maxFileSize  = 50 << 20 // as an upload
)

// skipDirs are folders of tools, not of documents.
var skipDirs = map[string]bool{"node_modules": true, "__pycache__": true, "venv": true}

// StepFile is one document a step made or changed.
type StepFile struct {
	Path     string    `json:"path"` // inside the workspace's folder, with "/"
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

func handedOver(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return openable[ext] && ext != ".json"
}

// codeFiles lists the documents changed since ?since= (Unix milliseconds).
func (s *Server) codeFiles(w http.ResponseWriter, r *http.Request) {
	b := s.backend()
	if b == nil {
		core.WriteError(w, http.StatusServiceUnavailable, "no_workspace", "no coding workspace is open")
		return
	}
	ms, err := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	if err != nil {
		core.WriteError(w, http.StatusBadRequest, "bad_request", "send ?since= in Unix milliseconds")
		return
	}
	writeJSON(w, map[string]any{"files": stepFiles(b.dir, time.UnixMilli(ms))})
}

func stepFiles(dir string, since time.Time) []StepFile {
	files, walked := []StepFile{}, 0
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real // a walk does not enter a folder reached through a link
	}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if walked++; walked > maxWalked {
			return fs.SkipAll
		}
		if err != nil {
			return nil // a folder that cannot be read is passed over
		}
		name := d.Name()
		if d.IsDir() {
			if path != dir && (strings.HasPrefix(name, ".") || skipDirs[name]) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || strings.HasPrefix(name, ".") || !handedOver(name) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.ModTime().Before(since) {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err == nil {
			files = append(files, StepFile{Path: filepath.ToSlash(rel), Size: info.Size(), Modified: info.ModTime()})
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Modified.After(files[j].Modified) })
	if len(files) > maxStepFiles {
		files = files[:maxStepFiles]
	}
	return files
}

// stepFile is the real folder of the workspace and the full path of rel in
// it when rel is a document there, reached only through plain folders: no
// link and no Windows junction (Go reports a junction as irregular, and
// filepath.EvalSymlinks does not follow it), else an error.
func stepFile(dir, rel string) (root, path string, err error) {
	bad := errors.New("not a document in this workspace (or over 50 MB)")
	rel = filepath.FromSlash(rel)
	if !filepath.IsLocal(rel) || !handedOver(rel) {
		return "", "", bad
	}
	if root, err = filepath.EvalSymlinks(dir); err != nil {
		return "", "", err
	}
	path = root
	parts := strings.Split(rel, string(filepath.Separator))
	for i, part := range parts {
		path = filepath.Join(path, part)
		fi, err := os.Lstat(path)
		last := i == len(parts)-1
		if err != nil || last && (!fi.Mode().IsRegular() || fi.Size() > maxFileSize) || !last && !fi.IsDir() ||
			fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return "", "", bad
		}
	}
	return root, path, nil
}

// codeFile sends one document (?path=) for the chat to save.
func (s *Server) codeFile(w http.ResponseWriter, r *http.Request) {
	b := s.backend()
	if b == nil {
		core.WriteError(w, http.StatusServiceUnavailable, "no_workspace", "no coding workspace is open")
		return
	}
	root, path, err := stepFile(b.dir, r.URL.Query().Get("path"))
	if err != nil {
		core.WriteError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	// Read inside the folder only: a link put in between the check and
	// the read cannot lead out of it.
	rel, err := filepath.Rel(root, path)
	if err != nil {
		core.WriteError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	fsRoot, err := os.OpenRoot(root)
	if err != nil {
		core.WriteError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	defer fsRoot.Close()
	f, err := fsRoot.Open(rel)
	if err != nil {
		core.WriteError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		core.WriteError(w, http.StatusNotFound, "not_found", "not a document in this workspace")
		return
	}
	kind := mime.TypeByExtension(filepath.Ext(path))
	if kind == "" {
		kind = "application/octet-stream"
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Content-Disposition", "attachment") // saved, never shown from MangoMan's address
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

// codeFileOpen opens one document ({"path"}) in its own app on this
// computer, or with {"folder": true} shows it in its folder (on Windows,
// opens the folder: Explorer's select option breaks on a path with a space).
func (s *Server) codeFileOpen(w http.ResponseWriter, r *http.Request) {
	b := s.backend()
	if b == nil {
		core.WriteError(w, http.StatusServiceUnavailable, "no_workspace", "no coding workspace is open")
		return
	}
	var in struct {
		Path   string `json:"path"`
		Folder bool   `json:"folder"`
	}
	if err := readJSON(r, &in); err != nil {
		core.WriteError(w, http.StatusBadRequest, "bad_request", "send {\"path\"}")
		return
	}
	_, path, err := stepFile(b.dir, in.Path)
	if err != nil {
		core.WriteError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	open := setup.OpenURL
	switch {
	case in.Folder && runtime.GOOS == "darwin":
		open = func(p string) error { return exec.Command("open", "-R", p).Start() }
	case in.Folder:
		path = filepath.Dir(path)
	}
	if runtime.GOOS == "windows" { // Explorer opens a folder, or a document in its own app
		open = func(p string) error { return exec.Command("explorer", p).Start() }
	}
	if err := open(path); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "not_opened", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
