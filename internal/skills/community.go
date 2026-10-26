package skills

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pusyc74-prog/mangoman/internal/download"
)

// Community skills: skills other people published under an open license,
// listed in MangoMan's library and installed into the user's own skills only
// when they add one. OpenCode sends every installed skill's description with
// every request, so none of them costs anything until it is added.
// scripts/communityindex builds the list from pinned commits, after a safety
// scan of every file; adding one downloads exactly those files and checks
// each SHA-256.

// Source is one repository community skills come from.
type Source struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Repo        string `json:"repo"` // owner/name on GitHub
	Commit      string `json:"commit"`
	Root        string `json:"root,omitempty"`         // folder the skills are under
	License     string `json:"license"`                // SPDX id
	LicenseFile string `json:"license_file,omitempty"` // in the repository, copied next to each skill
	LicenseText string `json:"license_text,omitempty"`
}

// CommunitySkill is one listed skill.
type CommunitySkill struct {
	Name        string          `json:"name"` // as installed: <source>-<its own name>
	Orig        string          `json:"orig"`
	Source      string          `json:"source"`
	Dir         string          `json:"dir"` // its folder in the repository
	Description string          `json:"description"`
	Needs       []string        `json:"needs,omitempty"`
	Files       []CommunityFile `json:"files"`
}

// CommunityFile is one file of a skill, as checked.
type CommunityFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Sum  string `json:"sum"` // SHA-256, hex
}

// CommunityIndex is the whole list.
type CommunityIndex struct {
	Sources []Source         `json:"sources"`
	Skills  []CommunitySkill `json:"skills"`
}

//go:embed community.json.gz
var communityGz []byte

var (
	communityOnce sync.Once
	community     *CommunityIndex
	communityErr  error
)

// Community returns the list of community skills.
func Community() (*CommunityIndex, error) {
	communityOnce.Do(func() {
		zr, err := gzip.NewReader(bytes.NewReader(communityGz))
		if err != nil {
			communityErr = err
			return
		}
		community = &CommunityIndex{}
		communityErr = json.NewDecoder(zr).Decode(community)
	})
	return community, communityErr
}

// Find returns a listed skill and its source.
func (c *CommunityIndex) Find(name string) (CommunitySkill, Source, bool) {
	for _, k := range c.Skills {
		if k.Name == name {
			for _, s := range c.Sources {
				if s.ID == k.Source {
					return k, s, true
				}
			}
		}
	}
	return CommunitySkill{}, Source{}, false
}

// RequestTokens is about what a skill adds to every request once installed:
// its name, description and folder, as OpenCode lists it, at 4 characters a token.
func (k CommunitySkill) RequestTokens() int {
	return (len(k.Name) + len(k.Description) + 160) / 4
}

// rawBase is where pinned files are downloaded from (a test server in tests).
var rawBase = "https://raw.githubusercontent.com/"

// InstallCommunity downloads the skill's files at the pinned commit into dest, checks
// every one against its recorded SHA-256, and writes a SKILL.md header
// MangoMan and OpenCode both read (name, description, license, where it came
// from) plus the source's license. Nothing is written unless every file
// checks out.
func InstallCommunity(k CommunitySkill, src Source, dest string) error {
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("%s already exists", filepath.Base(dest))
	}
	tmp := dest + ".adding"
	os.RemoveAll(tmp)
	defer os.RemoveAll(tmp)
	runs := false
	for _, f := range k.Files {
		if strings.Contains(f.Path, "..") || path.IsAbs(f.Path) {
			return fmt.Errorf("bad file path %q", f.Path)
		}
		parts := strings.Split(path.Join(k.Dir, f.Path), "/")
		for i, p := range parts {
			parts[i] = url.PathEscape(p)
		}
		raw, err := download.Fetch(rawBase+src.Repo+"/"+src.Commit+"/"+strings.Join(parts, "/"), f.Sum, f.Size+1, nil)
		if err != nil {
			return fmt.Errorf("%s: %w", f.Path, err)
		}
		if f.Path == "SKILL.md" {
			raw = header(k, src, raw)
		}
		switch path.Ext(f.Path) {
		case ".py", ".sh", ".js", ".mjs", ".cjs", ".ts":
			runs = true
		}
		p := filepath.Join(tmp, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, raw, 0o644); err != nil {
			return err
		}
	}
	if src.LicenseText != "" {
		if err := os.WriteFile(filepath.Join(tmp, "LICENSE-"+src.ID+".txt"), []byte(src.LicenseText), 0o644); err != nil {
			return err
		}
	}
	if runs {
		p := filepath.Join(tmp, "SKILL.md")
		raw, _ := os.ReadFile(p)
		_ = os.WriteFile(p, bytes.Replace(raw, []byte("\n  kind: method\n"), []byte("\n  kind: pack\n"), 1), 0o644)
	}
	if _, err := os.Stat(filepath.Join(tmp, "SKILL.md")); err != nil {
		return errors.New("the skill has no SKILL.md")
	}
	return os.Rename(tmp, dest)
}

// header replaces a SKILL.md's front matter with one that names the skill
// as installed, so the folder and the name match.
func header(k CommunitySkill, src Source, raw []byte) []byte {
	body := strings.TrimPrefix(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\uFEFF")
	if strings.HasPrefix(body, "---\n") {
		if end := strings.Index(body[4:], "\n---"); end >= 0 {
			body = body[4+end+4:]
			body = strings.TrimPrefix(strings.TrimLeft(body, "-"), "\n")
		}
	}
	// One line, and quoted when YAML needs it; inside double quotes a
	// backslash would start an escape, so none is kept.
	desc := strings.Join(strings.Fields(strings.NewReplacer(`"`, "'", `\`, "/").Replace(k.Description)), " ")
	if desc == "" || strings.Contains(desc, ": ") || strings.Contains(desc, " #") || strings.ContainsAny(desc[:1], "'>|&*!%@`[{?,-") {
		desc = `"` + desc + `"`
	}
	return []byte(fmt.Sprintf("---\nname: %s\ndescription: %s\nlicense: %s (from %s; see the license file here)\nmetadata:\n  kind: method\n  source: %s\n  from: https://github.com/%s/tree/%s/%s\n---\n%s",
		k.Name, desc, src.License, src.Name, src.ID, src.Repo, src.Commit, k.Dir, body))
}
