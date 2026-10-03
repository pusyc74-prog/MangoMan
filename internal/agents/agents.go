// Package agents handles advanced agents: skill folders made by MangoMan or
// outside creators, with an agent.json that declares what the agent may do.
// Creators sign a package with their own key; MangoMan checks the signature
// and every file before installing, and later updates must come from the
// same key. Agents use the Agent Skills format, so OpenCode loads them like
// the free packs, and their scripts run inside the sandbox (sandbox.go).
package agents

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pusyc74-prog/mangoman/internal/skills"
)

// Manifest is an agent's agent.json.
type Manifest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// Skill is the free pack this agent improves on; the agent must beat it
	// on that skill's test set to list as Advanced.
	Skill  string `json:"skill"`
	Author struct {
		Name    string `json:"name"`
		Contact string `json:"contact"`
	} `json:"author"`
	// RunsOn is "local" (scripts run on the user's machine, in the sandbox)
	// or "developer-cloud" (the agent sends data to Endpoint).
	RunsOn      string `json:"runs_on"`
	Endpoint    string `json:"endpoint,omitempty"`
	Permissions struct {
		Network  []string `json:"network"`  // hosts the scripts may reach; empty means none
		Commands []string `json:"commands"` // programs the scripts may start besides python
	} `json:"permissions"`
	Models     string `json:"models"` // "free" or "paid"; paid means the agent needs a paid model
	PriceINR   int    `json:"price_inr_month"`
	DataPolicy string `json:"data_policy"`
}

const (
	manifestFile = "agent.json"
	sigFile      = "SIGNATURE.json"
	pubFile      = ".publisher"
	maxFiles     = 2000
	maxBytes     = 50 << 20
)

var (
	nameRe    = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	versionRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	hostRe    = regexp.MustCompile(`^((\*\.)?[a-z0-9-]+(\.[a-z0-9-]+)+|localhost)$`)
)

// Validate checks the manifest; every problem is listed.
func (m Manifest) Validate() error {
	var p []string
	if !nameRe.MatchString(m.Name) || len(m.Name) > 64 {
		p = append(p, "name must be lower-case words joined by dashes")
	}
	if !versionRe.MatchString(m.Version) {
		p = append(p, "version must look like 1.0.0")
	}
	if m.Title == "" || m.Description == "" {
		p = append(p, "title and description are required")
	}
	if !nameRe.MatchString(m.Skill) {
		p = append(p, "skill must name the free pack this agent improves on")
	}
	if m.Author.Name == "" || m.Author.Contact == "" {
		p = append(p, "author name and contact are required")
	}
	switch m.RunsOn {
	case "local":
		if m.Endpoint != "" {
			p = append(p, "endpoint is only for developer-cloud agents")
		}
	case "developer-cloud":
		if !strings.HasPrefix(m.Endpoint, "https://") {
			p = append(p, "developer-cloud agents need an https endpoint")
		}
	default:
		p = append(p, `runs_on must be "local" or "developer-cloud"`)
	}
	for _, h := range m.Permissions.Network {
		if !hostRe.MatchString(h) {
			p = append(p, fmt.Sprintf("network host %q is not a host name", h))
		}
	}
	for _, c := range m.Permissions.Commands {
		if c == "" || strings.ContainsAny(c, `/\ `) {
			p = append(p, fmt.Sprintf("command %q must be a bare program name", c))
		}
	}
	if m.Models != "free" && m.Models != "paid" {
		p = append(p, `models must be "free" or "paid"`)
	}
	if m.PriceINR < 0 {
		p = append(p, "price cannot be negative")
	}
	if m.DataPolicy == "" {
		p = append(p, "data_policy must say what happens to the user's data")
	}
	if len(p) > 0 {
		return errors.New("agent.json: " + strings.Join(p, "; "))
	}
	return nil
}

