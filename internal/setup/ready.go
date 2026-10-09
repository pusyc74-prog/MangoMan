package setup

import (
	"fmt"

	"github.com/pusyc74-prog/mangoman/internal/opencode"
	"github.com/pusyc74-prog/mangoman/internal/pyenv"
)

// Part is one download "Get ready" makes, with its size in bytes (0 when
// it could not be found out).
type Part struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
	Note  string `json:"note,omitempty"`
}

// NeedsReady reports whether Ready has anything to do, without asking the
// network.
func NeedsReady(home string) bool { return needsOpenCode(home) || !pyenv.Installed(home) }

// needsOpenCode reports whether OpenCode is missing, or MangoMan's own copy
// is not the tested version.
func needsOpenCode(home string) bool {
	_, err := opencode.Path(home)
	return err != nil || opencode.Outdated(home)
}

// ReadyParts lists what Ready would download on this computer; empty when
// everything is in place. The sizes are shown before anything is downloaded.
func ReadyParts(home string) []Part {
	var out []Part
	if needsOpenCode(home) {
		out = append(out, Part{Name: "Coding helper (OpenCode)", Bytes: opencode.Size()})
	}
	if !pyenv.Installed(home) {
		s, _ := pyenv.SizesHere()
		out = append(out,
			Part{Name: "Python " + pyenv.PythonVersion, Bytes: s.UV + s.Python, Note: "with its installer, uv"},
			Part{Name: "Packages for the skill packs", Bytes: s.Packages,
				Note: fmt.Sprintf("Playwright alone is %s; it uses your Edge or Chrome, so no browser is downloaded", MB(s.Playwright))})
	}
	return out
}

// MB shows a size the way the setup page and the terminal do.
func MB(n int64) string {
	if n <= 0 {
		return "size unknown"
	}
	return fmt.Sprintf("%.0f MB", float64(n)/1e6)
}

// Ready downloads and installs whatever ReadyParts lists. step is told
// what is happening (nil: nothing is told).
func Ready(home string, step pyenv.Step) error {
	if step == nil {
		step = func(string, int64, int64) {}
	}
	if needsOpenCode(home) {
		if _, err := opencode.Install(home, func(d, t int64) { step("Downloading the coding helper", d, t) }); err != nil {
			return fmt.Errorf("could not install the coding helper: %w", err)
		}
	}
	if !pyenv.Installed(home) {
		if err := pyenv.Install(home, step); err != nil {
			return fmt.Errorf("could not set up Python: %w", err)
		}
	}
	step("Ready", 0, 0)
	return nil
}
