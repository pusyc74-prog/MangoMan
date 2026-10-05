package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/guardian"
	"github.com/pusyc74-prog/mangoman/internal/qa"
)

// parseWithDir parses flags that may come before or after one folder argument.
func parseWithDir(fs *flag.FlagSet, args []string) (string, error) {
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() == 0 {
		return ".", nil
	}
	dir := fs.Arg(0)
	return dir, fs.Parse(fs.Args()[1:])
}

func cmdQA(args []string) error {
	fs := flag.NewFlagSet("qa", flag.ContinueOnError)
	url := fs.String("url", "", "the running web app to click through in a browser")
	pages := fs.Int("pages", 20, "most pages to visit")
	out := fs.String("out", "", "report file (default DIR/qa-report.md)")
	noAI := fs.Bool("no-ai", false, "do not ask a model for likely causes")
	dir, err := parseWithDir(fs, args)
	if err != nil {
		return err
	}
	if *out == "" {
		*out = filepath.Join(dir, "qa-report.md")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	suites := qa.Detect(dir)
	if len(suites) == 0 && *url == "" {
		return errors.New("no tests found (go.mod, package.json, pytest, Cargo.toml or a Makefile test target) and no --url to check")
	}
	for _, s := range suites {
		fmt.Printf("Running %s tests...\n", s.Name)
	}
	results := qa.RunTests(ctx, dir, suites)
	var visited []string
	var found []qa.Finding
	if *url != "" {
		shown, _, _ := strings.Cut(*url, "#") // never print a token in the address
		fmt.Println("Clicking through", shown, "in a browser...")
		if visited, found, err = qa.Crawl(ctx, *url, *pages, filepath.Join(dir, "qa-shots")); err != nil {
			return fmt.Errorf("%w (needs Python with Playwright: pip install playwright && python -m playwright install chromium)", err)
		}
	}
	problems := qa.Problems(results, found)
	causes := ""
	if problems != "" && !*noAI {
		ans, _, err := askRouter("These problems were found while testing a software project:\n" + problems +
			"\nFor each problem, in one short line: the most likely cause and where to look. Say if you are unsure. No fixes.")
		if err == nil {
			causes = ans
		}
	}
	if err := os.WriteFile(*out, []byte(qa.Report(results, visited, found, causes)), 0o644); err != nil {
		return err
	}
	if problems == "" {
		fmt.Println("All good. Report:", *out)
		return nil
	}
	return fmt.Errorf("problems found; see %s", *out)
}

// guardianStart writes a starting guardian.json for the project in dir,
// with development and production set up from what the project uses.
func guardianStart(dir string) *guardian.Config {
	has := func(n string) bool { _, err := os.Stat(filepath.Join(dir, n)); return err == nil }
	c := &guardian.Config{App: filepath.Base(dir), Repo: ".", Checks: []guardian.Check{
		{Name: "site", URL: "https://example.com/", MaxMS: 3000, Fix: []string{"systemctl restart my-app"}},
		{Name: "errors", Log: "/var/log/my-app.log"},
	},
		// Own server: a second copy of the app on the same server.
		Dev:  guardian.Env{Deploy: "./deploy.sh dev", URL: "https://dev.example.com/"},
		Prod: guardian.Env{Deploy: "./deploy.sh", Rollback: "./rollback.sh", URL: "https://example.com/"},
		Data: guardian.Data{Export: `pg_dump --data-only --column-inserts "$DATABASE_URL"`, Import: `psql "$DATABASE_URL"`},
	}
	if b, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
		c.ProdBranch = strings.TrimSpace(string(b))
	}
	switch {
	case has("vercel.json") || has(".vercel"):
		c.Dev = guardian.Env{Deploy: "npx vercel deploy --yes"} // prints the dev address
		c.Prod = guardian.Env{Deploy: "npx vercel deploy --prod --yes", Rollback: "npx vercel rollback --yes", URL: "https://example.com/"}
	case has("docker-compose.yml") || has("compose.yaml"):
		c.Dev = guardian.Env{Deploy: "docker compose -p " + slugName(dir) + "-dev up -d --build", URL: "http://127.0.0.1:8081/"}
		c.Prod.Deploy = "docker compose -p " + slugName(dir) + " up -d --build"
	}
	c.Dev.Env = map[string]string{"DATABASE_URL": "dev's own database, never the live one"}
	return c
}

