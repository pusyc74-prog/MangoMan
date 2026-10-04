package guardian

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/qa"
)

// Incident is one problem on its way from found to fixed in production.
type Incident struct {
	ID      string    `json:"id"`
	Check   string    `json:"check"`
	Problem string    `json:"problem"`
	Cause   string    `json:"cause,omitempty"`
	Opened  time.Time `json:"opened"`
	// Status: fixing (Guardian and QA at work in development), ready
	// (passed QA in development, waiting for the owner's approval),
	// deploying, live, rejected, or needs_you (Guardian could not fix it).
	Status  string `json:"status"`
	Round   int    `json:"round"`
	Base    string `json:"base"`   // the commit the fix starts from
	Branch  string `json:"branch"` // the fix's branch
	Work    string `json:"work"`   // the development copy of the code
	Summary string `json:"summary,omitempty"`
	QA      string `json:"qa,omitempty"`   // the latest QA report
	Note    string `json:"note,omitempty"` // what happened last
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
	b, _ := json.MarshalIndent(m, "", "  ")
	return *i, os.WriteFile(g.incidentsPath(), b, 0o600)
}

func open(status string) bool {
	return status == "fixing" || status == "ready" || status == "deploying"
}

// Open starts an incident for each failed check that has none open yet, and
// returns the new ids. Without a git repo or an agent there is nothing to fix.
func (g *Guardian) Open(events []Event) []string {
	if g.Cfg.Repo == "" || g.Agent == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	m := g.load()
	var ids []string
	for _, e := range events {
		if e.Status == "ok" {
			continue
		}
		busy := false
		for _, i := range m {
			busy = busy || i.Check == e.Check && open(i.Status)
		}
		if busy {
			continue
		}
		id := e.Time.Format("0102-1504") + "-" + slug(e.Check)
		m[id] = &Incident{ID: id, Check: e.Check, Problem: e.Detail, Cause: e.Cause, Opened: e.Time, Status: "fixing", Branch: "guardian/" + id}
		ids = append(ids, id)
	}
	if len(ids) > 0 {
		b, _ := json.MarshalIndent(m, "", "  ")
		_ = os.WriteFile(g.incidentsPath(), b, 0o600)
	}
	return ids
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// Fix runs the fix-and-test rounds for an incident until QA passes in
// development (then it asks the owner) or the rounds run out.
func (g *Guardian) Fix(ctx context.Context, id string) error {
	inc, err := g.update(id, func(*Incident) {})
	if err != nil {
		return err
	}
	if inc.Work == "" {
		base, err := git(g.Cfg.Repo, "rev-parse", "HEAD")
		if err != nil {
			return g.giveUp(id, "the project is not a git repository with a commit: "+err.Error())
		}
		work := filepath.Join(g.Work, id)
		if _, err := git(g.Cfg.Repo, "worktree", "add", "-b", inc.Branch, work, base); err != nil {
			return g.giveUp(id, "could not make a development copy: "+err.Error())
		}
		inc, _ = g.update(id, func(i *Incident) { i.Work, i.Base = work, base })
	}
	for inc.Round < maxRounds {
		inc, _ = g.update(id, func(i *Incident) { i.Round++; i.Status = "fixing" })
		g.logf("%s: round %d, finding the root cause and fixing", id, inc.Round)
		if err := g.Agent(ctx, inc.Work, fixPrompt(inc)); err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		summary := readAndRemove(filepath.Join(inc.Work, "GUARDIAN_FIX.md"))
		commit(inc.Work, "Guardian: fix "+inc.Check)
		g.logf("%s: QA agent writing a test for the bug", id)
		if err := g.Agent(ctx, inc.Work, testPrompt(inc, summary)); err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		keepTestsOnly(inc.Work)
		commit(inc.Work, "QA: test for "+inc.Check)
		report, ok := g.devQA(ctx, inc)
		if ok {
			if _, err := git(inc.Work, "diff", "--quiet", inc.Base, "HEAD"); err == nil {
				ok, report = false, report+"\nNo change was made to the code."
			}
		}
		inc, _ = g.update(id, func(i *Incident) { i.QA, i.Summary = report, summary })
		if ok {
			inc, _ = g.update(id, func(i *Incident) { i.Status, i.Note = "ready", "passed QA in development" })
			g.askApproval(inc)
			return nil
		}
		g.logf("%s: QA failed in development", id)
	}
	return g.giveUp(id, fmt.Sprintf("QA still failed after %d rounds. The fix so far is on branch %s", maxRounds, inc.Branch))
}

func (g *Guardian) giveUp(id, why string) error {
	inc, err := g.update(id, func(i *Incident) { i.Status, i.Note = "needs_you", why })
	if err == nil {
		g.notify(fmt.Sprintf("%s: Guardian could not fix %q (%s). %s", g.Cfg.App, inc.Check, inc.Problem, why))
	}
	return err
}

func fixPrompt(inc Incident) string {
	p := fmt.Sprintf(`You are Guardian, fixing a problem in this project's live app.
Problem (check %q): %s
`, inc.Check, inc.Problem)
	if inc.Cause != "" {
		p += "First guess at the cause: " + inc.Cause + "\n"
	}
	if inc.QA != "" {
		p += "Your last fix failed QA. The QA report:\n" + cut(inc.QA, 6000) + "\n"
	}
	return p + `Find the root cause in the code, then make the smallest change that fixes it. Do not touch secrets, deployment settings or unrelated code.
Then write GUARDIAN_FIX.md in the project root, in plain short sentences: the root cause, what you changed, the risk, and how to undo it.`
}

func testPrompt(inc Incident, summary string) string {
	return fmt.Sprintf(`You are the QA agent. Guardian fixed this problem: %s (check %q).
What Guardian says it did:
%s
Write one automated test, next to the project's existing tests and in their style, that fails without this fix and passes with it.
Change only test files. Then run the project's tests.`, inc.Problem, inc.Check, summary)
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

// devQA starts a development copy of the fix and has the QA agent test it.
func (g *Guardian) devQA(ctx context.Context, inc Incident) (string, bool) {
	url, stop, err := g.startDev(ctx, inc)
	if err != nil {
		return "The development copy did not start: " + err.Error(), false
	}
	defer stop()
	return g.qa(ctx, inc.Work, url)
}

// qa runs the project's tests in dir and clicks through url (if any).
// Passing needs at least one of the two, and no problem in either.
func (g *Guardian) qa(ctx context.Context, dir, url string) (string, bool) {
	var results []qa.Result
	if dir != "" {
		results = qa.RunTests(ctx, dir, qa.Detect(dir))
	}
	visited, found, note := g.browse(ctx, url)
	ok := qa.Problems(results, found) == "" && (len(results) > 0 || len(visited) > 0)
	if len(results) == 0 && len(visited) == 0 {
		note += "\nNothing could be tested: add tests to the project or a development address."
	}
	return qa.Report(results, visited, found, "") + note, ok
}

// startDev makes the development copy reachable: a start command on a free
// port, or a deploy command (a preview link). Live secrets are left out.
func (g *Guardian) startDev(ctx context.Context, inc Incident) (string, func(), error) {
	d := g.Cfg.Dev
	port := freePort()
	expand := strings.NewReplacer("{port}", strconv.Itoa(port), "{branch}", slug(inc.Branch), "{id}", inc.ID).Replace
	env := devEnv(d.Env, port)
	url := expand(d.URL)
	switch {
	case d.Start != "":
		if url == "" {
			url = fmt.Sprintf("http://127.0.0.1:%d/", port)
		}
		cmd := shellCmd(context.Background(), expand(d.Start))
		cmd.Dir, cmd.Env = inc.Work, env
		group(cmd)
		if err := cmd.Start(); err != nil {
			return "", nil, err
		}
		stop := func() { stopGroup(cmd); _ = cmd.Wait() }
		if err := waitUp(ctx, url, 90*time.Second); err != nil {
			stop()
			return "", nil, err
		}
		return url, stop, nil
	case d.Deploy != "":
		cmd := shellCmd(ctx, expand(d.Deploy))
		cmd.Dir, cmd.Env = inc.Work, env
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", nil, fmt.Errorf("deploy failed: %s", cut(strings.TrimSpace(string(out)), 300))
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if last := strings.TrimSpace(lines[len(lines)-1]); strings.HasPrefix(last, "http") {
			url = last
		}
		return url, func() {}, nil
	}
	return url, func() {}, nil // tests only (or an address that is always there)
}

var secretName = regexp.MustCompile(`(?i)key|token|secret|passw|private|credential|dsn|database_url|_url$|auth`)

// devEnv is this environment without anything that looks like a secret,
// plus the owner's test values and the port.
func devEnv(test map[string]string, port int) []string {
	var env []string
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); !secretName.MatchString(k) {
			env = append(env, kv)
		}
	}
	for k, v := range test {
		env = append(env, k+"="+v)
	}
	return append(env, "PORT="+strconv.Itoa(port))
}

