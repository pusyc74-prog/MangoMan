package guardian

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/qa"
	"github.com/pusyc74-prog/mangoman/internal/review"
)

// The permanent development environment is the dev branch, checked out in
// its own folder and deployed next to production all the time. Every change
// reaches production only through it.
const devBranch = "dev"

func (g *Guardian) prod() string {
	if g.Cfg.ProdBranch != "" {
		return g.Cfg.ProdBranch
	}
	return "main"
}

func (g *Guardian) devDir() string { return filepath.Join(g.Root, "dev") }

// Setup makes the development environment on day 1: the dev branch (a copy
// of production) in its own folder, deployed. It is safe to run again.
func (g *Guardian) Setup(ctx context.Context) error {
	if _, err := os.Stat(g.devDir()); err == nil {
		return nil
	}
	if _, err := git(g.Cfg.Repo, "rev-parse", "--verify", devBranch); err != nil {
		if _, err := git(g.Cfg.Repo, "branch", devBranch, g.prod()); err != nil {
			return fmt.Errorf("making the dev branch from %s: %w", g.prod(), err)
		}
	}
	if _, err := git(g.Cfg.Repo, "worktree", "add", g.devDir(), devBranch); err != nil {
		return err
	}
	_, err := g.deployDev(ctx)
	return err
}

// sync brings production's commits into dev, so dev always has everything
// production has.
func (g *Guardian) sync() error {
	_, err := git(g.devDir(), "merge", "--no-edit", g.prod())
	if err != nil {
		_, _ = git(g.devDir(), "merge", "--abort")
	}
	return err
}

// deployDev deploys the dev folder and returns its address once it answers.
// Live secrets are left out of its environment.
func (g *Guardian) deployDev(ctx context.Context) (string, error) {
	d := g.Cfg.Dev
	url := d.URL
	if d.Deploy != "" {
		cmd := shellCmd(ctx, d.Deploy)
		cmd.Dir, cmd.Env = g.devDir(), devEnv(d.Env)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("development deploy failed: %s", cut(strings.TrimSpace(string(out)), 300))
		}
		if u := siteURL(string(out)); u != "" {
			url = u // a preview link printed by the deploy (Vercel, Netlify)
		}
	}
	if url != "" {
		if err := waitUp(ctx, url, 2*time.Minute); err != nil {
			return "", err
		}
	}
	return url, nil
}

var urlRe = regexp.MustCompile(`https?://[^\s"'<>]+`)

// siteURL is the last address in a deploy's output that is not the host's
// own dashboard (Vercel's "Inspect", Netlify's logs).
func siteURL(out string) string {
	found := ""
	for _, u := range urlRe.FindAllString(out, -1) {
		if !strings.Contains(u, "//vercel.com/") && !strings.Contains(u, "//app.netlify.com/") {
			found = strings.TrimRight(u, ".,)")
		}
	}
	return found
}

// running checks that the Version address of an environment reports the
// commit of branch; "" means it does (or no Version address is set).
func (g *Guardian) running(ctx context.Context, e Env, branch string) string {
	if e.Version == "" {
		return ""
	}
	want, err := git(g.Cfg.Repo, "rev-parse", "--short=7", branch)
	if err != nil {
		return ""
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.Version, nil)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "version address not reachable: " + err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if !strings.Contains(string(b), want) {
		return fmt.Sprintf("%s is not running the code it should (%s %s)", e.Version, branch, want)
	}
	return ""
}

var errBusy = errors.New("development is busy with another change")