// loadGuardian reads a guardian.json and wires Guardian to this machine:
// state next to the file, dev and workspaces in MangoMan's folder, the
// headless coding agent, and the free models for likely causes.
func loadGuardian(path string) (*guardian.Guardian, error) {
	cfg, err := guardian.Load(path)
	if err != nil {
		return nil, err
	}
	base := filepath.Dir(must(filepath.Abs(path)))
	if cfg.Repo != "" && !filepath.IsAbs(cfg.Repo) {
		cfg.Repo = filepath.Join(base, cfg.Repo)
	}
	state := filepath.Join(base, ".guardian")
	if err := os.MkdirAll(state, 0o700); err != nil {
		return nil, err
	}
	cdir, _ := config.Dir()
	self, _ := os.Executable()
	return &guardian.Guardian{Cfg: cfg, Dir: state, Wait: 10 * time.Second, TG: guardian.LoadTelegram(state),
		Root: filepath.Join(cdir, "guardian", slugName(cfg.App)),
		Ask:  func(p string) (string, error) { a, _, err := askRouter(p); return a, err },
		Agent: func(ctx context.Context, dir, prompt string) error {
			cmd := exec.CommandContext(ctx, self, "code", "--no-web", "run", "--auto", "--dir", dir, prompt)
			cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
			return cmd.Run()
		},
		Logf: func(f string, a ...any) { fmt.Printf(time.Now().Format("15:04:05")+"  "+f+"\n", a...) }}, nil
}

func slugName(dir string) string {
	return strings.ToLower(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, filepath.Base(dir)))
}

