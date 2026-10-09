// Package qa is the QA agent: it runs a project's own tests, clicks through
// its web app in a real browser, and writes a bug report with steps to
// reproduce each problem. A model may add likely causes; it never decides
// what passed.
package qa

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/skills"
)

//go:embed crawl.py
var crawlPy []byte

// Suite is one test command found in a project.
type Suite struct {
	Name string   `json:"name"`
	Cmd  []string `json:"cmd"`
}

var makeTest = regexp.MustCompile(`(?m)^test:`)

// Detect finds the project's test commands from its files.
func Detect(dir string) []Suite {
	has := func(name string) bool { _, err := os.Stat(filepath.Join(dir, name)); return err == nil }
	read := func(name string) string { b, _ := os.ReadFile(filepath.Join(dir, name)); return string(b) }
	var out []Suite
	if has("go.mod") {
		out = append(out, Suite{"Go", []string{"go", "test", "./..."}})
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal([]byte(read("package.json")), &pkg) == nil && pkg.Scripts["test"] != "" && !strings.Contains(pkg.Scripts["test"], "no test specified") {
		out = append(out, Suite{"npm", []string{"npm", "test", "--silent"}})
	}
	if has("pytest.ini") || has("conftest.py") || strings.Contains(read("pyproject.toml"), "pytest") {
		out = append(out, Suite{"pytest", []string{"python3", "-m", "pytest", "-q"}})
	}
	if has("Cargo.toml") {
		out = append(out, Suite{"Rust", []string{"cargo", "test"}})
	}
	if len(out) == 0 && makeTest.MatchString(read("Makefile")) {
		out = append(out, Suite{"make", []string{"make", "test"}})
	}
	return out
}

// Result is one suite's run.
type Result struct {
	Suite   Suite         `json:"suite"`
	OK      bool          `json:"ok"`
	Output  string        `json:"output"` // the end of the output, where failures are reported
	Elapsed time.Duration `json:"elapsed"`
}

// RunTests runs each suite in dir.
func RunTests(ctx context.Context, dir string, suites []Suite) []Result {
	var out []Result
	for _, s := range suites {
		start := time.Now()
		cmd := exec.CommandContext(ctx, s.Cmd[0], s.Cmd[1:]...)
		cmd.Dir = dir
		b, err := cmd.CombinedOutput()
		out = append(out, Result{Suite: s, OK: err == nil, Output: tail(string(b), 3000), Elapsed: time.Since(start).Round(time.Millisecond)})
	}
	return out
}

// Finding is one problem seen in the browser.
type Finding struct {
	Page   string   `json:"page"`
	Kind   string   `json:"kind"` // console_error, page_error, http_error, broken_image, phone_overflow, unreachable
	Detail string   `json:"detail"`
	Steps  []string `json:"steps"`
	Shot   string   `json:"screenshot,omitempty"`
}

// Crawl opens url in a headless browser, follows links on the same site up
// to pages pages, and reports what went wrong. Screenshots go to shots.
// It needs Python with Playwright.
func Crawl(ctx context.Context, url string, pages int, shots string) (visited []string, found []Finding, err error) {
	dir, err := os.MkdirTemp("", "mangoman-qa")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)
	script := filepath.Join(dir, "crawl.py")
	if err := os.WriteFile(script, crawlPy, 0o644); err != nil {
		return nil, nil, err
	}
	if err := skills.CopyShared(dir); err != nil {
		return nil, nil, err
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "python3", script, url, fmt.Sprint(pages), shots)
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("browser check failed: %v %s", err, tail(stderr.String(), 400))
	}
	var r struct {
		Visited  []string  `json:"visited"`
		Findings []Finding `json:"findings"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, nil, fmt.Errorf("browser check: %v", err)
	}
	return r.Visited, r.Findings, nil
}

// Report is the bug report as markdown. causes is the model's note on
// likely causes, or "".
func Report(results []Result, visited []string, found []Finding, causes string) string {
	var b strings.Builder
	failed := 0
	for _, r := range results {
		if !r.OK {
			failed++
		}
	}
	fmt.Fprintf(&b, "# QA report\n\n%s.", time.Now().Format("2 Jan 2006 15:04"))
	if len(results) > 0 {
		fmt.Fprintf(&b, " Tests: %d of %d suites passed.", len(results)-failed, len(results))
	}
	if len(visited) > 0 {
		fmt.Fprintf(&b, " Browser: %d pages, %d problems.", len(visited), len(found))
	}
	b.WriteString("\n")
	for _, r := range results {
		state := "passed"
		if !r.OK {
			state = "FAILED"
		}
		fmt.Fprintf(&b, "\n## %s tests %s (%s)\n\nRun: `%s`\n", r.Suite.Name, state, r.Elapsed, strings.Join(r.Suite.Cmd, " "))
		if !r.OK {
			fmt.Fprintf(&b, "\n```\n%s\n```\n", strings.TrimSpace(r.Output))
		}
	}
	for i, f := range found {
		fmt.Fprintf(&b, "\n## Bug %d: %s on %s\n\n%s\n\nSteps to reproduce:\n", i+1, strings.ReplaceAll(f.Kind, "_", " "), f.Page, f.Detail)
		for j, s := range f.Steps {
			fmt.Fprintf(&b, "%d. %s\n", j+1, s)
		}
		if f.Shot != "" {
			fmt.Fprintf(&b, "\nScreenshot: %s\n", f.Shot)
		}
	}
	if causes != "" {
		fmt.Fprintf(&b, "\n## Likely causes (from a model; check before acting)\n\n%s\n", strings.TrimSpace(causes))
	}
	return b.String()
}

// Problems lists every failure in short form, for a model to explain.
func Problems(results []Result, found []Finding) string {
	var b strings.Builder
	for _, r := range results {
		if !r.OK {
			fmt.Fprintf(&b, "- %s tests failed:\n%s\n", r.Suite.Name, tail(r.Output, 1500))
		}
	}
	for _, f := range found {
		fmt.Fprintf(&b, "- %s on %s: %s\n", f.Kind, f.Page, f.Detail)
	}
	return b.String()
}

func tail(s string, n int) string {
	if len(s) > n {
		return "..." + s[len(s)-n:]
	}
	return s
}
