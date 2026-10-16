package ingress

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/opencode"
	"github.com/pusyc74-prog/mangoman/internal/pyenv"
	"github.com/pusyc74-prog/mangoman/internal/skills"
)

// The setup page's Get ready: sizes before anything downloads, progress
// while it runs, one run at a time, a plain error, and a second try.
func TestGetReady(t *testing.T) {
	t.Setenv("MANGOMAN_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // no OpenCode of the user's own
	big := strings.Repeat("x", 61_000_000/1000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "61000000")
		if r.Method == http.MethodGet {
			w.Write([]byte(big))
		}
	}))
	defer srv.Close()
	old := opencode.Releases
	opencode.Releases = srv.URL + "/"
	defer func() { opencode.Releases = old }()

	h, s := dashServer(t, mem{})
	release, fails := make(chan struct{}), true
	s.GetReady = func(home string, step pyenv.Step) error {
		step("Downloading the coding helper", 30, 61)
		<-release
		if fails {
			return errors.New("could not reach the download site; check the internet connection and try again")
		}
		return nil
	}
	get := func() ReadyView {
		t.Helper()
		w := call(h, "GET", "/mangoman/ready", "127.0.0.1:4141", authz, "")
		var v ReadyView
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != 200 {
			t.Fatalf("%d %v %s", w.Code, err, w.Body)
		}
		return v
	}
	v := get()
	if len(v.Parts) != 3 || v.Parts[0].Bytes != 61_000_000 || v.Total <= v.Parts[0].Bytes || v.Ready || v.Keys != 0 || v.Running {
		t.Fatalf("before: %+v", v)
	}
	if w := call(h, "POST", "/mangoman/ready", "127.0.0.1:4141", authz, ""); w.Code != 200 {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for v = get(); v.Of == 0 && time.Now().Before(deadline); v = get() {
		time.Sleep(10 * time.Millisecond)
	}
	if !v.Running || v.Step != "Downloading the coding helper" || v.Done != 30 || v.Of != 61 {
		t.Fatalf("while running: %+v", v)
	}
	if w := call(h, "POST", "/mangoman/ready", "127.0.0.1:4141", authz, ""); w.Code != http.StatusConflict {
		t.Fatalf("a second run at once: %d", w.Code)
	}
	release <- struct{}{}
	for v = get(); v.Running && time.Now().Before(deadline); v = get() {
		time.Sleep(10 * time.Millisecond)
	}
	if v.Running || !strings.Contains(v.Error, "internet connection") || len(v.Parts) != 3 {
		t.Fatalf("after a failure: %+v", v)
	}
	fails = false
	call(h, "POST", "/mangoman/ready", "127.0.0.1:4141", authz, "")
	release <- struct{}{}
	for v = get(); v.Running && time.Now().Before(deadline); v = get() {
		time.Sleep(10 * time.Millisecond)
	}
	if v.Running || v.Error != "" {
		t.Fatalf("a second try: %+v", v)
	}
	for _, path := range []string{"/mangoman/ready", "/mangoman/code/open", "/mangoman/files/open"} {
		if w := call(h, "POST", path, "127.0.0.1:4141", nil, ""); w.Code != 401 {
			t.Errorf("%s without the token: %d", path, w.Code)
		}
	}
}

