// Package guardian keeps a running app healthy. Every app has a permanent
// development environment next to production, with the same code (dev.go).
// The watchdog runs checks (site up and fast, new errors in the log, jobs
// script passing) and keeps dev in step with production. On a failure
// Guardian gives first aid (only the fixes the owner listed: restart,
// retry), then fixes the root cause: on its own branch, with tests from the
// QA agent, tested in dev, round after round, with no approval needed. The
// owner's own changes take the same path. Production gets a change only
// after the owner approves it, and it is tested again there (rolled back if
// it fails). Production data is copied to dev nightly with personal details
// masked (data.go).
package guardian

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/qa"
)

// Config is guardian.json.
type Config struct {
	App    string  `json:"app"`
	Repo   string  `json:"repo,omitempty"` // a git folder: recent commits help find the cause
	Checks []Check `json:"checks"`
	// Webhook gets every message and report (Slack, Discord and similar
	// incoming webhooks). Telegram is set up with mangoman guardian telegram.
	Webhook    string `json:"webhook,omitempty"`
	Dev        Env    `json:"dev,omitzero"`
	Prod       Env    `json:"prod,omitzero"`
	ProdBranch string `json:"prod_branch,omitempty"` // default main; dev is always "dev"
	Data       Data   `json:"data,omitzero"`
	// Reports are the daily report times (24-hour, local), sent even on a
	// quiet day. Default 08:00 and 20:00.
	Reports []string `json:"reports,omitempty"`
	// Builder is the tool the app was made with (Lovable, Bolt, Replit...),
	// named in fix requests for apps whose code Guardian does not have.
	Builder string `json:"builder,omitempty"`
}

// Env is one environment. Dev's commands run in its own folder (the dev
// branch); production's in the project folder.
type Env struct {
	Deploy   string            `json:"deploy,omitempty"`   // deploy; for dev, an address on the last output line is used
	URL      string            `json:"url,omitempty"`      // where it answers
	Rollback string            `json:"rollback,omitempty"` // prod: go back to the last good version
	Env      map[string]string `json:"env,omitempty"`      // dev: its own values (test database); live secrets never reach dev
	// Version is an address whose answer includes the running commit (the
	// first 7 characters are enough), to check what is really deployed.
	Version string `json:"version,omitempty"`
}

// Check is one thing to watch. Set exactly one of URL, Log, Command,
// Browse, Expiry or Journey.
type Check struct {
	Name    string `json:"name"`
	URL     string `json:"url,omitempty"`
	Expect  int    `json:"expect,omitempty"` // status code, default 200
	MaxMS   int    `json:"max_ms,omitempty"` // slower than this fails; 0 = no limit
	Log     string `json:"log,omitempty"`
	Pattern string `json:"pattern,omitempty"` // new log lines matching this fail; default errors and crashes
	Command string `json:"command,omitempty"` // exit code 0 passes
	// Browse clicks through the site at this address in a real browser (up
	// to 10 pages): script errors, failed requests, broken images, pages
	// that scroll sideways on a phone.
	Browse string `json:"browse,omitempty"`
	// Expiry checks the address's security certificate (fails under 14
	// days left) and, where the registry publishes it, the domain's end
	// date (fails under 30 days).
	Expiry string `json:"expiry,omitempty"`
	// Journey walks these steps from Prod.URL, as a visitor would (open,
	// click, type, expect to see).
	Journey []qa.Step `json:"journey,omitempty"`
	Every   string    `json:"every,omitempty"` // run at most this often (for example 24h); empty = every run
	Fix     []string  `json:"fix,omitempty"`   // the only actions Guardian may take, tried in order
}

const defaultPattern = `(?i)\b(error|panic|fatal|exception|traceback)\b`

