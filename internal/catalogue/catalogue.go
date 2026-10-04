// Package catalogue holds the model catalogue: every free model, where it is
// served, its limits and its data policy. The catalogue is data, never code.
// Phase 1 ships an embedded seed; M5 adds the live signed feed, which is
// verified with VerifyAndParse before it replaces the seed.
package catalogue

import (
	"crypto/ed25519"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed seed.json
var seedJSON []byte

// DataPolicy describes what a provider (or one model) does with prompts.
type DataPolicy struct {
	Retention    string `json:"retention"`      // e.g. "none", "30 days", "unknown"
	TrainsOnData string `json:"trains_on_data"` // "no", "yes", "opt-out", "unknown"
	Jurisdiction string `json:"jurisdiction"`   // ISO country, or "local"
	TermsURL     string `json:"terms_url,omitempty"`
	Verified     bool   `json:"verified"` // true once a human reviewed the terms
}

// Label returns a short ASCII label (it is also sent as a response header),
// e.g. "US; no training; retention none".
func (p DataPolicy) Label() string {
	train := map[string]string{"no": "no training", "yes": "trains on data", "opt-out": "training opt-out"}[p.TrainsOnData]
	if train == "" {
		train = "training unknown"
	}
	s := fmt.Sprintf("%s; %s; retention %s", p.Jurisdiction, train, p.Retention)
	if !p.Verified {
		s += " (unverified)"
	}
	return s
}

// Provider is an upstream that serves models.
type Provider struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	BaseURL   string  `json:"base_url"`
	Kind      string  `json:"kind"` // "openai" (OpenAI-compatible); native kinds later
	KeyEnv    string  `json:"key_env,omitempty"`
	NeedsKey  bool    `json:"needs_key"`
	Local     bool    `json:"local,omitempty"` // runs on the user's machine (Ollama)
	SignupURL string  `json:"signup_url,omitempty"`
	Speed     float64 `json:"speed"` // 0..1, higher is faster (seed estimate)
	// Priority orders providers in the router's own ranking: 1 first. Models
	// of equal skill on a lower number are tried first. 0 counts as 3.
	Priority int        `json:"priority,omitempty"`
	Policy   DataPolicy `json:"policy"`
	Quirks   Quirks     `json:"quirks,omitempty"`
	// AccountLimits apply across all of this provider's models for one key,
	// e.g. OpenRouter's daily cap on free requests.
	AccountLimits Limits `json:"account_limits,omitempty"`
	// RateHeaders say which window each rate-limit header describes. Empty
	// means the generic x-ratelimit-remaining-* handling.
	RateHeaders []RateHeader `json:"rate_headers,omitempty"`
	// Discover says which models in the provider's live list count as free
	// chat models for the new-model radar.
	Discover Discover `json:"discover,omitempty"`
}

// Discover is a provider's radar rule.
type Discover struct {
	Mode    string   `json:"mode,omitempty"`    // "all", "suffix" or "" (off)
	Suffix  string   `json:"suffix,omitempty"`  // for "suffix": e.g. ":free"
	Exclude []string `json:"exclude,omitempty"` // substrings of non-chat models
}

// Matches reports whether a listed model id is a free chat model.
func (d Discover) Matches(id string) bool {
	low := strings.ToLower(id)
	for _, x := range d.Exclude {
		if strings.Contains(low, strings.ToLower(x)) {
			return false
		}
	}
	switch d.Mode {
	case "all":
		return true
	case "suffix":
		return d.Suffix != "" && strings.HasSuffix(low, strings.ToLower(d.Suffix))
	}
	return false
}

// Quirks adjust a request for one provider. They are data, so a catalogue
// update can fix a provider without a new release.
type Quirks struct {
	// DropParams are top-level request fields the provider rejects.
	DropParams []string `json:"drop_params,omitempty"`
	// MaxTokensField renames max_tokens / max_completion_tokens to the one
	// field this provider accepts.
	MaxTokensField string `json:"max_tokens_field,omitempty"`
	// StreamUsage asks for a final usage chunk on streams
	// (stream_options.include_usage), so quota counts are exact.
	StreamUsage bool `json:"stream_usage,omitempty"`
}

// RateHeader maps one family of rate-limit headers to a quota window.
type RateHeader struct {
	Kind      string `json:"kind"`   // "requests" or "tokens"
	Window    string `json:"window"` // "minute" or "day"
	Limit     string `json:"limit"`
	Remaining string `json:"remaining"`
	Reset     string `json:"reset"`
}

// Limits are free-tier limits; 0 means unknown or unlimited.
type Limits struct {
	RPM int `json:"rpm,omitempty"`
	RPD int `json:"rpd,omitempty"`
	TPM int `json:"tpm,omitempty"`
	TPD int `json:"tpd,omitempty"`
}

// Model is one model as served by one provider.
type Model struct {
	Canonical string             `json:"canonical"` // same model across providers shares this
	Provider  string             `json:"provider"`
	Upstream  string             `json:"upstream"` // provider's own model id
	Free      bool               `json:"free"`
	Context   int                `json:"context"`
	Caps      []string           `json:"caps"` // tools, json, vision, streaming, reasoning
	Limits    Limits             `json:"limits"`
	Quality   map[string]float64 `json:"quality"`          // per task class, 0..1
	Policy    *DataPolicy        `json:"policy,omitempty"` // overrides provider policy
}

// ID is the provider-qualified model id, e.g. "groq/llama-3.3-70b".
func (m Model) ID() string { return m.Provider + "/" + m.Canonical }

// Has reports whether the model declares a capability.
func (m Model) Has(cap string) bool {
	for _, c := range m.Caps {
		if c == cap {
			return true
		}
	}
	return false
}

