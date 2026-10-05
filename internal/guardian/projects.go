package guardian

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
)

// The projects list (in MangoMan's folder) tells the dashboard which
// guardian.json files exist on this computer, so approvals can be given
// there too.

// Register adds a guardian.json path to the list.
func Register(list, config string) {
	paths := Projects(list)
	if slices.Contains(paths, config) {
		return
	}
	b, _ := json.Marshal(append(paths, config))
	_ = os.MkdirAll(filepath.Dir(list), 0o700)
	_ = os.WriteFile(list, b, 0o600)
}

// Projects returns the registered guardian.json paths that still exist.
func Projects(list string) []string {
	var paths, out []string
	if b, err := os.ReadFile(list); err == nil {
		_ = json.Unmarshal(b, &paths)
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// OpenProject loads a project's incidents without wiring an agent: enough to show
// them.
func OpenProject(config string) (*Guardian, error) {
	cfg, err := Load(config)
	if err != nil {
		return nil, err
	}
	return &Guardian{Cfg: cfg, Dir: filepath.Join(filepath.Dir(config), ".guardian")}, nil
}