// Load reads and checks a config file.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	if len(c.Checks) == 0 && c.Repo == "" {
		return nil, fmt.Errorf("%s has no checks and no repo", path)
	}
	for _, k := range c.Checks {
		n := 0
		for _, v := range []string{k.URL, k.Log, k.Command, k.Browse, k.Expiry} {
			if v != "" {
				n++
			}
		}
		if len(k.Journey) > 0 {
			n++
		}
		if k.Name == "" || n != 1 {
			return nil, fmt.Errorf("check %q needs a name and exactly one of url, log, command, browse, expiry or journey", k.Name)
		}
		if len(k.Journey) > 0 {
			if c.Prod.URL == "" {
				return nil, fmt.Errorf("check %q: a journey starts at prod.url; set it", k.Name)
			}
			if err := qa.CheckSteps(c.Prod.URL, k.Journey); err != nil {
				return nil, fmt.Errorf("check %q: %v", k.Name, err)
			}
		}
		if k.Every != "" {
			if d, err := time.ParseDuration(k.Every); err != nil || d <= 0 {
				return nil, fmt.Errorf("check %q: every must be a duration like 1h or 24h", k.Name)
			}
		}
		if k.Pattern != "" {
			if _, err := regexp.Compile(k.Pattern); err != nil {
				return nil, fmt.Errorf("check %q: %v", k.Name, err)
			}
		}
	}
	if len(c.Reports) == 0 {
		c.Reports = []string{"08:00", "20:00"}
	}
	for _, r := range c.Reports {
		if _, err := time.Parse("15:04", r); err != nil {
			return nil, fmt.Errorf("report time %q: use 24-hour HH:MM", r)
		}
	}
	return &c, nil
}

// Event is what happened to one check in one run; events are kept in the
// history file for the morning report.
type Event struct {
	Time   time.Time `json:"time"`
	Check  string    `json:"check"`
	Status string    `json:"status"` // ok, fixed, failing
	Detail string    `json:"detail,omitempty"`
	Cause  string    `json:"cause,omitempty"`
	Done   []string  `json:"done,omitempty"` // fixes run, with their result
	// Request is a fix request ready to paste into the app builder (Lovable,
	// Bolt, Replit...), for an app whose code Guardian does not have.
	Request string `json:"request,omitempty"`
}

// Guardian runs one config.
type Guardian struct {
	Cfg  *Config
	Dir  string                                 // where state, history and incidents live
	Ask  func(prompt string) (string, error)    // a model, for likely causes; nil = none
	Send func(url string, payload []byte) error // webhook sender; nil = HTTP POST
	Wait time.Duration                          // pause after a fix or deploy before checking again
	// Agent does coding work in a folder (MangoMan's headless coding agent).
	// nil = no incidents: first aid and reports only.
	Agent func(ctx context.Context, dir, prompt string) error
	Root  string    // where the dev environment and change copies live
	TG    *Telegram // nil = no Telegram
	Logf  func(format string, args ...any)

	mu      sync.Mutex           // guards the incidents file
	copying sync.Mutex           // one nightly data copy at a time
	asked   map[string]string    // change requests from Telegram waiting for "Yes"; Serve's loop only
	written map[string][2]string // per check: the problem and the fix request written for it; Run only
}

// Run checks everything once, tries the listed fixes on failures, records
// and announces what happened.
func (g *Guardian) Run(ctx context.Context) ([]Event, error) {
	offsets := map[string]int64{}
	statePath := filepath.Join(g.Dir, "guardian-state.json")
	if b, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(b, &offsets)
	}
	var events []Event
	for _, k := range g.Cfg.Checks {
		if !due(k, offsets, time.Now()) {
			continue
		}
		if (k.Browse != "" || len(k.Journey) > 0) && !HasBrowser() {
			continue // nothing ran: no result, rather than a "fine" that was never checked
		}
		cctx, cancel := context.WithTimeout(ctx, 10*time.Minute) // a hung browser cannot hold up the watch
		detail := g.check(cctx, k, offsets)
		cancel()
		if ctx.Err() != nil {
			break // stopped mid-check: the result says nothing about the app
		}
		if k.Every != "" {
			offsets["every:"+k.Name] = time.Now().Unix()
		}
		ev := Event{Time: time.Now(), Check: k.Name, Status: "ok"}
		if detail == "" {
			delete(g.written, k.Name)
		} else {
			ev.Status, ev.Detail = "failing", detail
			ev.Cause = g.cause(k, detail)
			if g.Cfg.Repo == "" && k.Expiry == "" { // a domain or certificate is not the builder's to fix
				ev.Request = g.request(k, detail, ev.Cause)
			}
			for _, fix := range k.Fix {
				res := runFix(ctx, fix)
				ev.Done = append(ev.Done, fix+": "+res)
				if k.Log != "" {
					// Errors already logged stay in the log: a fix that ran
					// is the best this check can know.
					if res == "done" {
						ev.Status = "fixed"
						break
					}
					continue
				}
				time.Sleep(g.Wait)
				if g.check(ctx, k, offsets) == "" {
					ev.Status = "fixed"
					break
				}
			}
			g.notify(fmt.Sprintf("%s: %s %s. %s", g.Cfg.App, k.Name, ev.Status, ev.Detail))
		}
		events = append(events, ev)
	}
	if g.Cfg.Repo != "" {
		ev := Event{Time: time.Now(), Check: "dev", Status: "ok"}
		if d := g.parity(ctx); d != "" {
			ev.Status, ev.Detail = "failing", d
			g.notify(fmt.Sprintf("%s: %s", g.Cfg.App, d))
		}
		events = append(events, ev)
	}
	b, _ := json.Marshal(offsets)
	if err := os.WriteFile(statePath, b, 0o600); err != nil {
		return events, err
	}
	return events, g.record(events)
}