// Work takes a queued incident through development: write the change on
// its own branch, have the QA agent add tests, put it into dev, deploy dev,
// test there. When QA passes it asks the owner; otherwise it takes the
// change out of dev and tries again, up to maxRounds.
func (g *Guardian) Work(ctx context.Context, id string) error {
	if n := g.next(); n != id {
		return errBusy
	}
	if err := g.Setup(ctx); err != nil {
		return g.giveUp(id, "no development environment: "+err.Error())
	}
	if err := g.sync(); err != nil {
		return g.giveUp(id, "dev could not take production's latest code: "+err.Error())
	}
	before, _ := git(g.devDir(), "rev-parse", "HEAD")
	inc, err := g.update(id, func(i *Incident) { i.Status, i.Round, i.DevBefore = "working", 0, before })
	if err != nil {
		return err
	}
	if inc.Work == "" {
		work := filepath.Join(g.Root, id)
		if _, err := git(g.Cfg.Repo, "worktree", "add", "-b", inc.Branch, work, devBranch); err != nil {
			return g.giveUp(id, "could not make a copy to work in: "+err.Error())
		}
		inc, _ = g.update(id, func(i *Incident) { i.Work = work })
	}
	for inc.Round < maxRounds {
		inc, _ = g.update(id, func(i *Incident) { i.Round++ })
		g.logf("%s: round %d", id, inc.Round)
		summary := inc.Summary
		if !inc.Manual || inc.Round > 1 { // the owner's own code goes to QA as written first
			if err := g.Agent(ctx, inc.Work, workPrompt(inc)); err != nil && ctx.Err() != nil {
				return ctx.Err()
			}
			summary = readAndRemove(filepath.Join(inc.Work, "GUARDIAN_FIX.md"))
		}
		commit(inc.Work, "Guardian: "+cut(inc.Problem, 60))
		if err := g.Agent(ctx, inc.Work, testPrompt(inc, summary)); err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		keepTestsOnly(inc.Work)
		commit(inc.Work, "QA: tests for "+inc.ID)
		report, ok := g.catchesBug(ctx, inc)
		if ok {
			report, ok = g.tryInDev(ctx, inc)
		}
		inc, _ = g.update(id, func(i *Incident) { i.QA, i.Summary = report, summary })
		if ok {
			inc, _ = g.update(id, func(i *Incident) { i.Status, i.Note = "ready", "passed QA in development" })
			g.askApproval(inc)
			return nil
		}
		// Take it out of dev again, so dev matches production while Guardian retries.
		_, _ = git(g.devDir(), "reset", "-q", "--hard", inc.DevBefore)
		_, _ = g.deployDev(ctx)
		g.logf("%s: QA failed in development", id)
	}
	return g.giveUp(id, fmt.Sprintf("QA still failed after %d rounds. The work so far is on branch %s", maxRounds, inc.Branch))
}

// catchesBug checks that, for a fix, the project's tests (with the QA
// agent's new ones) fail on the code from before the fix. Tests that pass
// either way would not catch the bug coming back.
func (g *Guardian) catchesBug(ctx context.Context, inc Incident) (string, bool) {
	if inc.Kind != "fix" || len(qa.Detect(inc.Work)) == 0 {
		return "", true
	}
	old := filepath.Join(g.Root, inc.ID+"-before")
	if _, err := git(g.Cfg.Repo, "worktree", "add", "--detach", old, inc.DevBefore); err != nil {
		return "", true // cannot check; QA in dev still runs
	}
	defer git(g.Cfg.Repo, "worktree", "remove", "--force", old)
	tests, _ := git(inc.Work, "diff", "--name-only", "--diff-filter=AM", "HEAD~1", "HEAD")
	for _, f := range strings.Fields(tests) {
		if b, err := os.ReadFile(filepath.Join(inc.Work, f)); err == nil {
			_ = os.MkdirAll(filepath.Dir(filepath.Join(old, f)), 0o755)
			_ = os.WriteFile(filepath.Join(old, f), b, 0o644)
		}
	}
	for _, r := range qa.RunTests(ctx, old, qa.Detect(old)) {
		if !r.OK {
			return "", true
		}
	}
	return "The tests pass even without the fix, so they would not catch this bug coming back. Write a test that reproduces it.", false
}