// The Super Agents and Templates lists match the real packs: every pack
// named exists, every pack has one template, and a card that can start
// has a first message naming its packs.
func TestStartersMatchPacks(t *testing.T) {
	data, err := fs.ReadFile(uiFiles, "ui/starters.json")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Agents []struct {
			ID, Title, Summary, Prompt string
			File, Accept               string
			FilePrompt                 string `json:"file_prompt"`
			Gets, Needs, Packs         []string
			Super                      bool
			Stages                     []string
			Brief                      []struct {
				ID, Label, Kind string
				Options         []string
			}
		}
		Later     []struct{ Title, Summary, Why string }
		Templates []struct{ Pack, Title, Summary, Prompt, Dev string }
	}
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(string(data), '—') {
		t.Error("an em dash in starters.json")
	}
	packs, err := skills.List()
	if err != nil {
		t.Fatal(err)
	}
	have, tools := map[string]bool{}, map[string]bool{}
	for _, p := range packs {
		have[p.Name] = true
		tools[p.Name] = p.Kind == "pack"
	}
	for _, a := range d.Agents {
		if a.ID == "" || a.Title == "" || a.Summary == "" || len(a.Gets) == 0 || len(a.Needs) == 0 || len(a.Packs) == 0 {
			t.Errorf("agent %q is missing a field", a.Title)
		}
		for _, p := range a.Packs {
			if !have[p] || !strings.Contains(a.Prompt, p) {
				t.Errorf("agent %q: pack %s missing, or not named in its first message", a.Title, p)
			}
		}
		// A super agent starts from its brief: the form's questions are
		// well formed and its first message names the brief's folder.
		if a.Super {
			ids := map[string]bool{}
			for _, q := range a.Brief {
				choice := q.Kind == "choice" || q.Kind == "multi"
				if !slug.MatchString(strings.ReplaceAll(q.ID, "_", "-")) || q.Label == "" || ids[q.ID] || choice != (len(q.Options) > 0) ||
					(!choice && q.Kind != "text" && q.Kind != "long") {
					t.Errorf("agent %q: brief question %q is wrong", a.Title, q.ID)
				}
				ids[q.ID] = true
			}
			if !slug.MatchString(a.ID) || len(a.Stages) == 0 || len(a.Brief) == 0 || !strings.Contains(a.Prompt, "{folder}/brief.json") {
				t.Errorf("super agent %q needs an id, stages, a brief and {folder}/brief.json in its first message", a.Title)
			}
		}
		// An agent given a file: its own first message names the file, the
		// folder and every pack, and only kinds the upload takes are offered.
		if a.File != "" {
			if !strings.Contains(a.FilePrompt, "{file}") || !strings.Contains(a.FilePrompt, "{folder}") {
				t.Errorf("agent %q: file_prompt must name {file} and {folder}", a.Title)
			}
			for _, p := range a.Packs {
				if !strings.Contains(a.FilePrompt, p) {
					t.Errorf("agent %q: file_prompt does not name %s", a.Title, p)
				}
			}
			for _, ext := range strings.Split(a.Accept, ",") {
				if !uploadable[ext] {
					t.Errorf("agent %q offers %s, which the upload refuses", a.Title, ext)
				}
			}
		}
	}
	for _, l := range d.Later {
		if l.Title == "" || l.Why == "" {
			t.Errorf("coming-later card %q says no reason", l.Title)
		}
	}
	seen := map[string]bool{}
	for _, tp := range d.Templates {
		if !have[tp.Pack] || seen[tp.Pack] || (tp.Prompt == "") == (tp.Dev == "") || (tp.Prompt != "" && !strings.Contains(tp.Prompt, tp.Pack)) {
			t.Errorf("template %q (%s) is wrong", tp.Title, tp.Pack)
		}
		seen[tp.Pack] = true
	}
	for p := range have {
		if tools[p] && !seen[p] {
			t.Errorf("pack %s has no template", p)
		}
	}
}