// check returns "" when the check passes, else what is wrong.
func (g *Guardian) check(ctx context.Context, k Check, offsets map[string]int64) string {
	switch {
	case k.URL != "":
		client := &http.Client{Timeout: 10 * time.Second}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.URL, nil)
		if err != nil {
			return err.Error()
		}
		start := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			return "not reachable: " + err.Error()
		}
		resp.Body.Close()
		took := time.Since(start)
		want := k.Expect
		if want == 0 {
			want = http.StatusOK
		}
		if resp.StatusCode != want {
			return fmt.Sprintf("answered %d, expected %d", resp.StatusCode, want)
		}
		if k.MaxMS > 0 && took > time.Duration(k.MaxMS)*time.Millisecond {
			return fmt.Sprintf("slow: %d ms (limit %d ms)", took.Milliseconds(), k.MaxMS)
		}
	case k.Log != "":
		lines, err := newLines(k.Log, offsets)
		if err != nil {
			return "log unreadable: " + err.Error()
		}
		pat := k.Pattern
		if pat == "" {
			pat = defaultPattern
		}
		re := regexp.MustCompile(pat)
		var bad []string
		for _, l := range lines {
			if re.MatchString(l) {
				bad = append(bad, strings.TrimSpace(l))
			}
		}
		if len(bad) > 0 {
			return fmt.Sprintf("%d new error lines, latest: %s", len(bad), cut(bad[len(bad)-1], 300))
		}
	case k.Command != "":
		out, err := shell(ctx, "", k.Command)
		if err != nil {
			return "failed: " + cut(strings.TrimSpace(out), 300)
		}
	case k.Browse != "":
		if !HasBrowser() {
			return "" // no browser on this computer (Run skips these; release checks note it)
		}
		visited, found, err := qa.Crawl(ctx, k.Browse, 10, filepath.Join(g.Dir, "shots"))
		if err != nil {
			return cut(err.Error(), 300)
		}
		return findings(len(visited), found)
	case k.Expiry != "":
		return expiry(ctx, k.Expiry, time.Now())
	case len(k.Journey) > 0:
		if !HasBrowser() {
			return ""
		}
		why, err := qa.Journey(ctx, g.Cfg.Prod.URL, k.Journey, filepath.Join(g.Dir, "shots", "journey-"+slug(k.Name)+".png"))
		if err != nil {
			return cut(err.Error(), 300)
		}
		return why
	}
	return ""
}

// due reports whether a check with Every should run now. Run notes the
// time once the check has finished (kept with the log offsets).
func due(k Check, state map[string]int64, now time.Time) bool {
	d, err := time.ParseDuration(k.Every)
	if k.Every == "" || err != nil {
		return true
	}
	last := state["every:"+k.Name]
	return last == 0 || now.Sub(time.Unix(last, 0)) >= d
}

// HasBrowser reports whether this computer can run the browser checks
// (Python with Playwright, which Get ready installs).
func HasBrowser() bool { return exec.Command("python3", "-c", "import playwright").Run() == nil }

// findingWords names what the browser check found, in plain words.
var findingWords = map[string]string{
	"console_error": "Script error", "page_error": "Page crashed", "http_error": "Failed to load",
	"broken_image": "Broken image", "phone_overflow": "Too wide on a phone", "unreachable": "Did not open",
}