// tryInDev merges the change into dev, deploys dev and runs QA there.
func (g *Guardian) tryInDev(ctx context.Context, inc Incident) (string, bool) {
	if _, err := git(inc.Work, "diff", "--quiet", inc.DevBefore, "HEAD"); err == nil {
		return "No change was made to the code.", false
	}
	diff, _ := git(inc.Work, "diff", inc.DevBefore, "HEAD")
	found := review.Scan(diff)
	if review.Blocking(found) {
		return "Code review stopped it before dev:\n" + review.Report(found, ""), false
	}
	if _, err := git(g.devDir(), "merge", "--no-ff", "--no-edit", inc.Branch); err != nil {
		_, _ = git(g.devDir(), "merge", "--abort")
		return "The change does not merge into dev: " + err.Error(), false
	}
	url, err := g.deployDev(ctx)
	if err != nil {
		return err.Error(), false
	}
	report, ok := g.qa(ctx, g.devDir(), url)
	if len(found) > 0 {
		report += "\n" + review.Report(found, "")
	}
	return report, ok
}

// qa runs the project's tests in dir and clicks through url (if any).
// Passing needs at least one of the two, and no problem in either.
func (g *Guardian) qa(ctx context.Context, dir, url string) (string, bool) {
	results := qa.RunTests(ctx, dir, qa.Detect(dir))
	visited, found, note := g.browse(ctx, url)
	ok := qa.Problems(results, found) == "" && (len(results) > 0 || len(visited) > 0)
	if len(results) == 0 && len(visited) == 0 {
		note += "\nNothing could be tested: add tests to the project or a development address."
	}
	return qa.Report(results, visited, found, "") + note, ok
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

var secretName = regexp.MustCompile(`(?i)key|token|secret|passw|private|credential|dsn|database_url|_url$|auth`)

// devEnv is this environment without anything that looks like a secret,
// plus the owner's test values for development.
func devEnv(test map[string]string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); !secretName.MatchString(k) {
			env = append(env, kv)
		}
	}
	for k, v := range test {
		env = append(env, k+"="+v)
	}
	return env
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
	stat, _ := git(g.devDir(), "diff", "--shortstat", inc.DevBefore, "HEAD")
	what := "a fix for " + inc.Check
	if inc.Kind == "change" {
		what = "your change"
	}
	text := fmt.Sprintf("%s: %s is ready and running in development.\n%s\n%s\nChange: %s\nQA passed in development. Approve to make it live.\nIncident %s",
		g.Cfg.App, what, cut(inc.Problem, 300), strings.TrimSpace(inc.Summary), strings.TrimSpace(stat), inc.ID)
	if g.Cfg.Dev.URL != "" {
		text += "\nSee it: " + g.Cfg.Dev.URL
	}
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

