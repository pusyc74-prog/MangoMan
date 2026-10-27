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
	{ID: "anthropic", Name: "Anthropic skills", Repo: "anthropics/skills", Commit: "dbd4588f9e1033efb41dad4bef2f7947c8993d44", Root: "skills"}, // each skill carries its own license file
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
	idx := skills.CommunityIndex{Licenses: map[string]string{"Apache-2.0": apacheText(clones["anthropic"])}}
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
	fm, ok := frontmatter(string(raw))
	name, desc := fm["name"], fm["description"]
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
	files := map[string]bool{} // the licenses of the skill's own license files ("" when not MIT or Apache)
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
			needs["packages you install yourself (MangoMan's agents do not install any)"] = true
		}
		if strings.HasPrefix(strings.ToUpper(d.Name()), "LICENSE") {
			files[licenseOf(string(b))] = true
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
	}
	// A license file in a folder above the skill's is not downloaded with it:
	// the skill is listed only when that file is the repository's own.
	for up := filepath.Dir(dir); up != repo && strings.HasPrefix(up, repo); up = filepath.Dir(up) {
		found, _ := filepath.Glob(filepath.Join(up, "[Ll][Ii][Cc][Ee][Nn][Ss][Ee]*"))
		for _, f := range found {
			if b, _ := os.ReadFile(f); strings.Join(strings.Fields(string(b)), " ") != strings.Join(strings.Fields(src.LicenseText), " ") {
				return k, "its license is outside its folder"
			}
		}
	}
	// The skill's own license (its front matter, its license files) comes
	// before the repository's. Only MIT and Apache-2.0 are passed on.
	lic := strings.Trim(fm["license"], `"' `)
	if lic != "" && !open[lic] && !strings.HasPrefix(lic, "Complete terms in LICENSE") {
		return k, "not under MIT or Apache-2.0"
	}
	if files[""] || len(files) > 1 {
		return k, "not under MIT or Apache-2.0"
	}
	for f := range files {
		if open[lic] && f != lic {
			return k, "license unclear"
		}
		lic = f
	}
	if !open[lic] {
		lic = src.License // the anthropic source has none: each skill must carry its own
	}
	if !open[lic] {
		return k, "not under MIT or Apache-2.0"
	}
	if lic != src.License {
		k.License = lic
	}
	for n := range needs {
		k.Needs = append(k.Needs, n)
	}
	sort.Strings(k.Needs)
	return k, ""
}

// apacheText is the Apache License 2.0's terms, from an Anthropic skill's
// license file, for skills that are under it in a repository that is not.
func apacheText(clone string) string {
	raw, err := os.ReadFile(filepath.Join(clone, "skills", "skill-creator", "LICENSE.txt"))
	t, _, ok := strings.Cut(string(raw), "END OF TERMS AND CONDITIONS")
	if err != nil || !ok || licenseOf(t) != "Apache-2.0" {
		fail("no Apache License text in the anthropic clone")
	}
	return t + "END OF TERMS AND CONDITIONS\n"
}

var open = map[string]bool{"MIT": true, "Apache-2.0": true}

// licenseOf names the license a license file holds: MIT, Apache-2.0, or ""
// for anything else (source-available terms, share-alike, two layers).
func licenseOf(t string) string {
	t = strings.Join(strings.Fields(t), " ")
	switch {
	case strings.HasPrefix(t, "MIT License") && strings.Contains(t, "Permission is hereby granted, free of charge"):
		return "MIT"
	case strings.HasPrefix(t, "Apache License Version 2.0, January 2004"):
		return "Apache-2.0"
	}
	return ""
}

// frontmatter reads the top-level keys of a SKILL.md's YAML front matter as
// YAML does for the forms skills use: plain values (also over several
// lines), single and double quotes, and > or | blocks. Each value comes
// back on one line, its spaces collapsed.
func frontmatter(s string) (map[string]string, bool) {
	s = strings.TrimPrefix(strings.ReplaceAll(s, "\r\n", "\n"), "\uFEFF")
	if !strings.HasPrefix(s, "---\n") {
		return nil, false
	}
	end := strings.Index(s[4:], "\n---")
	if end < 0 {
		return nil, false
	}
	lines := strings.Split(s[4:4+end], "\n")
	out := map[string]string{}
	for i := 0; i < len(lines); i++ {
		k, v, found := strings.Cut(lines[i], ":")
		if !found || k == "" || k[0] == ' ' || k[0] == '#' || strings.ContainsAny(k, " \t") {
			continue
		}
		v = strings.TrimSpace(v)
		// The lines that belong to this key: the indented (or blank) ones after it.
		j := i + 1
		for j < len(lines) && (strings.HasPrefix(lines[j], " ") || strings.HasPrefix(lines[j], "\t") || strings.TrimSpace(lines[j]) == "") {
			j++
		}
		more := lines[i+1 : j]
		i = j - 1
		switch {
		case v == "" || v[0] == '>' || v[0] == '|':
			v = strings.Join(more, " ")
		case v[0] == '"' || v[0] == '\'':
			v = quoted(strings.Join(append([]string{v}, more...), "\n"))
		default:
			if c := strings.Index(v, " #"); c >= 0 {
				v = v[:c]
			}
			v = strings.Join(append([]string{v}, more...), " ")
		}
		out[k] = strings.Join(strings.Fields(v), " ")
	}
	return out, true
}

// quoted reads a YAML quoted value from its opening quote; what follows the
// closing quote is ignored. Inside double quotes, a JSON-style escape reads
// as JSON does.
func quoted(v string) string {
	q, b := v[0], strings.Builder{}
	for i := 1; i < len(v); i++ {
		c := v[i]
		switch {
		case c == q && q == '\'' && i+1 < len(v) && v[i+1] == '\'':
			b.WriteByte('\'')
			i++
		case c == q:
			return b.String()
		case c == '\\' && q == '"' && i+1 < len(v):
			n := 2
			if v[i+1] == 'u' {
				n = 6
			}
			var r string
			if i+n <= len(v) && json.Unmarshal([]byte(`"`+v[i:i+n]+`"`), &r) == nil {
				b.WriteString(r)
				i += n - 1
			} else {
				b.WriteByte(c)
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
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