// findings sums up a browser check in a few lines; "" when nothing was wrong.
func findings(pages int, found []qa.Finding) string {
	if len(found) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d problems on %d pages checked", len(found), pages)
	for i, f := range found {
		if i == 3 {
			fmt.Fprintf(&b, "\nand %d more", len(found)-3)
			break
		}
		kind := findingWords[f.Kind]
		if kind == "" {
			kind = f.Kind
		}
		fmt.Fprintf(&b, "\n%s on %s: %s", kind, f.Page, cut(f.Detail, 160))
	}
	return b.String()
}

// request writes a fix request the owner can paste into the tool the app
// was built with. The model words it when there is one; otherwise a plain
// one is made from the facts.
func (g *Guardian) request(k Check, detail, cause string) string {
	// The same problem keeps its request; numbers (timings, counts) do not
	// make it a new one.
	key := digits.ReplaceAllString(detail, "#")
	if r, ok := g.written[k.Name]; ok && r[0] == key {
		return r[1]
	}
	r := g.writeRequest(k, detail, cause)
	if g.written == nil {
		g.written = map[string][2]string{}
	}
	g.written[k.Name] = [2]string{key, r}
	return r
}

var digits = regexp.MustCompile(`[0-9]+`)

func (g *Guardian) writeRequest(k Check, detail, cause string) string {
	plain := fmt.Sprintf("My live app %s has a problem.\nCheck %q failed: %s\n", g.Cfg.Prod.URL, k.Name, detail)
	if cause != "" {
		plain += "Likely cause: " + cause + "\n"
	}
	plain += "Please find the cause, fix it with the smallest change, and tell me what you changed."
	if g.Ask == nil {
		return plain
	}
	tool := "the AI app builder they used (such as Lovable, Bolt or Replit)"
	if g.Cfg.Builder != "" {
		tool = g.Cfg.Builder + ", the tool they built it with,"
	}
	ans, err := g.Ask("Write a short request the owner of a web app can paste into " + tool + " to fix this problem. " +
		"Plain words, at most 8 lines, no greeting. Include the exact error text and where it happens, and ask for the smallest fix. Do not invent details.\n\n" + plain)
	if err != nil || strings.TrimSpace(ans) == "" {
		return plain
	}
	return strings.TrimSpace(ans)
}

// newLines returns the lines added to a log since the last run. A log that
// shrank (rotated) is read from the start. The first run only notes the end.
func newLines(path string, offsets map[string]int64) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	from, seen := offsets[path]
	offsets[path] = st.Size()
	if !seen {
		return nil, nil
	}
	if from > st.Size() {
		from = 0
	}
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return nil, err
	}
	var out []string
	sc := bufio.NewScanner(io.LimitReader(f, 8<<20))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out, nil
}

// cause asks the model for the likely cause, with recent commits if known.
func (g *Guardian) cause(k Check, detail string) string {
	if g.Ask == nil {
		return ""
	}
	prompt := fmt.Sprintf("An app called %q failed a health check.\nCheck: %s\nProblem: %s\n", g.Cfg.App, k.Name, detail)
	if g.Cfg.Repo != "" {
		if out, err := exec.Command("git", "-C", g.Cfg.Repo, "log", "-5", "--format=%h %ar %s").Output(); err == nil {
			prompt += "Recent commits:\n" + string(out)
		}
	}
	prompt += "In at most three short sentences: the most likely cause, and what to look at first. Say if you are unsure."
	ans, err := g.Ask(prompt)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(ans)
}

func runFix(ctx context.Context, fix string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	out, err := shell(ctx, "", fix)
	if err != nil {
		return "failed (" + cut(strings.TrimSpace(out), 200) + ")"
	}
	return "done"
}

func shellCmd(ctx context.Context, line string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd", "/C", line)
	}
	return exec.CommandContext(ctx, "sh", "-c", line)
}

