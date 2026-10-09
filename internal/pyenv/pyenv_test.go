package pyenv

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every system with a uv pin has measured package sizes, and the other way
// round, so the setup page never shows a size it does not know.
func TestPinsAndSizesMatch(t *testing.T) {
	var all map[string]struct{ Packages, Playwright int64 }
	if err := json.Unmarshal(sizesJSON, &all); err != nil {
		t.Fatal(err)
	}
	for sys, p := range pins {
		s, ok := all[sys]
		if !ok || s.Packages <= s.Playwright || s.Playwright <= 0 || p.uv <= 0 || p.python <= 0 || len(p.sum) != 64 {
			t.Errorf("%s: pin %+v, sizes %+v", sys, p, s)
		}
	}
	for sys := range all {
		if _, ok := pins[sys]; !ok {
			t.Errorf("%s has sizes but no uv pin", sys)
		}
	}
}

// The lock holds every package Imports needs, each pinned with checksums.
func TestLockCoversImports(t *testing.T) {
	pinned := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(requirements))
	for sc.Scan() {
		l := sc.Text()
		if name, _, ok := strings.Cut(l, "=="); ok && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "#") {
			pinned[strings.ToLower(name)] = true
		}
	}
	for _, pkg := range []string{"pillow", "python-pptx", "python-docx", "pypdf", "openpyxl", "pandas", "playwright"} {
		if !pinned[pkg] {
			t.Errorf("%s is not in requirements.txt", pkg)
		}
	}
	if !bytes.Contains(requirements, []byte("--hash=sha256:")) {
		t.Error("requirements.txt has no checksums")
	}
}

func TestInstalledAndPath(t *testing.T) {
	home := t.TempDir()
	if Installed(home) || Check(home) == nil {
		t.Fatal("nothing is installed yet")
	}
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("PYTHONUTF8", "")
	Use(home)
	if os.Getenv("PATH") != "/usr/bin" || os.Getenv("PYTHONUTF8") != "1" {
		t.Fatal("PATH changed before the Python was installed")
	}
	os.MkdirAll(root(home), 0o700)
	os.WriteFile(filepath.Join(root(home), "installed"), []byte(stamp()), 0o600)
	if !Installed(home) {
		t.Fatal("not seen as installed")
	}
	Use(home)
	if got := os.Getenv("PATH"); !strings.HasPrefix(got, Bin(home)+string(os.PathListSeparator)) {
		t.Fatalf("PATH = %q", got)
	}
	// An install from an older pin or lock counts as not installed.
	os.WriteFile(filepath.Join(root(home), "installed"), []byte("0.1 3.11 abc"), 0o600)
	if Installed(home) {
		t.Fatal("an older install counts as current")
	}
}
