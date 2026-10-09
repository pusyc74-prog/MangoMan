// Package pyenv gives the skill packs a Python of MangoMan's own, so users
// never install Python, run pip or download a browser engine. uv (Astral,
// MIT or Apache-2.0) is downloaded pinned and checked like OpenCode; it then
// installs Python and the packages in requirements.txt (every one pinned
// with its checksums) into MangoMan's folder.
package pyenv

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pusyc74-prog/mangoman/internal/download"
)

// The tested releases. Moving one forward means testing it first, then
// updating it here (uv's checksums are on its release page) or running
// scripts/lock-python.sh (packages).
const (
	UVVersion     = "0.12.24"
	PythonVersion = "3.12.15"
)

// Imports is what the packs need; Check runs it.
const Imports = "import PIL, pptx, docx, pypdf, openpyxl, pandas, playwright"

var releases = "https://github.com/astral-sh/uv/releases/download/" + UVVersion + "/"

type pin struct {
	asset, sum string
	uv, python int64 // download sizes in bytes, measured
}

// pins are uv's download, checksum and size, and the size of the Python
// uv downloads, per system (GOOS-GOARCH, as in sizes.json).
var pins = map[string]pin{
	"windows-amd64": {"uv-x86_64-pc-windows-msvc.zip", "7c38608c8a18ee137d748a1773053b07ec8f3a30fab49aebaa6f4e4efeceb019", 15685515, 22011023},
	"darwin-arm64":  {"uv-aarch64-apple-darwin.tar.gz", "0c4346de7abdb49495b393b9ec809fe387aa43e586be20fecb972216c1e71732", 14650202, 25013243},
	"darwin-amd64":  {"uv-x86_64-apple-darwin.tar.gz", "4fa82e37cb94767661f532b001e470b67a186c7260e305bd84ddb78fd545c0b6", 18120254, 24733148},
	"linux-amd64":   {"uv-x86_64-unknown-linux-gnu.tar.gz", "b4dfaef47d491a7296981f8374a4595f55dbf84e8937c8ecd2983574d8bb3da6", 17249236, 34285590},
	"linux-arm64":   {"uv-aarch64-unknown-linux-gnu.tar.gz", "5231be65f496304623895dacdbf1de8504fec90303684bdf05805aa34414dd21", 16478994, 29215308},
}

//go:embed requirements.txt
var requirements []byte

//go:embed sizes.json
var sizesJSON []byte

func system() string { return runtime.GOOS + "-" + runtime.GOARCH }

// Sizes is what Install downloads on this computer, in bytes.
type Sizes struct {
	UV, Python, Packages, Playwright int64 // Playwright is part of Packages
}

// Total is everything Install downloads.
func (s Sizes) Total() int64 { return s.UV + s.Python + s.Packages }

// SizesHere returns the download sizes for this computer; ok is false
// where MangoMan has no tested Python.
func SizesHere() (Sizes, bool) {
	p, ok := pins[system()]
	if !ok {
		return Sizes{}, false
	}
	var all map[string]struct{ Packages, Playwright int64 }
	if json.Unmarshal(sizesJSON, &all) != nil {
		return Sizes{}, false
	}
	pk, ok := all[system()]
	return Sizes{UV: p.uv, Python: p.python, Packages: pk.Packages, Playwright: pk.Playwright}, ok
}

func root(home string) string { return filepath.Join(home, "python") }

// Bin is the folder put first in PATH for everything MangoMan starts. It
// holds only python3, the name every pack runs, so the python and pip of a
// user's own projects stay theirs. Python finds the environment from it
// through env/pyvenv.cfg, one folder up.
func Bin(home string) string { return filepath.Join(root(home), "env", "shim") }

// envBin is the environment's own folder of programs.
func envBin(home string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(root(home), "env", "Scripts")
	}
	return filepath.Join(root(home), "env", "bin")
}

// Supported reports whether MangoMan has a tested Python for this computer.
func Supported() bool { _, ok := pins[system()]; return ok }

// stamp names what is installed, so a newer pin or lock installs again.
func stamp() string {
	return fmt.Sprintf("%s %s %x", UVVersion, PythonVersion, sha256.Sum256(requirements))
}

// Installed reports whether the private Python of this MangoMan is there
// (not whether it works: see Check).
func Installed(home string) bool {
	b, err := os.ReadFile(filepath.Join(root(home), "installed"))
	return err == nil && strings.TrimSpace(string(b)) == stamp()
}

// Check runs the private python3 and imports what the packs need. The error
// says in plain words what to do.
func Check(home string) error {
	if !Supported() {
		return fmt.Errorf("MangoMan has no Python for %s yet: skill packs need Python 3 with the packages in requirements.txt", system())
	}
	if !Installed(home) {
		return errors.New("MangoMan's Python is not set up yet: click Get ready in the dashboard, or run mangoman ready")
	}
	py := filepath.Join(Bin(home), exe("python3"))
	if out, err := exec.Command(py, "-c", Imports).CombinedOutput(); err != nil {
		return fmt.Errorf("MangoMan's Python is broken (%s): run mangoman ready again", lastLine(out, err))
	}
	return nil
}

