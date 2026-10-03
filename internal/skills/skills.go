// Package skills ships MangoMan's skill packs: folders in the open Agent
// Skills format (SKILL.md plus scripts) that OpenCode, Claude Code and Codex
// load by themselves. Each pack carries expert instructions, a tested
// renderer and a checker, so the result does not depend on a model's taste.
// Shared Python helpers live once in packs/shared and are copied into every
// pack's scripts folder on install.
package skills

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

//go:embed packs
var packsFS embed.FS

// Marker is written into every installed pack so updates and removals only
// touch MangoMan's own folders.
const Marker = ".mangoman-pack"

// Pack describes one skill pack.
type Pack struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
}

var nameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// List returns the embedded packs, sorted by name.
func List() ([]Pack, error) {
	entries, err := fs.ReadDir(packsFS, "packs")
	if err != nil {
		return nil, err
	}
	var out []Pack
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "shared" {
			continue
		}
		data, err := fs.ReadFile(packsFS, path.Join("packs", e.Name(), "SKILL.md"))
		if err != nil {
			return nil, fmt.Errorf("pack %s: %w", e.Name(), err)
		}
		p, err := ParseSkill(data)
		if err != nil {
			return nil, fmt.Errorf("pack %s: %w", e.Name(), err)
		}
		if p.Name != e.Name() {
			return nil, fmt.Errorf("pack %s: name %q must match its folder", e.Name(), p.Name)
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ParseSkill reads name, description and metadata.version from a SKILL.md
// frontmatter, checking the Agent Skills rules.
func ParseSkill(data []byte) (Pack, error) {
	var p Pack
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")) // files saved on Windows
	if !bytes.HasPrefix(data, []byte("---\n")) {
		return p, errors.New("SKILL.md must start with --- frontmatter")
	}
	end := bytes.Index(data[4:], []byte("\n---"))
	if end < 0 {
		return p, errors.New("frontmatter is not closed")
	}
	for _, line := range strings.Split(string(data[4:4+end]), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"`)
		switch strings.TrimRight(k, " ") {
		case "name":
			p.Name = v
		case "description":
			p.Description = v
		case "  version":
			p.Version = v
		}
	}
	if !nameRe.MatchString(p.Name) || len(p.Name) > 64 {
		return p, fmt.Errorf("invalid skill name %q", p.Name)
	}
	if n := len(p.Description); n == 0 || n > 1024 {
		return p, fmt.Errorf("description must be 1 to 1024 characters, has %d", n)
	}
	return p, nil
}

// Install writes every pack into dir (one folder per pack). Existing
// MangoMan packs are replaced; a folder with the same name that is not ours
// is left alone and reported.
func Install(dir string) (installed []string, skipped []string, err error) {
	packs, err := List()
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	for _, p := range packs {
		dest := filepath.Join(dir, p.Name)
		if st, err := os.Stat(dest); err == nil && st.IsDir() {
			if _, err := os.Stat(filepath.Join(dest, Marker)); err != nil {
				skipped = append(skipped, dest)
				continue
			}
			if err := os.RemoveAll(dest); err != nil {
				return installed, skipped, err
			}
		}
		root := path.Join("packs", p.Name)
		werr := fs.WalkDir(packsFS, root, func(fp string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel := strings.TrimPrefix(strings.TrimPrefix(fp, root), "/")
			target := filepath.Join(dest, filepath.FromSlash(rel))
			if d.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			if strings.Contains(rel, "__pycache__") {
				return nil
			}
			return writeFrom(fp, target)
		})
		if werr != nil {
			return installed, skipped, werr
		}
		if err := CopyShared(filepath.Join(dest, "scripts")); err != nil {
			return installed, skipped, err
		}
		if err := os.WriteFile(filepath.Join(dest, Marker), []byte(p.Version+"\n"), 0o644); err != nil {
			return installed, skipped, err
		}
		installed = append(installed, dest)
	}
	return installed, skipped, nil
}

// CopyShared writes the shared Python helpers (render, vizlib, brandkit,
// checks, tracenum) into dir, so packs and agents can import them.
func CopyShared(dir string) error {
	shared, err := fs.ReadDir(packsFS, "packs/shared")
	if err != nil {
		return err
	}
	for _, s := range shared {
		if s.IsDir() || !strings.HasSuffix(s.Name(), ".py") {
			continue
		}
		if err := writeFrom(path.Join("packs/shared", s.Name()), filepath.Join(dir, s.Name())); err != nil {
			return err
		}
	}
	return nil
}

// CopyScripts writes a pack's own scripts into dir, skipping files dir
// already has, so an agent can build on the free pack it improves.
func CopyScripts(pack, dir string) error {
	root := path.Join("packs", pack, "scripts")
	entries, err := fs.ReadDir(packsFS, root)
	if err != nil {
		return fmt.Errorf("no skill pack named %q", pack)
	}
	for _, e := range entries {
		target := filepath.Join(dir, e.Name())
		if e.IsDir() || strings.Contains(e.Name(), "__pycache__") {
			continue
		}
		if _, err := os.Stat(target); err == nil {
			continue
		}
		if err := writeFrom(path.Join(root, e.Name()), target); err != nil {
			return err
		}
	}
	return nil
}

func writeFrom(src, target string) error {
	data, err := fs.ReadFile(packsFS, src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if strings.HasSuffix(src, ".py") {
		mode = 0o755
	}
	return os.WriteFile(target, data, mode)
}

// Remove deletes MangoMan's packs from dir, leaving anything else.
func Remove(dir string) ([]string, error) {
	packs, err := List()
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, p := range packs {
		dest := filepath.Join(dir, p.Name)
		if _, err := os.Stat(filepath.Join(dest, Marker)); err != nil {
			continue
		}
		if err := os.RemoveAll(dest); err != nil {
			return removed, err
		}
		removed = append(removed, dest)
	}
	return removed, nil
}
