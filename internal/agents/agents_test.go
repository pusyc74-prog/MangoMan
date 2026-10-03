package agents

import (
	"archive/zip"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const testManifest = `{
  "name": "demo-agent", "version": "1.0.0", "title": "Demo", "description": "A demo agent.",
  "skill": "mangoman-ecommerce-listing", "author": {"name": "Asha", "contact": "asha@example.org"},
  "runs_on": "local", "permissions": {"network": [], "commands": []},
  "models": "free", "price_inr_month": 0, "data_policy": "Nothing leaves your computer."
}`

const testSkill = "---\nname: demo-agent\ndescription: A demo agent for tests.\nmetadata:\n  version: \"1.0\"\n---\n# Demo\n"

func writeAgent(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for n, c := range files {
		p := filepath.Join(dir, n)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func packDemo(t *testing.T, root string, extra map[string]string) (pkg, key string) {
	t.Helper()
	src := filepath.Join(root, "src")
	files := map[string]string{"agent.json": testManifest, "SKILL.md": testSkill, "scripts/run.py": "print('ok')\n"}
	for k, v := range extra {
		files[k] = v
	}
	writeAgent(t, src, files)
	key = filepath.Join(root, "creator.key")
	if _, err := os.Stat(key); err != nil {
		if _, err := Keygen(key); err != nil {
			t.Fatal(err)
		}
	}
	pkg = filepath.Join(root, "demo.mmagent")
	if _, err := Pack(src, key, pkg); err != nil {
		t.Fatal(err)
	}
	return pkg, key
}

func TestPackInstallListRemove(t *testing.T) {
	root := t.TempDir()
	pkg, _ := packDemo(t, root, nil)
	dir := filepath.Join(root, "agents")
	m, err := Install(pkg, dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "demo-agent" {
		t.Fatalf("name %q", m.Name)
	}
	for _, f := range []string{"scripts/run.py", "scripts/checks.py", "SKILL.md"} {
		if _, err := os.Stat(filepath.Join(dir, "demo-agent", f)); err != nil {
			t.Fatalf("%s not installed: %v", f, err)
		}
	}
	list, err := List(dir)
	if err != nil || len(list) != 1 {
		t.Fatalf("list %v %v", list, err)
	}
	if err := Remove(dir, "demo-agent"); err != nil {
		t.Fatal(err)
	}
	if list, _ := List(dir); len(list) != 0 {
		t.Fatal("still listed after remove")
	}
}

func TestTamperedPackageIsRefused(t *testing.T) {
	root := t.TempDir()
	pkg, _ := packDemo(t, root, nil)
	zr, err := zip.OpenReader(pkg)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		rc, _ := f.Open()
		w, _ := zw.Create(f.Name)
		if f.Name == "scripts/run.py" {
			w.Write([]byte("import os; os.system('curl evil')\n"))
		} else {
			b := new(bytes.Buffer)
			b.ReadFrom(rc)
			w.Write(b.Bytes())
		}
		rc.Close()
	}
	zr.Close()
	zw.Close()
	os.WriteFile(pkg, buf.Bytes(), 0o644)
	if _, err := Install(pkg, filepath.Join(root, "agents")); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("tampered package installed: %v", err)
	}
}

func TestUpdateMustComeFromSameCreator(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "agents")
	pkg, _ := packDemo(t, root, nil)
	if _, err := Install(pkg, dir); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	pkg2, _ := packDemo(t, other, nil)
	if _, err := Install(pkg2, dir); err == nil || !strings.Contains(err.Error(), "different creator") {
		t.Fatalf("update from another key accepted: %v", err)
	}
	pkg3, _ := packDemo(t, root, map[string]string{"scripts/more.py": "x = 1\n"})
	if _, err := Install(pkg3, dir); err != nil {
		t.Fatalf("update from the same key refused: %v", err)
	}
}

func TestUnsafePathsAndBadManifests(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("../escape.py")
	w.Write([]byte("x"))
	zw.Close()
	pkg := filepath.Join(root, "bad.mmagent")
	os.WriteFile(pkg, buf.Bytes(), 0o644)
	if _, err := Install(pkg, filepath.Join(root, "agents")); err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("unsafe path accepted: %v", err)
	}
	m := Manifest{Name: "Bad Name", Version: "1", RunsOn: "moon", Models: "gpt"}
	m.Permissions.Network = []string{"https://x.com"}
	m.Permissions.Commands = []string{"/bin/sh"}
	err := m.Validate()
	for _, want := range []string{"name must", "version", "runs_on", "network host", "command", "models", "data_policy"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("missing %q in %v", want, err)
		}
	}
}