// shell runs a command line in dir ("" = here) and returns its output.
func shell(ctx context.Context, dir, line string) (string, error) {
	cmd := shellCmd(ctx, line)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (g *Guardian) notify(text string) {
	if g.TG != nil {
		_ = g.TG.Send(text)
	}
	if g.Cfg.Webhook == "" {
		return
	}
	// "text" for Slack and most others, "content" for Discord.
	b, _ := json.Marshal(map[string]string{"text": text, "content": text})
	send := g.Send
	if send == nil {
		send = func(url string, payload []byte) error {
			resp, err := http.Post(url, "application/json", bytes.NewReader(payload))
			if err != nil {
				return err
			}
			resp.Body.Close()
			return nil
		}
	}
	_ = send(g.Cfg.Webhook, b)
}

func (g *Guardian) record(events []Event) error {
	f, err := os.OpenFile(filepath.Join(g.Dir, "guardian-history.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return nil
}

// Latest is each check's last result, from the end of the history.
func (g *Guardian) Latest() []Event {
	out := []Event{}
	at := map[string]int{}
	for _, e := range g.tail(1 << 20) {
		if i, ok := at[e.Check]; ok {
			out[i] = e
		} else {
			at[e.Check] = len(out)
			out = append(out, e)
		}
	}
	return out
}

// Strip is the last 24 hours in half-hour slots, oldest first: "bad" when
// any check failed in that slot, "ok" when checks ran and passed, "" when
// none ran. Dev drift and dates running out are left out: the app itself
// was fine.
func (g *Guardian) Strip(now time.Time) []string {
	const slots, width = 48, 30 * time.Minute
	dates := map[string]bool{"dev": true}
	for _, k := range g.Cfg.Checks {
		dates[k.Name] = k.Expiry != ""
	}
	out := make([]string, slots)
	start := now.Add(-slots * width)
	for _, e := range g.tail(1 << 20) {
		i := int(e.Time.Sub(start) / width)
		if i < 0 || i >= slots || dates[e.Check] {
			continue
		}
		if e.Status != "ok" {
			out[i] = "bad"
		} else if out[i] == "" {
			out[i] = "ok"
		}
	}
	return out
}

// tail reads the events in the last n bytes of the history.
func (g *Guardian) tail(n int64) []Event {
	f, err := os.Open(filepath.Join(g.Dir, "guardian-history.jsonl"))
	if err != nil {
		return nil
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() > n {
		_, _ = f.Seek(st.Size()-n, io.SeekStart) // the first line read may be cut; it does not parse
	}
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Check != "" {
			out = append(out, e)
		}
	}
	return out
}

// Report summarises the last day: what broke, why, what Guardian did and
// what still needs a person. If send is set it also goes to the webhook.
func (g *Guardian) Report(send bool) (string, error) {
	f, err := os.Open(filepath.Join(g.Dir, "guardian-history.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return "", errors.New("no history yet: run `mangoman guardian run` first")
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	since := time.Now().Add(-24 * time.Hour)
	runs, fails := map[string]int{}, map[string][]Event{}
	last := map[string]Event{}
	var order []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Time.Before(since) {
			continue
		}
		if runs[e.Check] == 0 {
			order = append(order, e.Check)
		}
		runs[e.Check]++
		last[e.Check] = e
		if e.Status != "ok" {
			fails[e.Check] = append(fails[e.Check], e)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: last 24 hours\n", g.Cfg.App)
	var decide []string
	for _, c := range order {
		fs := fails[c]
		if len(fs) == 0 {
			fmt.Fprintf(&b, "- %s: checked %d times, always fine\n", c, runs[c])
			continue
		}
		fixed := 0
		for _, e := range fs {
			if e.Status == "fixed" {
				fixed++
			}
		}
		latest := fs[len(fs)-1]
		fmt.Fprintf(&b, "- %s: failed %d of %d times", c, len(fs), runs[c])
		if fixed > 0 {
			fmt.Fprintf(&b, ", fixed by Guardian %d of them", fixed)
		}
		fmt.Fprintf(&b, ". Latest: %s\n", latest.Detail)
		if latest.Cause != "" {
			fmt.Fprintf(&b, "  Likely cause: %s\n", latest.Cause)
		}
		if last[c].Status == "failing" {
			decide = append(decide, c)
		}
	}
	if len(order) == 0 {
		b.WriteString("No checks ran.\n")
	} else if len(fails) == 0 {
		b.WriteString("All good: no issues.\n")
	}
	if len(decide) > 0 {
		fmt.Fprintf(&b, "Needs you: %s still failing.\n", strings.Join(decide, ", "))
	}
	b.WriteString(g.incidentLines())
	if send {
		g.notify(b.String())
	}
	return b.String(), nil
}

func cut(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
