package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Finding is one problem the review found in an agent's files.
type Finding struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Level  string `json:"level"` // "block" stops listing; "check" needs a reviewer's look
	Reason string `json:"reason"`
}

var reviewRules = []struct {
	re     *regexp.Regexp
	level  string
	reason string
}{
	{regexp.MustCompile(`\bimport\s+ctypes|\bfrom\s+ctypes\b|\bcffi\b`), "block", "loads native code (ctypes or cffi), which the sandbox cannot watch"},
	{regexp.MustCompile(`(^|[^.\w])(exec|eval|compile)\s*\(`), "block", "runs code built at run time"},
	{regexp.MustCompile(`__import__\s*\(|importlib\.import_module\s*\(`), "check", "imports modules by name at run time"},
	{regexp.MustCompile(`\bmarshal\.loads|\bpickle\.loads?\b|\bzlib\.decompress\b.*\bexec`), "block", "unpacks hidden code"},
	{regexp.MustCompile(`[A-Za-z0-9+/]{400,}={0,2}`), "check", "contains a long encoded blob"},
	{regexp.MustCompile(`\bsys\.addaudithook|\bsys\.settrace|\bsys\.setprofile|sitecustomize`), "block", "tampers with the sandbox"},
	{regexp.MustCompile(`\bos\.system\s*\(|shell\s*=\s*True`), "block", "starts a shell"},
	{regexp.MustCompile(`expanduser\s*\(\s*["']~|Path\.home\s*\(|os\.environ\[\s*["']HOME`), "check", "reads the user's home folder"},
}

var hostInCode = regexp.MustCompile(`https?://([a-zA-Z0-9.-]+)`)

// Review scans an agent folder before it can list in the marketplace. It
// returns every finding; any "block" finding stops the listing.
func Review(dir string) (Manifest, []Finding, error) {
	m, err := Load(dir)
	if err != nil {
		return m, nil, err
	}
	var out []Finding
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		ext := strings.ToLower(filepath.Ext(p))
		switch ext {
		case ".so", ".dll", ".dylib", ".exe", ".pyc", ".pyd", ".bin":
			out = append(out, Finding{rel, 0, "block", "ships a compiled program; agents ship readable scripts only"})
			return nil
		case ".py", ".sh", ".js", ".md", ".json", ".txt", ".html", ".css", ".csv":
		default:
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if ext == ".py" || ext == ".sh" || ext == ".js" {
				for _, r := range reviewRules {
					if r.re.MatchString(line) {
						out = append(out, Finding{rel, i + 1, r.level, r.reason})
					}
				}
			}
			if ext == ".py" || ext == ".js" || ext == ".sh" {
				for _, h := range hostInCode.FindAllStringSubmatch(line, -1) {
					if !hostAllowed(strings.ToLower(h[1]), m.Permissions.Network) {
						out = append(out, Finding{rel, i + 1, "block", fmt.Sprintf("reaches %s, which agent.json does not declare", h[1])})
					}
				}
			}
		}
		return nil
	})
	sort.SliceStable(out, func(i, j int) bool { return out[i].Level == "block" && out[j].Level != "block" })
	return m, out, err
}

func hostAllowed(h string, allowed []string) bool {
	for _, a := range allowed {
		if h == a || (strings.HasPrefix(a, "*.") && strings.HasSuffix(h, a[1:])) {
			return true
		}
	}
	return false
}
