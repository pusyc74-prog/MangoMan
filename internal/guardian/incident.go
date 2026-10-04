package guardian

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Incident is one change on its way to production: a fix for a problem
// Guardian found, or a change the owner asked for. Both take the same path:
// own branch, the permanent development environment, QA, the owner's
// approval, production, QA again.
type Incident struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"`  // fix or change
	Check   string    `json:"check"` // the failed check, or "change"
	Problem string    `json:"problem"`
	Cause   string    `json:"cause,omitempty"`
	Opened  time.Time `json:"opened"`
	// Status: queued (waiting for development to be free), working (Guardian
	// and QA at work in development), ready (passed QA in development,
	// waiting for the owner), deploying, live, rejected, or needs_you.
	Status    string `json:"status"`
	Round     int    `json:"round"`
	Branch    string `json:"branch"`               // the change's own branch, made from dev
	Work      string `json:"work,omitempty"`       // where the change is written
	DevBefore string `json:"dev_before,omitempty"` // dev's commit before the change went in
	Summary   string `json:"summary,omitempty"`
	QA        string `json:"qa,omitempty"`   // the latest QA report
	Note      string `json:"note,omitempty"` // what happened last
}

// maxRounds is how many fix-and-test rounds Guardian tries before it hands
// the problem to the owner.
const maxRounds = 3

func (g *Guardian) incidentsPath() string { return filepath.Join(g.Dir, "incidents.json") }

// Incidents returns every incident, newest first.
func (g *Guardian) Incidents() []Incident {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []Incident
	for _, i := range g.load() {
		out = append(out, *i)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Opened.After(out[b].Opened) })
	return out
}

