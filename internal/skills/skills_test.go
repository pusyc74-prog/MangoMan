package skills

import (
	"os"
	"os/exec"
	"path/filepath"
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
	if strings.Join(names, ",") != "mangoman-ceo-deck,mangoman-data-dashboard,mangoman-landing-page,mangoman-resume,mangoman-social-posts" {
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
	if len(got) != 4 || len(skipped) != 1 || skipped[0] != mine {
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
	// Updating replaces our own folders.
	os.WriteFile(filepath.Join(deck, "stale.txt"), []byte("x"), 0o644)
	if _, _, err := Install(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(deck, "stale.txt")); err == nil {
		t.Fatal("update kept a stale file")
	}
	removed, err := Remove(dir)
	if err != nil || len(removed) != 4 {
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
