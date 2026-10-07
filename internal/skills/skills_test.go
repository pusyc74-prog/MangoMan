package skills

import (
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestPacksAreValid(t *testing.T) {
	packs, err := List()
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, p := range packs {
		names = append(names, p.Name)
		if p.Version == "" {
			t.Errorf("%s: no metadata version", p.Name)
		}
	}
	if len(names) < 8 || !sort.StringsAreSorted(names) || !strings.Contains(strings.Join(names, ","), "mangoman-resume") {
		t.Fatalf("packs %v", names)
	}
}

func TestInstallUpdateRemove(t *testing.T) {
	dir := t.TempDir()
	// A user's own skill with a clashing name must never be overwritten.
	mine := filepath.Join(dir, "mangoman-resume")
	os.MkdirAll(mine, 0o755)
	os.WriteFile(filepath.Join(mine, "SKILL.md"), []byte("mine"), 0o644)
	got, skipped, err := Install(dir)
	if err != nil {
		t.Fatal(err)
	}
	all, _ := List()
	if len(got) != len(all)-1 || len(skipped) != 1 || skipped[0] != mine {
		t.Fatalf("installed %v skipped %v", got, skipped)
	}
	if b, _ := os.ReadFile(filepath.Join(mine, "SKILL.md")); string(b) != "mine" {
		t.Fatal("user's own skill overwritten")
	}
	deck := filepath.Join(dir, "mangoman-ceo-deck")
	for _, f := range []string{"SKILL.md", "scripts/build_deck.py", "scripts/vizlib.py", "scripts/render.py", "scripts/tracenum.py", "scripts/brandkit.py", Marker} {
		if _, err := os.Stat(filepath.Join(deck, f)); err != nil {
			t.Fatalf("missing %s", f)
		}
	}
	// Test cases and the scorer stay out of reach of the model.
	if _, err := os.Stat(filepath.Join(deck, "tests")); err == nil {
		t.Fatal("tests installed with the pack")
	}
	cases := t.TempDir()
	if err := Tests("mangoman-resume", cases); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cases, "score.py")); err != nil {
		t.Fatal("Tests did not write the scorer")
	}
	// Updating replaces our own folders.
	os.WriteFile(filepath.Join(deck, "stale.txt"), []byte("x"), 0o644)
	if _, _, err := Install(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(deck, "stale.txt")); err == nil {
		t.Fatal("update kept a stale file")
	}
	removed, err := Remove(dir)
	if err != nil || len(removed) != len(all)-1 {
		t.Fatalf("removed %v %v", removed, err)
	}
	if _, err := os.Stat(mine); err != nil {
		t.Fatal("remove deleted the user's own skill")
	}
}

// The Python scripts must at least compile and import on the test machine.
func TestPythonScriptsCompile(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	dir := t.TempDir()
	if _, _, err := Install(dir); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*", "scripts", "*.py"))
	args := append([]string{"-m", "py_compile"}, files...)
	if out, err := exec.Command(py, args...).CombinedOutput(); err != nil {
		t.Fatalf("python compile: %v\n%s", err, out)
	}
	csv := filepath.Join(dir, "d.csv")
	os.WriteFile(csv, []byte("region,amount\nSouth,10\nsouth ,5\nNorth,7\n"), 0o644)
	out, err := exec.Command(py, filepath.Join(dir, "mangoman-data-dashboard", "scripts", "profile.py"), csv).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "same value written differently") {
		t.Fatalf("profile: %v\n%s", err, out)
	}
}

func TestParseSkillAcceptsWindowsLineEndings(t *testing.T) {
	p, err := ParseSkill([]byte("---\r\nname: demo-skill\r\ndescription: A demo.\r\nmetadata:\r\n  version: \"1.0\"\r\n---\r\n# Demo\r\n"))
	if err != nil || p.Name != "demo-skill" || p.Version != "1.0" {
		t.Fatalf("got %+v, %v", p, err)
	}
}

// Every pack with a public test set has a scorer and cases with a prompt, so
// mangoman agents eval can run it.
func TestTestSetsAreComplete(t *testing.T) {
	packs, err := List()
	if err != nil {
		t.Fatal(err)
	}
	sets := 0
	for _, p := range packs {
		root := path.Join("packs", p.Name, "tests")
		if _, err := fs.Stat(packsFS, root); err != nil {
			continue
		}
		sets++
		if _, err := fs.Stat(packsFS, path.Join(root, "score.py")); err != nil {
			t.Errorf("%s: tests without score.py", p.Name)
		}
		cases, _ := fs.ReadDir(packsFS, path.Join(root, "cases"))
		if len(cases) < 3 {
			t.Errorf("%s: %d test cases, want at least 3", p.Name, len(cases))
		}
		for _, c := range cases {
			if _, err := fs.Stat(packsFS, path.Join(root, "cases", c.Name(), "prompt.txt")); err != nil {
				t.Errorf("%s/%s: no prompt.txt", p.Name, c.Name())
			}
		}
	}
	if sets < 6 {
		t.Errorf("%d packs have test sets, want at least 6", sets)
	}
}