func freePort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 8787
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// waitUp waits until url answers at all (any status).
func waitUp(ctx context.Context, url string, limit time.Duration) error {
	end := time.Now().Add(limit)
	for time.Now().Before(end) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req); err == nil {
			resp.Body.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("%s did not answer within %s", url, limit)
}

func (g *Guardian) askApproval(inc Incident) {
	stat, _ := git(inc.Work, "diff", "--shortstat", inc.Base, "HEAD")
	text := fmt.Sprintf("%s: a fix is ready for %q.\nProblem: %s\n%s\nChange: %s\nQA passed in development. Approve to make it live.\nIncident %s",
		g.Cfg.App, inc.Check, inc.Problem, strings.TrimSpace(inc.Summary), strings.TrimSpace(stat), inc.ID)
	if g.TG != nil {
		_ = g.TG.Send(text, [2]string{"Approve", "approve:" + inc.ID}, [2]string{"Reject", "reject:" + inc.ID})
	}
	if g.Cfg.Webhook != "" {
		tg := g.TG
		g.TG = nil // already sent with buttons
		g.notify(text + "\nApprove with: mangoman guardian approve " + inc.ID)
		g.TG = tg
	}
	g.logf("%s: ready. Approve with: mangoman guardian approve %s", inc.ID, inc.ID)
}