func cmdGuardian(args []string) error {
	sub := "run"
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		sub, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("guardian", flag.ContinueOnError)
	path := fs.String("config", "guardian.json", "the checks, fixes and environments")
	every := fs.Duration("every", 0, "run: keep watching, checking this often (for example 5m)")
	send := fs.Bool("send", false, "report: also send it to Telegram and the webhook")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if sub == "init" {
		if _, err := os.Stat(*path); err == nil {
			return fmt.Errorf("%s already exists", *path)
		}
		var b strings.Builder
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		_ = enc.Encode(guardianStart(filepath.Dir(must(filepath.Abs(*path)))))
		if err := os.WriteFile(*path, []byte(b.String()), 0o644); err != nil {
			return err
		}
		// Guardian's state (incidents, the Telegram token) stays out of git.
		ignore := filepath.Join(filepath.Dir(*path), ".gitignore")
		if old, _ := os.ReadFile(ignore); !strings.Contains(string(old), ".guardian/") {
			f, err := os.OpenFile(ignore, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err == nil {
				fmt.Fprintln(f, ".guardian/")
				f.Close()
			}
		}
		fmt.Printf("Wrote %s. Set your checks, the fixes that are safe any time, and how to deploy dev and production.\n"+
			"Then: mangoman guardian setup (makes the dev environment), mangoman guardian telegram BOT_TOKEN,\n"+
			"and mangoman guardian run --every 5m\n", *path)
		return nil
	}
	g, err := loadGuardian(*path)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	id := fs.Arg(0)
	switch sub {
	case "report":
		rep, err := g.Report(*send)
		fmt.Print(rep)
		return err
	case "incidents":
		for _, i := range g.Incidents() {
			fmt.Printf("%s  %-10s %-9s %s\n", i.ID, i.Check, i.Status, i.Note)
		}
		return nil
	case "approve":
		return g.Approve(ctx, id)
	case "reject":
		return g.Reject(ctx, id)
	case "change":
		what := strings.Join(fs.Args(), " ")
		if what == "" {
			return errors.New(`say what to change: mangoman guardian change "add a contact page"`)
		}
		qid, err := g.Request(what)
		if err == nil {
			fmt.Println("Queued as", qid+". Guardian builds it in dev, QA tests it, then asks you before it goes live.")
		}
		return err
	case "ship":
		qid, err := g.Ship(strings.Join(fs.Args(), " "))
		if err == nil {
			fmt.Println("Queued as", qid+". QA tests it in dev, then Guardian asks you before it goes live.")
		}
		return err
	case "setup":
		if err := g.Setup(ctx); err != nil {
			return err
		}
		fmt.Println("Development environment ready: branch dev, deployed with dev.deploy, running next to production.")
		return nil
	case "telegram":
		return setupTelegram(ctx, g.Dir, id)
	case "run":
	default:
		return fmt.Errorf("unknown guardian command %q (init, setup, run, change, ship, report, incidents, approve, reject, telegram)", sub)
	}
	if *every > 0 {
		return g.Serve(ctx, *every, showEvents)
	}
	events, err := g.Run(ctx)
	showEvents(events)
	if err != nil {
		return err
	}
	for _, e := range events {
		if e.Status == "failing" {
			return errors.New("some checks are still failing") // non-zero exit for schedulers
		}
	}
	return nil
}

func showEvents(events []guardian.Event) {
	for _, e := range events {
		line := fmt.Sprintf("%s  %-10s %s", e.Time.Format("15:04:05"), e.Check, e.Status)
		if e.Detail != "" {
			line += ": " + e.Detail
		}
		fmt.Println(line)
		for _, d := range e.Done {
			fmt.Println("            ran", d)
		}
		if e.Cause != "" {
			fmt.Println("            likely cause:", e.Cause)
		}
	}
}

// setupTelegram links the owner's bot: they send it any message, and that
// chat becomes the only one Guardian listens to.
func setupTelegram(ctx context.Context, state, token string) error {
	if token == "" {
		return errors.New("make a bot with @BotFather in Telegram, then: mangoman guardian telegram BOT_TOKEN")
	}
	t := &guardian.Telegram{Token: token}
	fmt.Println("Now open your bot in Telegram and send it any message (waiting up to 3 minutes)...")
	end := time.Now().Add(3 * time.Minute)
	offset := 0
	for time.Now().Before(end) && ctx.Err() == nil {
		ups, err := t.Updates(ctx, offset, 30*time.Second)
		if err != nil {
			return err
		}
		for _, u := range ups {
			offset = u.ID + 1
			if u.Chat != "" && u.Text != "" {
				t.Chat = u.Chat
				if err := t.Save(state); err != nil {
					return err
				}
				_ = t.Send("Connected. Guardian sends reports here and asks here before anything goes live. Send /report any time.")
				fmt.Println("Connected.")
				return nil
			}
		}
	}
	return errors.New("no message arrived; run the command again")
}

func must[T any](v T, _ error) T { return v }

// cmdNew starts a new app with its development environment from day 1: a
// git project, guardian.json, and the dev branch next to production.
func cmdNew(args []string) error {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return errors.New("usage: mangoman new NAME")
	}
	dir := args[0]
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%s already exists", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	c := guardianStart(must(filepath.Abs(dir)))
	// Nothing is online yet: no deploy commands, data copy or site checks.
	c.Dev, c.Prod, c.Data, c.Checks, c.ProdBranch = guardian.Env{}, guardian.Env{}, guardian.Data{}, nil, "main"
	b, _ := json.MarshalIndent(c, "", "  ")
	files := map[string]string{"README.md": "# " + filepath.Base(dir) + "\n", ".gitignore": ".guardian/\nnode_modules/\n.env\n", "guardian.json": string(b) + "\n"}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			return err
		}
	}
	for _, a := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"-c", "user.name=MangoMan", "-c", "user.email=mangoman@local", "commit", "-q", "-m", "Start " + filepath.Base(dir)}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, a...)...).CombinedOutput(); err != nil {
			return fmt.Errorf("git: %s", out)
		}
	}
	g, err := loadGuardian(filepath.Join(dir, "guardian.json"))
	if err != nil {
		return err
	}
	if err := g.Setup(context.Background()); err != nil {
		return err
	}
	fmt.Printf("Made %s with a development environment (branch dev) next to production (branch main).\n"+
		"Build it: cd %s && mangoman code. When it goes online, add the deploy commands and checks to guardian.json.\n", dir, dir)
	return nil
}