func TestGuardBlocksUndeclaredAccess(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	root := t.TempDir()
	pkg, _ := packDemo(t, root, nil)
	dir := filepath.Join(root, "agents")
	if _, err := Install(pkg, dir); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(root, "work")
	os.MkdirAll(work, 0o755)
	home, _ := os.UserHomeDir()
	t.Setenv("GROQ_API_KEY", "secret-value")
	cases := map[string]string{
		"write outside":  "open(" + quote(filepath.Join(root, "outside.txt")) + ", 'w').write('x')",
		"read secrets":   "open(" + quote(filepath.Join(home, ".ssh", "id_rsa")) + ")",
		"network":        "import socket; socket.getaddrinfo('example.com', 443)",
		"other programs": "import subprocess; subprocess.run(['curl', 'https://example.com'])",
		"shell":          "import os; os.system('true')",
		"local services": "import socket; socket.create_connection(('127.0.0.1', 4141), timeout=2)",
	}
	for name, code := range cases {
		cmd, err := Command(dir, "demo-agent", work, []string{py, "-c", code})
		if err != nil {
			t.Fatal(err)
		}
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "not allowed") {
			t.Errorf("%s was not blocked: %v %s", name, err, out)
		}
	}
	cmd, _ := Command(dir, "demo-agent", work, []string{py, "-c",
		"import os; open('ok.txt','w').write(os.environ.get('GROQ_API_KEY','none'))"})
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("allowed write failed: %v %s", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(work, "ok.txt")); string(b) != "none" {
		t.Fatalf("secret env reached the agent: %q", b)
	}
}

func quote(s string) string { return "r'" + s + "'" }

func TestEvalAndVerdict(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	root := t.TempDir()
	writeAgent(t, root, map[string]string{
		"score.py":             "import json,sys; print(json.dumps({'score': float(open(sys.argv[1]+'/out.txt').read()), 'notes': 'ok'}))\n",
		"cases/one/prompt.txt": "Make it.",
		"cases/one/facts.md":   "facts",
		"cases/two/prompt.txt": "Make it again.",
	})
	scores := map[string]string{"pack": "70", "agent": "85"}
	results, err := Eval(root, []string{"pack", "agent"}, filepath.Join(root, "work"), func(c, dir, prompt string) error {
		if !strings.HasPrefix(prompt, "Use the "+c+" skill.") {
			t.Errorf("prompt %q does not name the skill", prompt)
		}
		if _, err := os.Stat(filepath.Join(dir, "prompt.txt")); err != nil {
			t.Error("case files were not copied")
		}
		return os.WriteFile(filepath.Join(dir, "out.txt"), []byte(scores[c]), 0o644)
	})
	if err != nil || len(results) != 2 {
		t.Fatalf("results %v %v", results, err)
	}
	if p, a, beats := Verdict(results, "pack", "agent"); !beats || p != 70 || a != 85 {
		t.Fatalf("verdict %v %v %v", p, a, beats)
	}
	scores["agent"] = "60"
	results, _ = Eval(root, []string{"pack", "agent"}, filepath.Join(root, "work"), func(c, dir, _ string) error {
		return os.WriteFile(filepath.Join(dir, "out.txt"), []byte(scores[c]), 0o644)
	})
	if _, _, beats := Verdict(results, "pack", "agent"); beats {
		t.Fatal("a weaker agent was said to beat the pack")
	}
}

func TestInHouseAgentsAreValid(t *testing.T) {
	dirs, _ := filepath.Glob("../../agents/*")
	if len(dirs) == 0 {
		t.Fatal("no in-house agents found")
	}
	for _, d := range dirs {
		if _, err := Load(d); err != nil {
			t.Errorf("%s: %v", d, err)
		}
	}
}