// Load reads and checks an agent folder: agent.json plus a SKILL.md whose
// name matches.
func Load(dir string) (Manifest, error) {
	var m Manifest
	data, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if err != nil {
		return m, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, fmt.Errorf("agent.json: %w", err)
	}
	if err := m.Validate(); err != nil {
		return m, err
	}
	sk, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return m, err
	}
	p, err := skills.ParseSkill(sk)
	if err != nil {
		return m, fmt.Errorf("SKILL.md: %w", err)
	}
	if p.Name != m.Name {
		return m, fmt.Errorf("SKILL.md name %q must match agent.json name %q", p.Name, m.Name)
	}
	return m, nil
}

// Keygen writes a new creator key (private, 0600) to path and returns the
// public key as text.
func Keygen(path string) (string, error) {
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s already exists; keep it, it signs your updates", path)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(pub), nil
}

func loadKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(k) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%s is not a creator key", path)
	}
	return ed25519.PrivateKey(k), nil
}

type signature struct {
	Publisher string            `json:"publisher"`
	Files     map[string]string `json:"files"` // path -> sha256
	Sig       string            `json:"sig"`
}

// signedBytes is what the signature covers: one "sha256  path" line per
// file, sorted by path.
func signedBytes(files map[string]string) []byte {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var b bytes.Buffer
	for _, n := range names {
		fmt.Fprintf(&b, "%s  %s\n", files[n], n)
	}
	return b.Bytes()
}

// Pack signs the agent folder src with the key at keyPath and writes a
// .mmagent package to out.
func Pack(src, keyPath, out string) (Manifest, error) {
	m, err := Load(src)
	if err != nil {
		return m, err
	}
	key, err := loadKey(keyPath)
	if err != nil {
		return m, err
	}
	contents := map[string][]byte{}
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && (strings.HasPrefix(d.Name(), ".") || d.Name() == "__pycache__") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || strings.HasSuffix(rel, ".mmagent") || rel == sigFile {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s: only plain files can be packed", rel)
		}
		data, err := os.ReadFile(p)
		contents[rel] = data
		return err
	})
	if err != nil {
		return m, err
	}
	files := map[string]string{}
	for n, data := range contents {
		sum := sha256.Sum256(data)
		files[n] = hex.EncodeToString(sum[:])
	}
	sig := signature{
		Publisher: base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)),
		Files:     files,
		Sig:       base64.StdEncoding.EncodeToString(ed25519.Sign(key, signedBytes(files))),
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(contents))
	for n := range contents {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			return m, err
		}
		if _, err := w.Write(contents[n]); err != nil {
			return m, err
		}
	}
	w, err := zw.Create(sigFile)
	if err != nil {
		return m, err
	}
	if err := json.NewEncoder(w).Encode(sig); err != nil {
		return m, err
	}
	if err := zw.Close(); err != nil {
		return m, err
	}
	return m, os.WriteFile(out, buf.Bytes(), 0o644)
}

// Install checks the package at pkg and unpacks it into agentsDir/<name>.
// An installed agent is only replaced by a package signed with the same key.
func Install(pkg, agentsDir string) (Manifest, error) {
	var m Manifest
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		return m, err
	}
	tmp, err := os.MkdirTemp(agentsDir, ".install-")
	if err != nil {
		return m, err
	}
	defer os.RemoveAll(tmp)
	publisher, err := Unpack(pkg, tmp)
	if err != nil {
		return m, err
	}
	m, findings, err := Review(tmp)
	if err != nil {
		return m, err
	}
	for _, f := range findings {
		if f.Level == "block" {
			return m, fmt.Errorf("%s fails the safety review: %s:%d %s", m.Name, f.File, f.Line, f.Reason)
		}
	}
	dest := filepath.Join(agentsDir, m.Name)
	if old, err := os.ReadFile(filepath.Join(dest, pubFile)); err == nil && strings.TrimSpace(string(old)) != publisher {
		return m, fmt.Errorf("%s is installed from a different creator key; remove it first if you trust the new one", m.Name)
	}
	if cur, err := Load(dest); err == nil && newer(cur.Version, m.Version) {
		return m, fmt.Errorf("%s %s is installed; %s is older (remove it first to go back)", m.Name, cur.Version, m.Version)
	}
	if err := skills.CopyShared(filepath.Join(tmp, "scripts")); err != nil {
		return m, err
	}
	if err := skills.CopyScripts(m.Skill, filepath.Join(tmp, "scripts")); err != nil {
		return m, err
	}
	if err := os.WriteFile(filepath.Join(tmp, pubFile), []byte(publisher+"\n"), 0o644); err != nil {
		return m, err
	}
	if err := os.RemoveAll(dest); err != nil {
		return m, err
	}
	return m, os.Rename(tmp, dest)
}