// Approve makes a fix live: merge, deploy to production, test there. If
// production QA fails it rolls back and Guardian works on it again.
func (g *Guardian) Approve(ctx context.Context, id string) error {
	var was string
	inc, err := g.update(id, func(i *Incident) {
		was = i.Status
		if i.Status == "ready" {
			i.Status = "deploying"
		}
	})
	if err != nil {
		return err
	}
	if was != "ready" {
		return fmt.Errorf("incident %s is %s, not ready to go live", id, was)
	}
	if g.Cfg.Prod.Deploy == "" {
		g.update(id, func(i *Incident) { i.Status = "ready" })
		return errors.New("set prod.deploy in guardian.json to make fixes live")
	}
	if _, err := git(g.Cfg.Repo, "merge", "--no-ff", "--no-edit", inc.Branch); err != nil {
		_, _ = git(g.Cfg.Repo, "merge", "--abort")
		return g.giveUp(id, "the fix no longer merges cleanly: "+err.Error())
	}
	g.notify(fmt.Sprintf("%s: deploying the fix for %q to production.", g.Cfg.App, inc.Check))
	out, err := shell(ctx, g.Cfg.Repo, g.Cfg.Prod.Deploy)
	report, ok := "", err == nil
	if err != nil {
		report = "Deploy failed: " + cut(strings.TrimSpace(out), 500)
	} else {
		time.Sleep(g.Wait)
		report, ok = g.prodQA(ctx)
	}
	if ok {
		g.cleanup(inc)
		g.update(id, func(i *Incident) { i.Status, i.Note, i.QA = "live", "live and passed QA in production", report })
		g.notify(fmt.Sprintf("%s: the fix for %q is live and passed QA in production.", g.Cfg.App, inc.Check))
		return nil
	}
	if g.Cfg.Prod.Rollback != "" {
		_, _ = shell(ctx, g.Cfg.Repo, g.Cfg.Prod.Rollback)
	}
	_, _ = git(g.Cfg.Repo, "revert", "-m", "1", "--no-edit", "HEAD")
	// Start again from the reverted code: a branch that was merged and then
	// reverted would not merge a second time.
	g.cleanup(inc)
	g.update(id, func(i *Incident) {
		i.Status, i.Round, i.QA, i.Work = "fixing", 0, report, ""
		i.Note = "failed QA in production and was rolled back"
	})
	g.notify(fmt.Sprintf("%s: the fix for %q failed QA in production and was rolled back. Guardian is working on it again.", g.Cfg.App, inc.Check))
	return g.Fix(ctx, id)
}

