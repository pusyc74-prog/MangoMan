package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

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

const guardianTemplate = `{
  "app": "My app",
  "repo": ".",
  "webhook": "",
  "checks": [
    { "name": "site", "url": "http://localhost:3000/", "max_ms": 3000, "fix": ["systemctl restart my-app"] },
    { "name": "errors", "log": "/var/log/my-app.log" },
    { "name": "jobs", "command": "./scripts/check-jobs.sh", "fix": ["./scripts/retry-failed-jobs.sh"] }
  ]
}
`

func cmdGuardian(args []string) error {
	sub := "run"
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		sub, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("guardian", flag.ContinueOnError)
	path := fs.String("config", "guardian.json", "the checks and fixes")
	every := fs.Duration("every", 0, "run: keep watching, checking this often (for example 5m)")
	send := fs.Bool("send", false, "report: also send it to the webhook")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if sub == "init" {
		if _, err := os.Stat(*path); err == nil {
			return fmt.Errorf("%s already exists", *path)
		}
		if err := os.WriteFile(*path, []byte(guardianTemplate), 0o644); err != nil {
			return err
		}
		fmt.Printf("Wrote %s. Edit the checks, list only fixes that are safe to run any time, then: mangoman guardian run --every 5m\n", *path)
		return nil
	}
	cfg, err := guardian.Load(*path)
	if err != nil {
		return err
	}
	abs, _ := filepath.Abs(*path)
	g := &guardian.Guardian{Cfg: cfg, Dir: filepath.Dir(abs), Wait: 10 * time.Second,
		Ask: func(p string) (string, error) { a, _, err := askRouter(p); return a, err }}
	switch sub {
	case "report":
		rep, err := g.Report(*send)
		fmt.Print(rep)
		return err
	case "run":
	default:
		return fmt.Errorf("unknown guardian command %q (init, run or report)", sub)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	for {
		events, err := g.Run(ctx)
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
		if err != nil {
			return err
		}
		if *every <= 0 {
			for _, e := range events {
				if e.Status == "failing" {
					return errors.New("some checks are still failing") // non-zero exit for schedulers
				}
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(*every):
		}
	}
}
