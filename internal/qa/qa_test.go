package qa

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetect(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) { os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644) }
	if len(Detect(dir)) != 0 {
		t.Fatal("empty folder has no tests")
	}
	write("Makefile", "build:\n\ttrue\ntest:\n\ttrue\n")
	if s := Detect(dir); len(s) != 1 || s[0].Name != "make" {
		t.Fatalf("make: %+v", s)
	}
	write("go.mod", "module x\n")
	write("package.json", `{"scripts":{"test":"echo \"Error: no test specified\" && exit 1"}}`)
	write("pyproject.toml", "[tool.pytest.ini_options]\n")
	var names []string
	for _, s := range Detect(dir) {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "Go,pytest" {
		t.Fatalf("got %v (npm's placeholder script and make must be left out)", names)
	}
}

func TestRunTestsAndReport(t *testing.T) {
	res := RunTests(context.Background(), t.TempDir(), []Suite{
		{"ok", []string{"go", "env", "GOOS"}},
		{"broken", []string{"go", "tool", "no-such-tool"}},
	})
	if !res[0].OK || res[1].OK || res[1].Output == "" {
		t.Fatalf("results: %+v", res)
	}
	found := []Finding{{Page: "http://x/", Kind: "console_error", Detail: "boom", Steps: []string{"Open http://x/"}}}
	rep := Report(res, []string{"http://x/"}, found, "Probably the build.")
	for _, want := range []string{"1 of 2 suites passed", "## broken tests FAILED", "## Bug 1: console error on http://x/", "1. Open http://x/", "Probably the build."} {
		if !strings.Contains(rep, want) {
			t.Fatalf("report missing %q:\n%s", want, rep)
		}
	}
	if p := Problems(res, found); !strings.Contains(p, "broken tests failed") || !strings.Contains(p, "boom") {
		t.Fatalf("problems: %s", p)
	}
}

func TestCrawl(t *testing.T) {
	if exec.Command("python3", "-c", "import playwright").Run() != nil {
		t.Skip("needs Python with Playwright")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`<html><body><a href="/two">two</a><a href="https://elsewhere.invalid/">out</a>
<script>console.error("cart failed to load")</script></body></html>`))
	})
	mux.HandleFunc("/two", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`<html><body><div style="width:900px">wide</div><img src="/missing.png"></body></html>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	visited, found, err := Crawl(context.Background(), srv.URL+"/#token=secret", 5, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(visited) != 2 || strings.Contains(strings.Join(visited, " "), "secret") {
		t.Fatalf("visited %v", visited)
	}
	kinds := map[string]bool{}
	for _, f := range found {
		kinds[f.Kind] = true
		if len(f.Steps) < 2 || f.Shot == "" {
			t.Fatalf("finding without steps or screenshot: %+v", f)
		}
	}
	for _, k := range []string{"console_error", "http_error", "broken_image", "phone_overflow"} {
		if !kinds[k] {
			t.Fatalf("missing %s in %+v", k, found)
		}
	}
}
