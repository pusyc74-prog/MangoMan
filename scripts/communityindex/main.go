// Command communityindex builds internal/skills/community.json.gz, the list
// of community skills MangoMan offers in its library, from local clones of
// the source repositories at their pinned commits. A skill is listed only if
// its SKILL.md reads cleanly, every file is text, it is not tied to Claude
// Code's own machinery, it is small, and skills.Scan finds nothing in any of
// its files. Each file's SHA-256 is recorded, so adding a skill downloads
// exactly what was checked here.
//
// Usage: go run ./scripts/communityindex ecc=<clone> superpowers=<clone> anthropic=<clone> rezvani=<clone>
package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/pusyc74-prog/mangoman/internal/skills"
)

// The sources, pinned. To update one, clone it at a newer commit, change the
// commit here and run this again.
var sources = []skills.Source{
	{ID: "ecc", Name: "everything-claude-code", Repo: "affaan-m/everything-claude-code", Commit: "25278e7d7b6cf6e6a4e7a6c8e10f14ff22f0eb5b", Root: "skills", License: "MIT", LicenseFile: "LICENSE"},
	{ID: "superpowers", Name: "superpowers", Repo: "obra/superpowers", Commit: "2ce8aa15d05bd57213e6f746f953aafb7ded7a8b", Root: "skills", License: "MIT", LicenseFile: "LICENSE"},
	{ID: "anthropic", Name: "Anthropic skills", Repo: "anthropics/skills", Commit: "dbd4588f9e1033efb41dad4bef2f7947c8993d44", Root: "skills", License: "Apache-2.0"},
	{ID: "rezvani", Name: "claude-skills", Repo: "alirezarezvani/claude-skills", Commit: "5434623a788ec3105d238328cfe554e1e2242ff7", Root: "", License: "MIT", LicenseFile: "LICENSE"},
}

var (
	textExt = map[string]bool{".md": true, ".py": true, ".sh": true, ".js": true, ".mjs": true, ".cjs": true, ".ts": true,
		".json": true, ".txt": true, ".yaml": true, ".yml": true, ".html": true, ".css": true, ".sql": true, ".csv": true, ".toml": true}
	// Built for Claude Code's own settings, hooks or plugins: they do nothing in OpenCode.
	claudeOnly = regexp.MustCompile(`(?i)~/\.claude\b|\.claude/(settings|hooks|commands|agents)|hooks\.json|/plugin (install|marketplace)|claude plugin\b`)
	envNeed    = regexp.MustCompile(`\b[A-Z][A-Z0-9_]*(API_KEY|_TOKEN|_SECRET)\b`)
	installs   = regexp.MustCompile(`(?i)\b(pip3? install|npm (install|i)\b|npx |brew install|apt(-get)? install)`)
	slugRe     = regexp.MustCompile(`[^a-z0-9]+`)
)

const maxSkillBytes, maxFiles = 512 << 10, 60

func main() {
	clones := map[string]string{}
	for _, a := range os.Args[1:] {
		id, dir, ok := strings.Cut(a, "=")
		if !ok {
			fail("arguments are id=clone-folder")
		}
		clones[id] = dir
	}
	idx := skills.CommunityIndex{}
	why := map[string]int{}
	for _, src := range sources {
		dir := clones[src.ID]
		if dir == "" {
			fail("no clone given for " + src.ID)
		}
		if head := strings.TrimSpace(run(dir, "git", "rev-parse", "HEAD")); head != src.Commit {
			fail(fmt.Sprintf("%s is at %s, not the pinned %s", src.ID, head, src.Commit))
		}
		if src.LicenseFile != "" {
			raw, err := os.ReadFile(filepath.Join(dir, src.LicenseFile))
			if err != nil {
				fail(err.Error())
			}
			src.LicenseText = string(raw)
		}
		idx.Sources = append(idx.Sources, src)
		seen := map[string]bool{}
		root := filepath.Join(dir, src.Root)
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && p != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir // tool mirrors (.gemini, .cursor) and git
			}
			if d.IsDir() || d.Name() != "SKILL.md" {
				return nil
			}
			k, reason := skill(src, dir, filepath.Dir(p))
			if reason == "" && seen[k.Name] {
				reason = "same name twice"
			}
			if reason != "" {
				why[reason]++
				return nil
			}
			seen[k.Name] = true
			idx.Skills = append(idx.Skills, k)
			return nil
		})
	}
	sort.Slice(idx.Skills, func(i, j int) bool { return idx.Skills[i].Name < idx.Skills[j].Name })
	raw, _ := json.Marshal(idx)
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	zw.Write(raw)
	zw.Close()
	if err := os.WriteFile("internal/skills/community.json.gz", buf.Bytes(), 0o644); err != nil {
		fail(err.Error())
	}
	fmt.Printf("listed %d skills (%d KB); left out: %v\n", len(idx.Skills), buf.Len()>>10, why)
}