// QualityFor returns the quality score for a task class, defaulting to 0.5.
func (m Model) QualityFor(class string) float64 {
	if q, ok := m.Quality[class]; ok {
		return q
	}
	if q, ok := m.Quality["default"]; ok {
		return q
	}
	return 0.5
}

// Catalogue is the full data set. It is safe for concurrent use.
type Catalogue struct {
	Version   string     `json:"version"`
	Updated   string     `json:"updated"`
	Providers []Provider `json:"providers"`
	Models    []Model    `json:"models"`

	mu        sync.RWMutex
	providers map[string]Provider
}

// Seed returns the catalogue embedded in the binary.
func Seed() (*Catalogue, error) { return Parse(seedJSON) }

// Parse decodes and validates catalogue JSON.
func Parse(data []byte) (*Catalogue, error) {
	var c Catalogue
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("catalogue: %w", err)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	c.index()
	return &c, nil
}

func (c *Catalogue) validate() error {
	seen := map[string]bool{}
	for _, p := range c.Providers {
		if p.ID == "" || p.BaseURL == "" {
			return errors.New("catalogue: provider missing id or base_url")
		}
		if !strings.HasPrefix(p.BaseURL, "https://") && !p.Local {
			return fmt.Errorf("catalogue: provider %s must use https", p.ID)
		}
		for _, rh := range p.RateHeaders {
			if (rh.Kind != "requests" && rh.Kind != "tokens") || (rh.Window != "minute" && rh.Window != "day") || rh.Remaining == "" {
				return fmt.Errorf("catalogue: provider %s has a bad rate_headers entry", p.ID)
			}
		}
		if f := p.Quirks.MaxTokensField; f != "" && f != "max_tokens" && f != "max_completion_tokens" {
			return fmt.Errorf("catalogue: provider %s has a bad max_tokens_field", p.ID)
		}
		seen[p.ID] = true
	}
	for _, m := range c.Models {
		if !seen[m.Provider] {
			return fmt.Errorf("catalogue: model %s has unknown provider %s", m.Canonical, m.Provider)
		}
		if m.Canonical == "" || m.Upstream == "" {
			return errors.New("catalogue: model missing canonical or upstream id")
		}
	}
	return nil
}

func (c *Catalogue) index() {
	c.providers = make(map[string]Provider, len(c.Providers))
	for _, p := range c.Providers {
		c.providers[p.ID] = p
	}
}

// Provider looks up a provider by id.
func (c *Catalogue) Provider(id string) (Provider, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	p, ok := c.providers[id]
	return p, ok
}

// AllProviders returns providers sorted by id.
func (c *Catalogue) AllProviders() []Provider {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := append([]Provider(nil), c.Providers...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// AllModels returns a copy of every model.
func (c *Catalogue) AllModels() []Model {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Model(nil), c.Models...)
}

// PolicyFor returns the effective data policy of a model.
func (c *Catalogue) PolicyFor(m Model) DataPolicy {
	if m.Policy != nil {
		return *m.Policy
	}
	p, _ := c.Provider(m.Provider)
	return p.Policy
}

// HasUpstream reports whether a provider's model id is in the catalogue.
func (c *Catalogue) HasUpstream(provider, upstream string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, m := range c.Models {
		if m.Provider == provider && m.Upstream == upstream {
			return true
		}
	}
	return false
}

// AddModel adds one model unless the provider already has that upstream id.
func (c *Catalogue) AddModel(m Model) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, x := range c.Models {
		if x.Provider == m.Provider && x.Upstream == m.Upstream {
			return false
		}
	}
	c.Models = append(c.Models, m)
	return true
}

// DiscoveredModel builds a usable catalogue entry for a model the radar
// found: the provider's policy and typical limits, cautious defaults
// otherwise. Capabilities are assumed and corrected by failover.
func (c *Catalogue) DiscoveredModel(provider, upstream string) Model {
	name := upstream
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSuffix(strings.TrimSuffix(name, ":free"), "-free")
	m := Model{Canonical: name, Provider: provider, Upstream: upstream, Free: true, Context: 32768,
		Caps: []string{"tools", "json", "streaming"}, Quality: map[string]float64{"default": 0.6}}
	c.mu.RLock()
	defer c.mu.RUnlock()
	limits := false
	for _, x := range c.Models {
		if x.Provider != provider {
			continue
		}
		if !limits {
			m.Limits, limits = x.Limits, true
		}
		if x.Canonical == m.Canonical && x.Upstream != upstream {
			// Another model of this provider has the short name: keep the
			// full id, so the two never share a breaker or a list entry.
			m.Canonical = strings.TrimSuffix(upstream, ":free")
		}
	}
	return m
}

// ReplaceProviderModels swaps all models of one provider, used for models
// discovered at runtime (local Ollama models).
func (c *Catalogue) ReplaceProviderModels(provider string, models []Model) {
	c.mu.Lock()
	defer c.mu.Unlock()
	kept := c.Models[:0:0]
	for _, m := range c.Models {
		if m.Provider != provider {
			kept = append(kept, m)
		}
	}
	c.Models = append(kept, models...)
}

// VerifyAndParse checks an Ed25519 signature over a feed before parsing it.
// A feed that fails verification is rejected and the caller keeps the last
// good catalogue.
func VerifyAndParse(data, sig []byte, pub ed25519.PublicKey) (*Catalogue, error) {
	if len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("catalogue: bad public key")
	}
	if !ed25519.Verify(pub, data, sig) {
		return nil, errors.New("catalogue: signature check failed")
	}
	return Parse(data)
}