func (g *Guardian) load() map[string]*Incident {
	m := map[string]*Incident{}
	if b, err := os.ReadFile(g.incidentsPath()); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func (g *Guardian) store(m map[string]*Incident) error {
	b, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(g.incidentsPath(), b, 0o600)
}

// update changes one incident under the lock and saves.
func (g *Guardian) update(id string, f func(*Incident)) (Incident, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	m := g.load()
	i, ok := m[id]
	if !ok {
		return Incident{}, fmt.Errorf("no incident %s", id)
	}
	f(i)
	return *i, g.store(m)
}

// inDev reports whether a status holds the development environment.
func inDev(status string) bool {
	return status == "working" || status == "ready" || status == "deploying"
}

func active(status string) bool { return status == "queued" || inDev(status) }

// Open queues a fix for each failed check that has none under way, and
// returns the new ids. Without a git repo or an agent there is nothing to fix.
func (g *Guardian) Open(events []Event) []string {
	if g.Cfg.Repo == "" || g.Agent == nil {
		return nil
	}
	var ids []string
	for _, e := range events {
		if e.Status == "ok" || e.Check == "dev" { // drift in dev is for the owner, not a code fix
			continue
		}
		if id := g.add(Incident{Kind: "fix", Check: e.Check, Problem: e.Detail, Cause: e.Cause, Opened: e.Time}); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// Request queues a change the owner asked for, in their own words.
func (g *Guardian) Request(what string) (string, error) {
	if g.Cfg.Repo == "" || g.Agent == nil {
		return "", fmt.Errorf("set repo in guardian.json to make changes")
	}
	id := g.add(Incident{Kind: "change", Check: "change", Problem: strings.TrimSpace(what), Opened: time.Now()})
	g.notify(fmt.Sprintf("%s: change %s queued: %s", g.Cfg.App, id, cut(what, 200)))
	return id, nil
}

// add stores a new queued incident; a fix is skipped when one for the same
// check is under way.
func (g *Guardian) add(inc Incident) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	m := g.load()
	for _, i := range m {
		if inc.Kind == "fix" && i.Check == inc.Check && active(i.Status) {
			return ""
		}
	}
	inc.ID = inc.Opened.Format("0102-150405") + "-" + slug(inc.Check)
	inc.Status, inc.Branch = "queued", "guardian/"+inc.ID
	m[inc.ID] = &inc
	if g.store(m) != nil {
		return ""
	}
	return inc.ID
}

// next is the oldest queued incident when development is free, or "".
func (g *Guardian) next() string {
	list := g.Incidents()
	for _, i := range list {
		if inDev(i.Status) {
			return ""
		}
	}
	for k := len(list) - 1; k >= 0; k-- {
		if list[k].Status == "queued" {
			return list[k].ID
		}
	}
	return ""
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

func (g *Guardian) giveUp(id, why string) error {
	inc, err := g.update(id, func(i *Incident) { i.Status, i.Note = "needs_you", why })
	if err == nil {
		g.notify(fmt.Sprintf("%s: Guardian could not finish %s (%s). %s", g.Cfg.App, inc.ID, cut(inc.Problem, 200), why))
	}
	return err
}

func workPrompt(inc Incident) string {
	var p string
	if inc.Kind == "change" {
		p = "You are Guardian, making a change the owner of this app asked for:\n" + inc.Problem + "\nBuild it well, in the project's style, with the smallest change that does it.\n"
	} else {
		p = fmt.Sprintf("You are Guardian, fixing a problem in this project's live app.\nProblem (check %q): %s\n", inc.Check, inc.Problem)
		if inc.Cause != "" {
			p += "First guess at the cause: " + inc.Cause + "\n"
		}
		p += "Find the root cause in the code, then make the smallest change that fixes it.\n"
	}
	if inc.QA != "" {
		p += "Your last attempt failed QA. The QA report:\n" + cut(inc.QA, 6000) + "\n"
	}
	return p + `Do not touch secrets, deployment settings or unrelated code.
Then write GUARDIAN_FIX.md in the project root, in plain short sentences: the root cause (for a fix), what you changed, the risk, and how to undo it.`
}

func testPrompt(inc Incident, summary string) string {
	return fmt.Sprintf(`You are the QA agent. Guardian made this %s: %s
What Guardian says it did:
%s
Write automated tests, next to the project's existing tests and in their style, that check it (for a fix: a test that fails without it).
Change only test files. Then run the project's tests.`, inc.Kind, inc.Problem, summary)
}

var testFile = regexp.MustCompile(`(?i)(^|/)(tests?|spec|__tests__)/|(_test\.go|\.test\.[jt]sx?|\.spec\.[jt]sx?|(^|/)test_[^/]*\.py|_test\.py)$`)

// keepTestsOnly undoes anything the QA agent changed outside test files.
func keepTestsOnly(work string) {
	out, _ := git(work, "status", "--porcelain")
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		f := strings.Trim(line[3:], `"`)
		if testFile.MatchString(f) {
			continue
		}
		if line[:2] == "??" {
			_ = os.RemoveAll(filepath.Join(work, f))
		} else {
			_, _ = git(work, "checkout", "--", f)
		}
	}
}

// incidentLines is the incidents part of a report.
func (g *Guardian) incidentLines() string {
	var b strings.Builder
	for _, i := range g.Incidents() {
		what := i.Check
		if i.Kind == "change" {
			what = "change " + cut(i.Problem, 60)
		}
		switch {
		case i.Status == "ready":
			fmt.Fprintf(&b, "Waiting for your approval: %s (%s).\n", what, i.ID)
		case i.Status == "needs_you":
			fmt.Fprintf(&b, "Needs you: %s. %s\n", what, i.Note)
		case i.Status == "queued":
			fmt.Fprintf(&b, "Queued: %s.\n", what)
		case inDev(i.Status):
			fmt.Fprintf(&b, "Guardian is working on %s (round %d).\n", what, i.Round)
		case time.Since(i.Opened) < 24*time.Hour:
			fmt.Fprintf(&b, "%s: %s.\n", what, i.Note)
		}
	}
	return b.String()
}

func git(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Guardian", "-c", "user.email=guardian@mangoman.local"}, args...)...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %s", args[0], cut(strings.TrimSpace(string(out)), 300))
	}
	return strings.TrimSpace(string(out)), nil
}

func commit(work, msg string) {
	_, _ = git(work, "add", "-A")
	_, _ = git(work, "commit", "-q", "-m", msg)
}

func readAndRemove(path string) string {
	b, _ := os.ReadFile(path)
	_ = os.Remove(path)
	return strings.TrimSpace(string(b))
}

func (g *Guardian) logf(format string, args ...any) {
	if g.Logf != nil {
		g.Logf(format, args...)
	}
}
