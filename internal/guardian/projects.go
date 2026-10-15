package guardian

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// Unregister takes a guardian.json path off the list. The file stays.
func Unregister(list, config string) {
	paths := slices.DeleteFunc(Projects(list), func(p string) bool { return p == config })
	b, _ := json.Marshal(paths)
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

// Save writes a guardian.json and keeps Guardian's state (.guardian/, with
// incidents and the Telegram token) out of git next to it.
func Save(path string, c *Config) error {
	ignore := filepath.Join(filepath.Dir(path), ".gitignore")
	if old, _ := os.ReadFile(ignore); !strings.Contains(string(old), ".guardian/") {
		f, err := os.OpenFile(ignore, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, err = f.WriteString(".guardian/\n")
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(c)
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// Hosting finds how the project in dir deploys, from its files: Vercel,
// Netlify or Docker Compose. Empty when it cannot tell.
func Hosting(dir string) (dev, prod Env) {
	has := func(n string) bool { _, err := os.Stat(filepath.Join(dir, n)); return err == nil }
	switch {
	case has("vercel.json") || has(".vercel"):
		dev = Env{Deploy: "npx vercel deploy --yes"} // prints the dev address
		prod = Env{Deploy: "npx vercel deploy --prod --yes", Rollback: "npx vercel rollback --yes"}
	case has("netlify.toml"):
		dev = Env{Deploy: "npx netlify deploy --alias dev"} // prints the dev address
		prod = Env{Deploy: "npx netlify deploy --prod"}
	case has("docker-compose.yml") || has("compose.yaml"):
		dev = Env{Deploy: "docker compose -p " + ProjectName(dir) + "-dev up -d --build", URL: "http://127.0.0.1:8081/"}
		prod = Env{Deploy: "docker compose -p " + ProjectName(dir) + " up -d --build"}
	}
	return dev, prod
}

// ProjectName is a folder or app name made safe for commands and folders.
func ProjectName(dir string) string {
	return strings.ToLower(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, filepath.Base(dir)))
}
