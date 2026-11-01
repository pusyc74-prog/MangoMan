package ingress

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The chat hands over the documents a step made: listed since the step
// began, sent for saving, and never anything outside the workspace's
// folder, a link, a hidden file or the agents' working data.
func TestCodeStepFiles(t *testing.T) {
	h, _ := dashServer(t, mem{})
	host := "127.0.0.1:4141"
	if w := call(h, "GET", "/mangoman/code/files?since=0", host, authz, ""); w.Code != 503 {
		t.Fatalf("no workspace: %d", w.Code)
	}
	dir := t.TempDir()
	write := func(rel, body string, age time.Duration) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		_ = os.Chtimes(p, at, at)
	}
	write("old.pdf", "old", time.Hour)
	write("council/decision.md", "# Decision", 0)
	write("site/index.html", "<h1>Hi</h1>", 0)
	write("council/council.json", "{}", 0)      // working data
	write(".hidden/x.html", "x", 0)             // a dot folder
	write("node_modules/pkg/readme.md", "x", 0) // a tool's folder
	write("build.py", "print(1)", 0)            // a script, not a document
	outside := filepath.Join(t.TempDir(), "secret.pdf")
	_ = os.WriteFile(outside, []byte("secret"), 0o644)
	linked := os.Symlink(outside, filepath.Join(dir, "link.pdf")) == nil

	body, _ := json.Marshal(map[string]string{"url": "http://127.0.0.1:1", "password": "x", "dir": dir})
	if w := call(h, "POST", "/mangoman/code/attach", host, authz, string(body)); w.Code != 200 {
		t.Fatalf("attach: %d %s", w.Code, w.Body)
	}
	since := strconv.FormatInt(time.Now().Add(-time.Minute).UnixMilli(), 10)
	w := call(h, "GET", "/mangoman/code/files?since="+since, host, authz, "")
	var got struct{ Files []StepFile }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
		t.Fatalf("files: %d %s", w.Code, w.Body)
	}
	var names []string
	for _, f := range got.Files {
		names = append(names, f.Path)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "council/decision.md,site/index.html" {
		t.Fatalf("this step's documents only: %v", names)
	}
	if w := call(h, "GET", "/mangoman/code/files?since=x", host, authz, ""); w.Code != 400 {
		t.Fatalf("a bad since: %d", w.Code)
	}

	// Sent for saving, as an attachment.
	w = call(h, "GET", "/mangoman/code/file?path="+url.QueryEscape("site/index.html"), host, authz, "")
	if w.Code != 200 || w.Body.String() != "<h1>Hi</h1>" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") ||
		!strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("send: %d %v %s", w.Code, w.Header(), w.Body)
	}
	if w := call(h, "GET", "/mangoman/code/file?path=site/index.html", host, nil, ""); w.Code != 401 {
		t.Fatalf("no token: %d", w.Code)
	}
	bad := []string{"../" + filepath.Base(filepath.Dir(outside)) + "/secret.pdf", outside, "council/council.json", "build.py", "missing.pdf", "", "site"}
	if linked {
		bad = append(bad, "link.pdf")
	}
	for _, p := range bad {
		if w := call(h, "GET", "/mangoman/code/file?path="+url.QueryEscape(p), host, authz, ""); w.Code != 404 {
			t.Fatalf("%q must not be sent: %d %s", p, w.Code, w.Body)
		}
		if w := call(h, "POST", "/mangoman/code/file/open", host, authz, `{"path":`+strconv.Quote(p)+`}`); w.Code != 404 {
			t.Fatalf("%q must not be opened: %d", p, w.Code)
		}
	}
}
