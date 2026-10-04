// Package guardian watches a running app and keeps it healthy. The watchdog
// part runs checks (is the site up and fast, are there new errors in the
// log, does the jobs check pass). When one fails, Guardian asks a model for
// the likely cause, runs only the fixes the owner listed for that check
// (restart, retry, roll back), checks again, and reports. It never changes
// code.
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
	"time"
)

// Config is guardian.json.
type Config struct {
	App    string  `json:"app"`
	Repo   string  `json:"repo,omitempty"` // a git folder: recent commits help find the cause
	Checks []Check `json:"checks"`
	// Webhook gets a message when a check fails or is fixed, and the morning
	// report (Slack, Discord and similar incoming webhooks).
	Webhook string `json:"webhook,omitempty"`
}

// Check is one thing to watch. Set exactly one of URL, Log or Command.
type Check struct {
	Name    string   `json:"name"`
	URL     string   `json:"url,omitempty"`
	Expect  int      `json:"expect,omitempty"` // status code, default 200
	MaxMS   int      `json:"max_ms,omitempty"` // slower than this fails; 0 = no limit
	Log     string   `json:"log,omitempty"`
	Pattern string   `json:"pattern,omitempty"` // new log lines matching this fail; default errors and crashes
	Command string   `json:"command,omitempty"` // exit code 0 passes
	Fix     []string `json:"fix,omitempty"`     // the only actions Guardian may take, tried in order
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
	if len(c.Checks) == 0 {
		return nil, fmt.Errorf("%s has no checks", path)
	}
	for _, k := range c.Checks {
		n := 0
		for _, v := range []string{k.URL, k.Log, k.Command} {
			if v != "" {
				n++
			}
		}
		if k.Name == "" || n != 1 {
			return nil, fmt.Errorf("check %q needs a name and exactly one of url, log or command", k.Name)
		}
		if k.Pattern != "" {
			if _, err := regexp.Compile(k.Pattern); err != nil {
				return nil, fmt.Errorf("check %q: %v", k.Name, err)
			}
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
}

// Guardian runs checks for one config. State (log positions, history) is
// kept next to the config file.
type Guardian struct {
	Cfg  *Config
	Dir  string                                 // where state and history live
	Ask  func(prompt string) (string, error)    // a model, for likely causes; nil = none
	Send func(url string, payload []byte) error // webhook sender; nil = HTTP POST
	Wait time.Duration                          // pause after a fix before checking again
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
		detail := g.check(ctx, k, offsets)
		ev := Event{Time: time.Now(), Check: k.Name, Status: "ok"}
		if detail != "" {
			ev.Status, ev.Detail = "failing", detail
			ev.Cause = g.cause(k, detail)
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
		out, err := shell(ctx, k.Command)
		if err != nil {
			return "failed: " + cut(strings.TrimSpace(out), 300)
		}
	}
	return ""
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
	out, err := shell(ctx, fix)
	if err != nil {
		return "failed (" + cut(strings.TrimSpace(out), 200) + ")"
	}
	return "done"
}

func shell(ctx context.Context, line string) (string, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", line)
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", line)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (g *Guardian) notify(text string) {
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
	}
	if len(decide) > 0 {
		fmt.Fprintf(&b, "Needs you: %s still failing.\n", strings.Join(decide, ", "))
	}
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
