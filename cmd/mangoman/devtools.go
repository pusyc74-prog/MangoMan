package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pusyc74-prog/mangoman/internal/qa"
	"github.com/pusyc74-prog/mangoman/internal/review"
)

// gitOut runs git in dir and returns its output.
func gitOut(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// cmdCodeReview reviews a change before it ships: secrets block it, risky code
// and new dependencies are flagged, and a free model reviews it for bugs.
func cmdCodeReview(args []string) error {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	base := fs.String("base", "HEAD", "compare with this commit (default: your uncommitted changes)")
	noAI := fs.Bool("no-ai", false, "rules only, no model review")
	dir, err := parseWithDir(fs, args)
	if err != nil {
		return err
	}
	diff, err := gitOut(dir, "diff", *base)
	if err != nil {
		return err
	}
	if strings.TrimSpace(diff) == "" {
		fmt.Println("No changes to review.")
		return nil
	}
	found := review.Scan(diff)
	opinion := ""
	if !*noAI {
		d := diff
		if len(d) > 40000 {
			d = d[:40000] + "\n[cut]"
		}
		opinion, _, _ = askRouter("Review this code change like a careful senior engineer. List real bugs, security problems and missing error handling, " +
			"one short line each with file and line. Say \"No problems found\" if there are none. Do not restate the change.\n\n" + d)
	}
	rep := review.Report(found, opinion)
	fmt.Print(rep)
	if review.Blocking(found) {
		return errors.New("fix the MUST FIX items before this ships")
	}
	return nil
}

// cmdTests has the QA agent write tests for the given files (or the whole
// project), then runs the project's tests.
func cmdTests(args []string) error {
	fs := flag.NewFlagSet("tests", flag.ContinueOnError)
	dirFlag := fs.String("dir", ".", "the project")
	if err := fs.Parse(args); err != nil {
		return err
	}
	target := "the parts of this project with the least test coverage"
	if fs.NArg() > 0 {
		target = strings.Join(fs.Args(), ", ")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	fmt.Println("The QA agent is writing tests for", target+"...")
	prompt := "You are the QA agent. Write automated tests for " + target + `: the normal cases, the edge cases (empty, very large, wrong types, missing data) and the error paths, plus integration tests where parts meet.
Put them next to the project's existing tests, in their style and framework (set one up if there is none). Change only test files. Run them, and fix the tests (not the code) until they run.
If a test shows a real bug in the code, leave that test failing and say so.`
	cmd := exec.Command(self, "code", "--no-web", "run", "--auto", "--dir", must(filepath.Abs(*dirFlag)), prompt)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	results := qa.RunTests(context.Background(), *dirFlag, qa.Detect(*dirFlag))
	fmt.Print(qa.Report(results, nil, nil, ""))
	if qa.Problems(results, nil) != "" {
		return errors.New("some tests fail: either the new tests or the code need a look")
	}
	return nil
}

// cmdChangelog writes release notes from the commits since the last tag
// (or --since) and adds them to the top of CHANGELOG.md.
func cmdChangelog(args []string) error {
	fs := flag.NewFlagSet("changelog", flag.ContinueOnError)
	since := fs.String("since", "", "from this commit or tag (default: the latest tag)")
	version := fs.String("version", "Unreleased", "heading for this release")
	dir, err := parseWithDir(fs, args)
	if err != nil {
		return err
	}
	if *since == "" {
		if tag, err := gitOut(dir, "describe", "--tags", "--abbrev=0"); err == nil {
			*since = strings.TrimSpace(tag)
		}
	}
	rng := "HEAD"
	if *since != "" {
		rng = *since + "..HEAD"
	}
	log, err := gitOut(dir, "log", "--no-merges", "--format=- %s%n%b", rng)
	if err != nil {
		return err
	}
	if strings.TrimSpace(log) == "" {
		return errors.New("no commits to describe")
	}
	if len(log) > 30000 {
		log = log[:30000]
	}
	notes, _, err := askRouter("Write release notes from these commit messages for the people who use this software, not its developers. " +
		"Use the headings ### New, ### Fixed and ### Changed (leave out empty ones), one short plain sentence per item, " +
		"no commit hashes, no internal details. Write only the notes.\n\n" + log)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "CHANGELOG.md")
	old, _ := os.ReadFile(path)
	body := strings.TrimPrefix(string(old), "# Changelog\n")
	entry := "# Changelog\n\n## " + *version + "\n\n" + strings.TrimSpace(notes) + "\n\n" + strings.TrimLeft(body, "\n")
	if err := os.WriteFile(path, []byte(entry), 0o644); err != nil {
		return err
	}
	fmt.Printf("Added %s to %s:\n\n%s\n", *version, path, strings.TrimSpace(notes))
	return nil
}
