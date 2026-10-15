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
	"net/url"
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

//go:embed journey.py
var journeyPy []byte

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
	b, err := runScript(ctx, "crawl.py", crawlPy, url, fmt.Sprint(pages), shots)
	if err != nil {
		return nil, nil, err
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

// Step is one step of a journey, a path a visitor takes through a site.
// Exactly one of Go, Click, Fill or See is set.
type Step struct {
	Go    string `json:"go,omitempty"`    // open this address or path
	Click string `json:"click,omitempty"` // click the button or link with this text
	Fill  string `json:"fill,omitempty"`  // type With into the field with this label or placeholder
	With  string `json:"with,omitempty"`
	See   string `json:"see,omitempty"` // this text must appear on the page
}

// Valid reports what is wrong with a step, or "".
func (s Step) Valid() string {
	n := 0
	for _, v := range []string{s.Go, s.Click, s.Fill, s.See} {
		if strings.TrimSpace(v) != "" {
			n++
		}
	}
	switch {
	case n != 1:
		return "each step needs exactly one of go, click, fill or see"
	case s.Fill != "" && s.With == "":
		return "a fill step needs the text to type (with)"
	case s.Fill == "" && s.With != "":
		return "with goes only with fill"
	}
	return ""
}

// CheckSteps checks a journey before a browser walks it: every step valid,
// every page opened on the site itself (http or https, same host as base),
// and no click that would pay or order for real.
func CheckSteps(base string, steps []Step) error {
	b, err := url.Parse(base)
	if err != nil || (b.Scheme != "http" && b.Scheme != "https") || b.Host == "" {
		return fmt.Errorf("the journey's site %q is not a web address", base)
	}
	for i, st := range steps {
		if why := st.Valid(); why != "" {
			return fmt.Errorf("step %d: %s", i+1, why)
		}
		if st.Go != "" {
			u, err := b.Parse(st.Go)
			if err != nil || u.Scheme != b.Scheme || u.Host != b.Host {
				return fmt.Errorf("step %d: a journey only opens pages of %s", i+1, b.Host)
			}
		}
		if realMoney.MatchString(st.Click) {
			return fmt.Errorf("step %d: a journey never clicks %q; it stops before paying or ordering", i+1, st.Click)
		}
	}
	return nil
}

var realMoney = regexp.MustCompile(`(?i)\b(pay|payment|place (my |the )?order|purchase|buy now|confirm (order|payment|booking)|donate|subscribe)\b`)

// Say is the step in plain words.
func (s Step) Say() string {
	switch {
	case s.Go != "":
		return "Open " + s.Go
	case s.Click != "":
		return fmt.Sprintf("Click %q", s.Click)
	case s.Fill != "":
		return fmt.Sprintf("Type %q into %q", s.With, s.Fill)
	}
	return fmt.Sprintf("See %q", s.See)
}

// Journey walks the steps from base in a headless browser. It returns ""
// when every step worked, else which step failed and why; a screenshot of
// the failure goes to shot. A journey that does not start with go starts at base.
func Journey(ctx context.Context, base string, steps []Step, shot string) (string, error) {
	if err := CheckSteps(base, steps); err != nil {
		return "", err
	}
	added := len(steps) == 0 || steps[0].Go == ""
	if added {
		steps = append([]Step{{Go: base}}, steps...) // a journey starts at the site
	}
	in, _ := json.Marshal(steps)
	b, err := runScript(ctx, "journey.py", journeyPy, base, string(in), shot)
	if err != nil {
		return "", err
	}
	var r struct {
		Failed int    `json:"failed"` // 1-based; 0 = all passed
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return "", fmt.Errorf("journey: %v", err)
	}
	if r.Failed < 1 || r.Failed > len(steps) {
		return "", nil
	}
	n := r.Failed
	if added {
		if n == 1 {
			return "the site did not open: " + r.Detail, nil
		}
		n-- // numbered as the owner wrote the steps
	}
	return fmt.Sprintf("step %d (%s) failed: %s", n, steps[r.Failed-1].Say(), r.Detail), nil
}

// runScript runs one of the browser scripts with the computer's own browser
// and returns what it printed.
func runScript(ctx context.Context, name string, body []byte, args ...string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "mangoman-qa")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	script := filepath.Join(dir, name)
	if err := os.WriteFile(script, body, 0o644); err != nil {
		return nil, err
	}
	if err := skills.CopyShared(dir); err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "python3", append([]string{script}, args...)...)
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("browser check failed: %v %s", err, tail(stderr.String(), 400))
	}
	return b, nil
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
