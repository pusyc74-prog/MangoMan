// Package config holds local settings. It never contains provider keys.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// DefaultPort is the local endpoint port.
const DefaultPort = 4141

// Config is stored as config.json in the config directory.
type Config struct {
	Port int `json:"port"`
	// Token is the local bearer token clients must send. It only protects the
	// localhost endpoint from other local processes and websites.
	Token string `json:"token"`
	// ExcludedProviders are providers the user turned off. Every provider is
	// on by default.
	ExcludedProviders []string `json:"excluded_providers,omitempty"`
	// PaidFallback stays false unless the user opts in.
	PaidFallback bool `json:"paid_fallback"`
	// MaxAttempts caps candidates tried per request.
	MaxAttempts int `json:"max_attempts,omitempty"`
	// Favorites is "My list": models tried first, in this order, before the
	// router's own ranking. An entry is a model name ("gpt-oss-120b", any
	// provider) or provider/model ("groq/gpt-oss-120b", that provider only).
	Favorites []string `json:"favorites,omitempty"`
	// CustomModels are new models the user added from the radar; they are
	// not in the catalogue yet.
	CustomModels []CustomModel `json:"custom_models,omitempty"`
	// Groups are named sets of models tested as equivalent for a workflow.
	// Asking for model "group/<name>" only ever uses these models, in this
	// order, and never falls back to anything else. Entries use the same
	// form as Favorites.
	Groups map[string][]string `json:"groups,omitempty"`
	// Brain configures the decision brain. It is on by default.
	Brain BrainConfig `json:"brain,omitempty"`
}

// BrainConfig is the decision brain's setting.
type BrainConfig struct {
	Off   bool   `json:"off,omitempty"`
	Model string `json:"model,omitempty"` // engine; empty = free/fast
}

// GetBrain returns the brain setting.
func (c *Config) GetBrain() BrainConfig {
	mu.RLock()
	defer mu.RUnlock()
	return c.Brain
}

// SetBrain replaces the brain setting.
func (c *Config) SetBrain(b BrainConfig) {
	mu.Lock()
	defer mu.Unlock()
	c.Brain = b
}

// CustomModel is a model added by the user from a provider's live list.
type CustomModel struct {
	Provider string `json:"provider"`
	Upstream string `json:"upstream"`
	Added    string `json:"added,omitempty"`
}

var (
	mu     sync.RWMutex // guards fields changed live from the dashboard
	saveMu sync.Mutex   // one Save at a time
)

// GetFavorites returns a copy of My list.
func (c *Config) GetFavorites() []string {
	mu.RLock()
	defer mu.RUnlock()
	return append([]string(nil), c.Favorites...)
}

// SetFavorites replaces My list, dropping blanks and duplicates.
func (c *Config) SetFavorites(list []string) {
	seen := map[string]bool{}
	var out []string
	for _, f := range list {
		f = strings.TrimSpace(f)
		k := strings.ToLower(f)
		if f == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, f)
	}
	mu.Lock()
	defer mu.Unlock()
	c.Favorites = out
}

// Group returns one group's models (nil if there is no such group).
func (c *Config) Group(name string) []string {
	mu.RLock()
	defer mu.RUnlock()
	for k, v := range c.Groups {
		if strings.EqualFold(k, name) {
			return append([]string(nil), v...)
		}
	}
	return nil
}

// GroupNames returns the group names, sorted.
func (c *Config) GroupNames() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(c.Groups))
	for k := range c.Groups {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SetGroup creates or replaces a group; an empty list deletes it.
func (c *Config) SetGroup(name string, models []string) {
	name = strings.ToLower(strings.TrimSpace(name))
	seen := map[string]bool{}
	var out []string
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" || seen[strings.ToLower(m)] {
			continue
		}
		seen[strings.ToLower(m)] = true
		out = append(out, m)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(out) == 0 {
		delete(c.Groups, name)
		return
	}
	if c.Groups == nil {
		c.Groups = map[string][]string{}
	}
	c.Groups[name] = out
}

// GetCustomModels returns a copy of the user-added models.
func (c *Config) GetCustomModels() []CustomModel {
	mu.RLock()
	defer mu.RUnlock()
	return append([]CustomModel(nil), c.CustomModels...)
}

// AddCustomModel records a user-added model once.
func (c *Config) AddCustomModel(m CustomModel) {
	mu.Lock()
	defer mu.Unlock()
	for _, x := range c.CustomModels {
		if x.Provider == m.Provider && x.Upstream == m.Upstream {
			return
		}
	}
	c.CustomModels = append(c.CustomModels, m)
}

// Excluded reports whether a provider is turned off.
func (c *Config) Excluded(provider string) bool {
	mu.RLock()
	defer mu.RUnlock()
	for _, p := range c.ExcludedProviders {
		if p == provider {
			return true
		}
	}
	return false
}

// SetExcluded turns a provider off (true) or back on (false).
func (c *Config) SetExcluded(provider string, excluded bool) {
	mu.Lock()
	defer mu.Unlock()
	out := c.ExcludedProviders[:0:0]
	for _, p := range c.ExcludedProviders {
		if p != provider {
			out = append(out, p)
		}
	}
	if excluded {
		out = append(out, provider)
	}
	c.ExcludedProviders = out
}

// Dir returns the config directory, honouring MANGOMAN_HOME.
func Dir() (string, error) {
	if d := os.Getenv("MANGOMAN_HOME"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "mangoman"), nil
}

// Path joins a file name onto the config directory.
func Path(name string) (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, name), nil
}

// NewToken returns a random local token.
func NewToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return "mm-local-" + hex.EncodeToString(b)
}

// ErrNotInitialised means `mangoman init` has not been run.
var ErrNotInitialised = errors.New("not initialised: run `mangoman init`")

// Load reads config.json.
func Load() (*Config, error) {
	p, err := Path("config.json")
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, ErrNotInitialised
	}
	if err != nil {
		return nil, err
	}
	c := &Config{}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, err
	}
	if c.Port == 0 {
		c.Port = DefaultPort
	}
	if c.MaxAttempts == 0 {
		c.MaxAttempts = 6
	}
	return c, nil
}

// Save writes config.json with owner-only permissions. It writes a temp
// file and renames it, so a reader never sees a half-written file.
func Save(c *Config) error {
	saveMu.Lock()
	defer saveMu.Unlock()
	d, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return err
	}
	mu.RLock()
	data, err := json.MarshalIndent(c, "", "  ")
	mu.RUnlock()
	if err != nil {
		return err
	}
	p := filepath.Join(d, "config.json")
	if err := os.WriteFile(p+".tmp", data, 0o600); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}