// lastLine is the last line of a program's output (where Python and uv put
// the error), else the error itself.
func lastLine(out []byte, err error) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if l := strings.TrimSpace(lines[len(lines)-1]); l != "" {
		return l
	}
	return err.Error()
}

// offline reports whether uv's output says the network could not be reached.
func offline(out string) bool {
	out = strings.ToLower(out)
	for _, s := range []string{"dns error", "failed to lookup address", "error sending request", "connection refused", "timed out"} {
		if strings.Contains(out, s) {
			return true
		}
	}
	return false
}

func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// Step tells the caller what Install is doing: a short line, and for a
// download the bytes done and expected (0 when not a download).
type Step func(what string, done, total int64)

// Install downloads uv, then has it install Python and the packages into
// home/python. It replaces an older install. step may be nil.
func Install(home string, step Step) error {
	if step == nil {
		step = func(string, int64, int64) {}
	}
	p, ok := pins[system()]
	if !ok {
		return fmt.Errorf("MangoMan has no tested Python for %s yet", system())
	}
	dir := root(home)
	data, err := download.Fetch(releases+p.asset, p.sum, 64<<20, func(d, t int64) { step("Downloading uv", d, t) })
	if err != nil {
		return err
	}
	prog, err := download.Program(p.asset, data, "uv", 128<<20)
	if err != nil {
		return err
	}
	// Only now, with uv in hand, a half-finished or older install is
	// removed, so a failed download never costs a Python that worked.
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("could not replace the old Python (is a pack still running?): %w", err)
	}
	uv := filepath.Join(dir, exe("uv"))
	if err := download.Save(uv, prog); err != nil {
		return err
	}
	req := filepath.Join(dir, "requirements.txt")
	if err := os.WriteFile(req, requirements, 0o600); err != nil {
		return err
	}
	env := filepath.Join(dir, "env")
	run := func(what string, args ...string) error {
		step(what, 0, 0)
		cmd := exec.Command(uv, args...)
		cmd.Env = append(os.Environ(),
			"UV_PYTHON_INSTALL_DIR="+filepath.Join(dir, "versions"),
			"UV_CACHE_DIR="+filepath.Join(dir, "cache"),
			"UV_PYTHON_PREFERENCE=only-managed", // never a Python the user has
			"UV_NO_CONFIG=1",                    // nor their uv settings
			"VIRTUAL_ENV=",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			why := lastLine(out, err)
			if offline(string(out)) {
				why = "could not reach the download site; check the internet connection and try again"
			}
			return fmt.Errorf("%s failed: %s", strings.ToLower(what[:1])+what[1:], why)
		}
		return nil
	}
	if err := run("Downloading Python "+PythonVersion, "venv", "--python", PythonVersion, env); err != nil {
		return err
	}
	if err := run("Downloading the packages for the skill packs", "pip", "install", "--python", env, "--require-hashes", "-r", req); err != nil {
		return err
	}
	if err := shim(home); err != nil {
		return err
	}
	_ = os.RemoveAll(filepath.Join(dir, "cache"))
	step("Checking it works", 0, 0)
	if out, err := exec.Command(filepath.Join(Bin(home), exe("python3")), "-c", Imports).CombinedOutput(); err != nil {
		return fmt.Errorf("the new Python does not work: %s", lastLine(out, err))
	}
	return os.WriteFile(filepath.Join(dir, "installed"), []byte(stamp()), 0o600)
}

// Use sets up this process so every program MangoMan starts (OpenCode,
// pack scripts, agents) gets the private python3 first in PATH, once it is
// installed, and Python's UTF-8 mode: on Windows, Python otherwise prints
// through the old code page, which has no ₹, and a script that prints a
// rupee amount stops with an error (seen in the Windows check, 9 Oct).
func Use(home string) {
	_ = os.Setenv("PYTHONUTF8", "1")
	first := Bin(home) + string(os.PathListSeparator)
	if path := os.Getenv("PATH"); Installed(home) && !strings.HasPrefix(path, first) {
		_ = os.Setenv("PATH", first+path)
	}
}

// shim makes Bin: python3 alone. On Windows a copy of the environment's
// python.exe (a launcher that reads pyvenv.cfg one folder up), elsewhere a
// link to the environment's python3.
func shim(home string) error {
	if err := os.MkdirAll(Bin(home), 0o700); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		b, err := os.ReadFile(filepath.Join(envBin(home), "python.exe"))
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(Bin(home), "python3.exe"), b, 0o700)
	}
	return os.Symlink(filepath.Join("..", "bin", "python3"), filepath.Join(Bin(home), "python3"))
}