// My Files lists the projects folder newest first, and opens only a direct
// entry of it, never a path or a link out of it.
func TestFilesListAndOpen(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	projects := filepath.Join(home, "MangoMan Projects")
	for i, name := range []string{"old-task", "new-task"} {
		if err := os.MkdirAll(filepath.Join(projects, name), 0o755); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(time.Duration(i-2) * time.Hour)
		if err := os.Chtimes(filepath.Join(projects, name), when, when); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(projects, ".DS_Store"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	h, _ := dashServer(t, mem{})
	w := call(h, "GET", "/mangoman/files", "127.0.0.1:4141", authz, "")
	var got struct {
		Folder string
		Items  []ProjectItem
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 {
		t.Fatalf("%d %v %s", w.Code, err, w.Body)
	}
	if len(got.Items) != 2 || got.Items[0].Name != "new-task" || !got.Items[0].Dir || entry(projects, "new-task") == "" {
		t.Fatalf("items: %+v", got.Items)
	}
	if err := os.WriteFile(filepath.Join(projects, "run.bat"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	bad := map[string]int{
		`{"name": "../secret"}`: 404, `{"name": ".."}`: 404, `{"name": "a/b"}`: 404, `{"name": "a\\b"}`: 404,
		`{"name": "missing"}`: 404, `{"name": "run.bat"}`: 404, `{"name": "NEW-TASK"}`: 404, `{"other": 1}`: 400,
	}
	// A link out of the folder (Windows needs extra rights to make one).
	if err := os.Symlink(t.TempDir(), filepath.Join(projects, "link")); err == nil {
		bad[`{"name": "link"}`] = 404
	}
	for body, want := range bad {
		if w := call(h, "POST", "/mangoman/files/open", "127.0.0.1:4141", authz, body); w.Code != want {
			t.Errorf("%s: %d, want %d (%s)", body, w.Code, want, w.Body)
		}
	}
}

// The coding screen's workspace is reused: a second click, or an agent's
// Start, while one is open in MangoMan Projects or still starting, starts
// nothing new.
func TestCodeOpenReuses(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	h, s := dashServer(t, mem{})
	projects := filepath.Join(home, "MangoMan Projects")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	for name, c := range map[string]struct {
		set  func()
		code int
	}{
		"open":     {func() { s.code.b, s.code.starting = &codeBackend{dir: projects}, time.Time{} }, 200},
		"starting": {func() { s.code.b, s.code.starting = nil, time.Now() }, 200},
		// A developer's own workspace is never taken over.
		"another folder": {func() { s.code.b, s.code.starting = &codeBackend{dir: repo}, time.Time{} }, 409},
	} {
		c.set()
		w := call(h, "POST", "/mangoman/code/open", "127.0.0.1:4141", authz, "")
		if w.Code != c.code || (c.code == 200) != strings.Contains(w.Body.String(), "MangoMan Projects") {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
}

// A file picked on an agent's card lands in a task folder named after it,
// inside MangoMan Projects, whatever the name it was sent with.
func TestFilesUpload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("MANGOMAN_HOME", t.TempDir())
	h, _ := dashServer(t, mem{})
	projects := filepath.Join(home, "MangoMan Projects")
	w := call(h, "POST", "/mangoman/files/upload?name="+url.QueryEscape("Ozonics Updated Feed.xlsx"), "127.0.0.1:4141", authz, "sheet bytes")
	var got struct{ Path, Folder string }
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	want := filepath.Join(projects, "ozonics-updated-feed", "Ozonics Updated Feed.xlsx")
	if b, err := os.ReadFile(want); err != nil || string(b) != "sheet bytes" || got.Path != want || got.Folder != filepath.Dir(want) {
		t.Fatalf("saved %+v: %v", got, err)
	}
	// A name with folders in it stays inside its task folder.
	w = call(h, "POST", "/mangoman/files/upload?name="+url.QueryEscape(`..\..\evil.csv`), "127.0.0.1:4141", authz, "x")
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || !strings.HasPrefix(got.Path, projects+string(filepath.Separator)) || filepath.Base(got.Path) != "evil.csv" {
		t.Fatalf("%d %+v", w.Code, got)
	}
	for _, bad := range []string{"run.exe", "script.ps1", ".env", "", "noext"} {
		if w := call(h, "POST", "/mangoman/files/upload?name="+url.QueryEscape(bad), "127.0.0.1:4141", authz, "x"); w.Code != 400 {
			t.Fatalf("%q: %d", bad, w.Code)
		}
	}
}