// Unpack checks the package's signature and every file, then writes the
// files into dir. It returns the creator's public key.
func Unpack(pkg, dir string) (string, error) {
	zr, err := zip.OpenReader(pkg)
	if err != nil {
		return "", fmt.Errorf("%s is not an agent package: %w", pkg, err)
	}
	defer zr.Close()
	if len(zr.File) > maxFiles {
		return "", errors.New("package has too many files")
	}
	contents := map[string][]byte{}
	seen := map[string]bool{}
	var total int64
	for _, f := range zr.File {
		n := f.Name
		if n != path.Clean(n) || path.IsAbs(n) || strings.HasPrefix(n, "../") || strings.ContainsAny(n, "\\\n\r") || n == ".." {
			return "", fmt.Errorf("unsafe path %q in package", n)
		}
		if seen[strings.ToLower(n)] {
			return "", fmt.Errorf("package has %q twice (names must differ by more than case)", n)
		}
		seen[strings.ToLower(n)] = true
		if f.FileInfo().IsDir() {
			continue
		}
		if !f.Mode().IsRegular() {
			return "", fmt.Errorf("%s: only plain files are allowed", n)
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		data, err := io.ReadAll(io.LimitReader(rc, maxBytes-total+1))
		rc.Close()
		if err != nil {
			return "", err
		}
		if total += int64(len(data)); total > maxBytes {
			return "", errors.New("package is larger than 50 MB")
		}
		contents[n] = data
	}
	var sig signature
	if err := json.Unmarshal(contents[sigFile], &sig); err != nil {
		return "", errors.New("package is not signed")
	}
	delete(contents, sigFile)
	pub, err := base64.StdEncoding.DecodeString(sig.Publisher)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return "", errors.New("package has a bad publisher key")
	}
	sb, _ := base64.StdEncoding.DecodeString(sig.Sig)
	if !ed25519.Verify(pub, signedBytes(sig.Files), sb) {
		return "", errors.New("signature check failed: the package was changed after signing")
	}
	if len(sig.Files) != len(contents) {
		return "", errors.New("package files do not match its signature")
	}
	for n, data := range contents {
		sum := sha256.Sum256(data)
		if sig.Files[n] != hex.EncodeToString(sum[:]) {
			return "", fmt.Errorf("%s does not match its signature", n)
		}
		target := filepath.Join(dir, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return "", err
		}
	}
	return sig.Publisher, nil
}

// List returns the installed agents, sorted by name.
func List(agentsDir string) ([]Manifest, error) {
	entries, err := os.ReadDir(agentsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Manifest
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		m, err := Load(filepath.Join(agentsDir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		out = append(out, m)
	}
	return out, nil
}

// Remove deletes an installed agent.
func Remove(agentsDir, name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("no agent named %q", name)
	}
	dest := filepath.Join(agentsDir, name)
	if _, err := os.Stat(filepath.Join(dest, pubFile)); err != nil {
		return fmt.Errorf("no agent named %q is installed", name)
	}
	return os.RemoveAll(dest)
}

// Expose copies each installed agent's instructions (not its scripts, which
// only run through the sandbox) into skillsDir, where OpenCode finds them.
func Expose(agentsDir, skillsDir string) error {
	list, err := List(agentsDir)
	if err != nil {
		return err
	}
	for _, m := range list {
		src, dest := filepath.Join(agentsDir, m.Name), filepath.Join(skillsDir, m.Name)
		if err := os.RemoveAll(dest); err != nil {
			return err
		}
		err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(src, p)
			if d.IsDir() {
				if rel == "scripts" {
					return filepath.SkipDir
				}
				return os.MkdirAll(filepath.Join(dest, rel), 0o755)
			}
			if d.Name() == pubFile {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dest, rel), data, 0o644)
		})
		if err != nil {
			return err
		}
	}
	return nil
}
