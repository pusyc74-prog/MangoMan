package agents

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The marketplace index lives on the repository's registry branch. Only
// packages that passed the review are listed, and the index is signed with
// MangoMan's marketplace key, so a changed index or package is refused.
const (
	RegistryURL = "https://raw.githubusercontent.com/pusyc74-prog/MangoMan/registry/"
	// RegistryKey verifies index.json (Ed25519, base64). Empty until the
	// marketplace key is made; then installs by name work.
	RegistryKey = ""
)

// Listing is one agent in the marketplace index.
type Listing struct {
	Manifest
	Publisher string   `json:"publisher"`
	File      string   `json:"file"`
	SHA256    string   `json:"sha256"`
	Score     *Score   `json:"score,omitempty"`
	Checks    []string `json:"review_points,omitempty"` // reviewer notes ("check" findings)
}

// Score is an agent's result on its free pack's test set.
type Score struct {
	Pack  float64 `json:"pack"`
	Agent float64 `json:"agent"`
}

// Index is the signed marketplace index.
type Index struct {
	Updated string    `json:"updated"`
	Agents  []Listing `json:"agents"`
}

// BuildIndex reviews every package in dir (newest version per name wins)
// and returns the signed index. A package with a blocking finding stops the
// build. An optional NAME-VERSION.eval.json next to a package holds its score.
func BuildIndex(dir, keyPath string) (index, sig []byte, err error) {
	key, err := loadKey(keyPath)
	if err != nil {
		return nil, nil, err
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.mmagent"))
	best := map[string]Listing{}
	for _, f := range files {
		tmp, err := os.MkdirTemp("", "mangoman-index-")
		if err != nil {
			return nil, nil, err
		}
		pub, err := Unpack(f, tmp)
		if err != nil {
			os.RemoveAll(tmp)
			return nil, nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		m, findings, err := Review(tmp)
		os.RemoveAll(tmp)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		l := Listing{Manifest: m, Publisher: pub, File: filepath.Base(f)}
		for _, x := range findings {
			if x.Level == "block" {
				return nil, nil, fmt.Errorf("%s: %s:%d %s", l.File, x.File, x.Line, x.Reason)
			}
			l.Checks = append(l.Checks, fmt.Sprintf("%s:%d %s", x.File, x.Line, x.Reason))
		}
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, nil, err
		}
		sum := sha256.Sum256(data)
		l.SHA256 = hex.EncodeToString(sum[:])
		if b, err := os.ReadFile(strings.TrimSuffix(f, ".mmagent") + ".eval.json"); err == nil {
			l.Score = &Score{}
			if err := json.Unmarshal(b, l.Score); err != nil {
				return nil, nil, fmt.Errorf("%s: bad eval file: %w", l.File, err)
			}
		}
		if old, ok := best[m.Name]; ok {
			if old.Publisher != pub {
				return nil, nil, fmt.Errorf("%s: two packages of %s signed by different creators", l.File, m.Name)
			}
			if !newer(m.Version, old.Version) {
				continue
			}
		}
		best[m.Name] = l
	}
	ix := Index{Updated: time.Now().UTC().Format(time.RFC3339)}
	for _, l := range best {
		ix.Agents = append(ix.Agents, l)
	}
	sort.Slice(ix.Agents, func(i, j int) bool { return ix.Agents[i].Name < ix.Agents[j].Name })
	index, err = json.MarshalIndent(ix, "", " ")
	if err != nil {
		return nil, nil, err
	}
	return index, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, index))), nil
}

func newer(a, b string) bool {
	var x, y [3]int
	fmt.Sscanf(a, "%d.%d.%d", &x[0], &x[1], &x[2])
	fmt.Sscanf(b, "%d.%d.%d", &y[0], &y[1], &y[2])
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

// Registry is where installs by name come from.
type Registry struct {
	URL string // base URL ending in "/"
	Key string // base64 Ed25519 public key
}

// DefaultRegistry is MangoMan's marketplace; MANGOMAN_REGISTRY and
// MANGOMAN_REGISTRY_KEY point at another one (for testing a registry).
func DefaultRegistry() Registry {
	r := Registry{URL: RegistryURL, Key: RegistryKey}
	if u := os.Getenv("MANGOMAN_REGISTRY"); u != "" {
		r = Registry{URL: strings.TrimSuffix(u, "/") + "/", Key: os.Getenv("MANGOMAN_REGISTRY_KEY")}
	}
	return r
}

func (r Registry) get(name string) ([]byte, error) {
	if strings.HasPrefix(r.URL, "file://") {
		return os.ReadFile(strings.TrimPrefix(r.URL, "file://") + name)
	}
	c := &http.Client{Timeout: 60 * time.Second}
	resp, err := c.Get(r.URL + name)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", name, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
}

// Fetch downloads the index and checks its signature.
func (r Registry) Fetch() (Index, error) {
	var ix Index
	pub, err := base64.StdEncoding.DecodeString(r.Key)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return ix, errors.New("the marketplace is not open yet: install agents from a .mmagent file")
	}
	data, err := r.get("index.json")
	if err != nil {
		return ix, err
	}
	sigText, err := r.get("index.json.sig")
	if err != nil {
		return ix, err
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigText)))
	if err != nil || !ed25519.Verify(pub, data, sig) {
		return ix, errors.New("the marketplace index failed its signature check")
	}
	return ix, json.Unmarshal(data, &ix)
}

// InstallListed downloads a listed agent, checks it against the signed
// index and installs it.
func (r Registry) InstallListed(name, agentsDir string) (Listing, error) {
	ix, err := r.Fetch()
	if err != nil {
		return Listing{}, err
	}
	for _, l := range ix.Agents {
		if l.Name != name {
			continue
		}
		data, err := r.get("packages/" + l.File)
		if err != nil {
			return l, err
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != l.SHA256 {
			return l, errors.New("the downloaded package does not match the marketplace index")
		}
		f, err := os.CreateTemp("", "mangoman-*.mmagent")
		if err != nil {
			return l, err
		}
		defer os.Remove(f.Name())
		if _, err := f.Write(data); err != nil {
			f.Close()
			return l, err
		}
		f.Close()
		check, err := os.MkdirTemp("", "mangoman-check-")
		if err != nil {
			return l, err
		}
		defer os.RemoveAll(check)
		if pub, err := Unpack(f.Name(), check); err != nil || pub != l.Publisher {
			return l, errors.New("the package is not signed by the creator the marketplace lists")
		}
		_, err = Install(f.Name(), agentsDir)
		return l, err
	}
	return Listing{}, fmt.Errorf("no agent named %q in the marketplace", name)
}

// Publisher returns the key an installed agent was signed with.
func Publisher(agentsDir, name string) string {
	b, _ := os.ReadFile(filepath.Join(agentsDir, name, pubFile))
	return strings.TrimSpace(string(b))
}