// skill reads one skill folder; reason says why it is left out ("" when listed).
func skill(src skills.Source, repo, dir string) (skills.CommunitySkill, string) {
	var k skills.CommunitySkill
	raw, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	name, desc, ok := frontmatter(string(raw))
	if !ok || name == "" || desc == "" || utf8.RuneCountInString(desc) > 1024 {
		return k, "no clear name or description"
	}
	if claudeOnly.MatchString(string(raw)) {
		return k, "built for Claude Code itself"
	}
	rel, _ := filepath.Rel(repo, dir)
	k = skills.CommunitySkill{Name: strings.Trim(src.ID+"-"+slugRe.ReplaceAllString(strings.ToLower(name), "-"), "-"),
		Orig: name, Source: src.ID, Dir: filepath.ToSlash(rel), Description: desc}
	if len(k.Name) > 64 {
		return k, "name too long"
	}
	var total int64
	needs := map[string]bool{}
	bad := ""
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || bad != "" {
			return err
		}
		if d.IsDir() {
			if p != dir {
				if strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				if _, err := os.Stat(filepath.Join(p, "SKILL.md")); err == nil {
					return filepath.SkipDir // a skill inside this one is listed on its own
				}
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 || !textExt[strings.ToLower(path.Ext(d.Name()))] && d.Name() != "LICENSE" {
			bad = "has files that are not text"
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil || !utf8.Valid(b) {
			bad = "has files that are not text"
			return nil
		}
		if f := skills.Scan(string(b)); len(f) > 0 {
			bad = "the safety scan found something"
			return nil
		}
		for _, m := range envNeed.FindAllString(string(b), -1) {
			needs[m+" set"] = true
		}
		if installs.Match(b) {
			needs["installs packages"] = true
		}
		total += int64(len(b))
		sum := sha256.Sum256(b)
		r, _ := filepath.Rel(dir, p)
		k.Files = append(k.Files, skills.CommunityFile{Path: filepath.ToSlash(r), Size: int64(len(b)), Sum: hex.EncodeToString(sum[:])})
		return nil
	})
	switch {
	case bad != "":
		return k, bad
	case total > maxSkillBytes || len(k.Files) > maxFiles:
		return k, "too big"
	case src.ID == "anthropic" && !apache(dir):
		return k, "not under an open license"
	}
	for n := range needs {
		k.Needs = append(k.Needs, n)
	}
	sort.Strings(k.Needs)
	return k, ""
}

// apache reports whether an Anthropic skill folder carries the Apache
// License (its document skills are source-available only).
func apache(dir string) bool {
	raw, err := os.ReadFile(filepath.Join(dir, "LICENSE.txt"))
	return err == nil && bytes.Contains(raw, []byte("Apache License"))
}

// frontmatter reads name and description, including a description written
// over several lines (YAML > or |).
func frontmatter(s string) (name, desc string, ok bool) {
	s = strings.TrimPrefix(strings.ReplaceAll(s, "\r\n", "\n"), "\uFEFF")
	if !strings.HasPrefix(s, "---\n") {
		return "", "", false
	}
	end := strings.Index(s[4:], "\n---")
	if end < 0 {
		return "", "", false
	}
	lines := strings.Split(s[4:4+end], "\n")
	for i := 0; i < len(lines); i++ {
		k, v, found := strings.Cut(lines[i], ":")
		if !found || strings.HasPrefix(k, " ") {
			continue
		}
		v = strings.TrimSpace(v)
		if v == ">" || v == "|" || v == ">-" || v == "|-" || v == "" {
			var parts []string
			for i+1 < len(lines) && (strings.HasPrefix(lines[i+1], " ") || strings.TrimSpace(lines[i+1]) == "") {
				i++
				if t := strings.TrimSpace(lines[i]); t != "" {
					parts = append(parts, t)
				}
			}
			v = strings.Join(parts, " ")
		}
		v = strings.Trim(v, `"'`)
		switch k {
		case "name":
			name = v
		case "description":
			desc = v
		}
	}
	return name, desc, true
}

func run(dir string, args ...string) string {
	out, err := exec.Command(args[0], append([]string{"-C", dir}, args[1:]...)...).Output()
	if err != nil {
		fail(fmt.Sprintf("%v: %v", args, err))
	}
	return string(out)
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
