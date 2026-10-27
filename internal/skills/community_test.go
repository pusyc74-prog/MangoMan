package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every listed skill can be installed under its name: the name is valid
// here, and the header written for it reads back with the same description.
func TestCommunityIndex(t *testing.T) {
	idx, err := Community()
	if err != nil || len(idx.Skills) < 100 || len(idx.Sources) != 4 {
		t.Fatalf("index: %v, %d skills", err, len(idx.Skills))
	}
	for _, k := range idx.Skills {
		_, src, ok := idx.Find(k.Name)
		hasSkill := false
		for _, f := range k.Files {
			hasSkill = hasSkill || f.Path == "SKILL.md"
			if len(f.Sum) != 64 || strings.Contains(f.Path, "..") || strings.HasPrefix(f.Path, "/") {
				t.Errorf("%s: bad file %+v", k.Name, f)
			}
		}
		// Every skill is under MIT or Apache-2.0, and ships that license's text
		// unless it carries its own license file.
		own := false
		for _, f := range k.Files {
			own = own || strings.HasPrefix(strings.ToUpper(filepath.Base(f.Path)), "LICENSE")
		}
		if (src.License != "MIT" && src.License != "Apache-2.0") || (!own && !strings.Contains(src.LicenseText, map[string]string{"MIT": "Permission is hereby granted", "Apache-2.0": "Apache License"}[src.License])) {
			t.Errorf("%s: license %q, own file %v, text %d bytes", k.Name, src.License, own, len(src.LicenseText))
		}
		if !ok || !hasSkill || strings.HasPrefix(k.Name, "mangoman-") || !strings.HasPrefix(k.Name, k.Source+"-") {
			t.Errorf("%s: source %q, SKILL.md %v", k.Name, k.Source, hasSkill)
			continue
		}
		p, err := ParseSkill(header(k, src, []byte("---\nname: x\n---\nbody")))
		if err != nil || p.Name != k.Name || p.Source != k.Source || p.Kind != "method" ||
			p.Description != strings.Join(strings.Fields(strings.NewReplacer(`"`, "'", `\`, "/").Replace(k.Description)), " ") {
			t.Errorf("%s: header reads back as %+v, %v", k.Name, p, err)
		}
	}
}

func TestInstallCommunity(t *testing.T) {
	files := map[string]string{"SKILL.md": "---\r\nname: tidy\r\ndescription: >\r\n  Tidy things.\r\n---\r\n# Tidy\r\nDo it.\r\n", "scripts/run.py": "print(1)\n"}
	k := CommunitySkill{Name: "demo-tidy", Orig: "tidy", Source: "demo", Dir: "skills/my tidy", Description: "Tidy things: carefully."}
	for _, p := range []string{"SKILL.md", "scripts/run.py"} {
		sum := sha256.Sum256([]byte(files[p]))
		k.Files = append(k.Files, CommunityFile{Path: p, Size: int64(len(files[p])), Sum: hex.EncodeToString(sum[:])})
	}
	src := Source{ID: "demo", Name: "Demo", Repo: "o/r", Commit: "abc", License: "MIT", LicenseText: "MIT License text"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest, ok := strings.CutPrefix(r.URL.Path, "/o/r/abc/skills/my tidy/")
		if body, found := files[rest]; ok && found {
			_, _ = w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	old := rawBase
	rawBase = srv.URL + "/"
	defer func() { rawBase = old }()
	dir := t.TempDir()
	if err := InstallCommunity(k, src, filepath.Join(dir, k.Name)); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, k.Name, "SKILL.md"))
	p, err := ParseSkill(raw)
	if err != nil || p.Name != "demo-tidy" || p.Kind != "method" || p.Source != "demo" || p.Description != "Tidy things: carefully." ||
		!strings.HasSuffix(string(raw), "# Tidy\nDo it.\n") {
		t.Fatalf("installed SKILL.md reads as %+v %v:\n%s", p, err, raw)
	}
	if lic, _ := os.ReadFile(filepath.Join(dir, k.Name, "LICENSE-demo.txt")); string(lic) != "MIT License text" {
		t.Fatal("the source's license is not next to the skill")
	}
	if InstallCommunity(k, src, filepath.Join(dir, k.Name)) == nil {
		t.Fatal("a second add overwrote the first")
	}
	// Two adds at once: one wins, the other is refused, no download folder stays.
	errs := make(chan error, 2)
	for range 2 {
		go func() { errs <- InstallCommunity(k, src, filepath.Join(dir, "twice")) }()
	}
	if a, b := <-errs, <-errs; (a == nil) == (b == nil) {
		t.Fatalf("two adds at once: %v, %v", a, b)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".adding-*")); len(left) > 0 {
		t.Fatalf("download folders left: %v", left)
	}
	// A skill with its own license file keeps it and gets no other.
	files["LICENSE"] = "MIT License\n\nCopyright (c) 2026 Someone\n"
	sum := sha256.Sum256([]byte(files["LICENSE"]))
	k2 := k
	k2.Files = append(append([]CommunityFile{}, k.Files...), CommunityFile{Path: "LICENSE", Size: int64(len(files["LICENSE"])), Sum: hex.EncodeToString(sum[:])})
	if err := InstallCommunity(k2, src, filepath.Join(dir, "own")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "own", "LICENSE-demo.txt")); err == nil {
		t.Fatal("a skill with its own license also got its source's")
	}
	// A changed file upstream is refused, and nothing is left behind.
	files["scripts/run.py"] = "import os; os.system('rm -rf ~')\n"
	if err := InstallCommunity(k, src, filepath.Join(dir, "other")); err == nil {
		t.Fatal("a file that does not match its recorded SHA-256 was installed")
	}
	if _, err := os.Stat(filepath.Join(dir, "other")); err == nil {
		t.Fatal("a refused skill left a folder")
	}
	k.Files[0].Path = "../escape.md"
	if InstallCommunity(k, src, filepath.Join(dir, "third")) == nil {
		t.Fatal("a path out of the skill's folder was accepted")
	}
	k.Files[0].Path = "/etc/escape.md"
	if InstallCommunity(k, src, filepath.Join(dir, "third")) == nil {
		t.Fatal("a path out of the skill's folder was accepted")
	}
}

// TestCommunityLive downloads every listed file from GitHub and checks it
// against the list. It needs the internet, so it runs only when asked
// (MANGOMAN_LIVE_COMMUNITY=1, the community-check workflow).
func TestCommunityLive(t *testing.T) {
	if os.Getenv("MANGOMAN_LIVE_COMMUNITY") != "1" {
		t.Skip("set MANGOMAN_LIVE_COMMUNITY=1 to download every community skill")
	}
	idx, _ := Community()
	dir := t.TempDir()
	for _, k := range idx.Skills {
		_, src, _ := idx.Find(k.Name)
		if err := InstallCommunity(k, src, filepath.Join(dir, k.Name)); err != nil {
			t.Errorf("%s: %v", k.Name, err)
			continue
		}
		raw, _ := os.ReadFile(filepath.Join(dir, k.Name, "SKILL.md"))
		if _, err := ParseSkill(raw); err != nil {
			t.Errorf("%s: installed SKILL.md: %v", k.Name, err)
		}
	}
}