// prodQA re-runs the address and command checks, then clicks through the
// production site.
func (g *Guardian) prodQA(ctx context.Context) (string, bool) {
	var b strings.Builder
	ok := true
	for _, k := range g.Cfg.Checks {
		if k.Log != "" {
			continue // only new log lines count; the next watch run reads them
		}
		if d := g.check(ctx, k, map[string]int64{}); d != "" {
			ok = false
			fmt.Fprintf(&b, "Check %s failed: %s\n", k.Name, d)
		}
	}
	visited, found, note := g.browse(ctx, g.Cfg.Prod.URL)
	if len(visited) > 0 {
		b.WriteString(qa.Report(nil, visited, found, ""))
	}
	b.WriteString(note)
	return b.String(), ok && len(found) == 0
}

// browse clicks through url with the QA agent's browser check, when there
// is a url and Python with Playwright; note says why it was skipped.
func (g *Guardian) browse(ctx context.Context, url string) (visited []string, found []qa.Finding, note string) {
	if url == "" {
		return nil, nil, ""
	}
	if exec.Command("python3", "-c", "import playwright").Run() != nil {
		return nil, nil, "\nBrowser check skipped: install Playwright (pip install playwright && python -m playwright install chromium)."
	}
	visited, found, err := qa.Crawl(ctx, url, 10, filepath.Join(g.Dir, "shots"))
	if err != nil {
		// The site did not load at all: that is a failure, not a skip.
		return nil, []qa.Finding{{Page: url, Kind: "unreachable", Detail: err.Error()}}, ""
	}
	return visited, found, ""
}

// Reject drops a fix.
func (g *Guardian) Reject(id string) error {
	inc, err := g.update(id, func(i *Incident) {
		if open(i.Status) || i.Status == "needs_you" {
			i.Status, i.Note = "rejected", "rejected by the owner"
		}
	})
	if err == nil {
		g.cleanup(inc)
	}
	return err
}

func (g *Guardian) cleanup(inc Incident) {
	if inc.Work != "" {
		_, _ = git(g.Cfg.Repo, "worktree", "remove", "--force", inc.Work)
		_, _ = git(g.Cfg.Repo, "branch", "-D", inc.Branch)
	}
}

// incidentLines is the incidents part of a report.
func (g *Guardian) incidentLines() string {
	var b strings.Builder
	for _, i := range g.Incidents() {
		switch {
		case i.Status == "ready":
			fmt.Fprintf(&b, "Waiting for your approval: fix for %s (incident %s).\n", i.Check, i.ID)
		case i.Status == "needs_you":
			fmt.Fprintf(&b, "Needs you: %s. %s\n", i.Check, i.Note)
		case open(i.Status):
			fmt.Fprintf(&b, "Guardian is fixing %s (round %d).\n", i.Check, i.Round)
		case time.Since(i.Opened) < 24*time.Hour:
			fmt.Fprintf(&b, "%s: %s.\n", i.Check, i.Note)
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