// Approve makes what is in dev live: merge dev into production, deploy,
// test production. If production QA fails it rolls back, takes the change
// out of dev and queues it to be done again.
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
	back := func(why string) error {
		g.update(id, func(i *Incident) { i.Status = "ready" })
		return errors.New(why)
	}
	if g.Cfg.Prod.Deploy == "" {
		return back("set prod.deploy in guardian.json to make changes live")
	}
	if b, _ := git(g.Cfg.Repo, "rev-parse", "--abbrev-ref", "HEAD"); b != g.prod() {
		return back(fmt.Sprintf("the project folder is on branch %s; switch it to %s first", b, g.prod()))
	}
	if _, err := git(g.Cfg.Repo, "merge", "--no-ff", "--no-edit", devBranch); err != nil {
		_, _ = git(g.Cfg.Repo, "merge", "--abort")
		return g.giveUp(id, "dev no longer merges into production cleanly: "+err.Error())
	}
	g.notify(fmt.Sprintf("%s: deploying %s to production.", g.Cfg.App, id))
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
		g.notify(fmt.Sprintf("%s: %s is live and passed QA in production.", g.Cfg.App, id))
		return nil
	}
	if g.Cfg.Prod.Rollback != "" {
		_, _ = shell(ctx, g.Cfg.Repo, g.Cfg.Prod.Rollback)
	}
	_, _ = git(g.Cfg.Repo, "revert", "-m", "1", "--no-edit", "HEAD")
	// Dev goes back to production's code. The work moves to a new branch as
	// fresh copies of its commits: a branch that was merged and reverted
	// would not merge again.
	_, _ = git(g.devDir(), "reset", "-q", "--hard", inc.DevBefore)
	_ = g.sync()
	_, _ = g.deployDev(ctx)
	branch := "guardian/" + id + "-" + time.Now().Format("150405")
	work := filepath.Join(g.Root, slug(branch))
	if _, err := git(g.Cfg.Repo, "worktree", "add", "-b", branch, work, devBranch); err != nil {
		work = ""
	} else if commits, _ := git(g.Cfg.Repo, "rev-list", "--reverse", "--no-merges", inc.DevBefore+".."+inc.Branch); commits != "" {
		if _, err := git(work, append([]string{"cherry-pick"}, strings.Fields(commits)...)...); err != nil {
			_, _ = git(work, "cherry-pick", "--abort") // start from dev's code instead
		}
	}
	g.cleanup(inc)
	g.update(id, func(i *Incident) {
		i.Status, i.QA, i.Work, i.Branch, i.Manual = "queued", report, work, branch, false
		i.Note = "failed QA in production and was rolled back"
	})
	g.notify(fmt.Sprintf("%s: %s failed QA in production and was rolled back. Guardian will work on it again.", g.Cfg.App, id))
	return nil
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
	if d := g.running(ctx, g.Cfg.Prod, g.prod()); d != "" {
		ok = false
		b.WriteString(d + "\n")
	}
	visited, found, note := g.browse(ctx, g.Cfg.Prod.URL)
	if len(visited) > 0 || len(found) > 0 {
		b.WriteString(qa.Report(nil, visited, found, ""))
	}
	b.WriteString(note)
	return b.String(), ok && len(found) == 0
}

// Reject drops a change and takes it out of dev.
func (g *Guardian) Reject(ctx context.Context, id string) error {
	var was string
	inc, err := g.update(id, func(i *Incident) {
		was = i.Status
		if active(i.Status) || i.Status == "needs_you" {
			i.Status, i.Note = "rejected", "rejected by the owner"
		}
	})
	if err != nil {
		return err
	}
	if was == "ready" {
		_, _ = git(g.devDir(), "reset", "-q", "--hard", inc.DevBefore)
		_, _ = g.deployDev(ctx)
	}
	g.cleanup(inc)
	return nil
}

func (g *Guardian) cleanup(inc Incident) {
	if inc.Work != "" {
		_, _ = git(g.Cfg.Repo, "worktree", "remove", "--force", inc.Work)
		_, _ = git(g.Cfg.Repo, "branch", "-D", inc.Branch)
	}
}

// parity checks that dev runs production's code (plus the one change being
// worked on). Missing production commits are brought in; anything else is
// drift to report. "" means all is well.
func (g *Guardian) parity(ctx context.Context) string {
	if _, err := os.Stat(g.devDir()); err != nil {
		return ""
	}
	for _, i := range g.Incidents() {
		if inDev(i.Status) {
			return "" // dev differs on purpose
		}
	}
	if _, err := git(g.Cfg.Repo, "merge-base", "--is-ancestor", g.prod(), devBranch); err != nil {
		if err := g.sync(); err != nil {
			return "dev could not take production's latest code: " + err.Error()
		}
		if _, err := g.deployDev(ctx); err != nil {
			return err.Error()
		}
	}
	if stat, err := git(g.Cfg.Repo, "diff", "--shortstat", g.prod(), devBranch); err != nil || stat != "" {
		return "dev differs from production outside Guardian: " + stat
	}
	if d := g.running(ctx, g.Cfg.Dev, devBranch); d != "" {
		return d
	}
	return g.running(ctx, g.Cfg.Prod, g.prod())
}
