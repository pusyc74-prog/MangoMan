package guardian

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The owner's own coding (mangoman code) happens in a workspace: a copy on
// its own branch, made from dev. Ship hands it to Guardian, and from there
// it takes the same path as every change: tests from the QA agent, dev,
// QA, the owner's approval, production.

func (g *Guardian) workspaceFile() string { return filepath.Join(g.Dir, "workspace") }

// Workspace returns the owner's current workspace, making one from dev if
// there is none.
func (g *Guardian) Workspace(ctx context.Context) (dir, branch string, err error) {
	if b, err := os.ReadFile(g.workspaceFile()); err == nil {
		dir, branch, _ = strings.Cut(strings.TrimSpace(string(b)), "\n")
		if _, err := os.Stat(dir); err == nil {
			return dir, branch, nil
		}
	}
	if err := g.Setup(ctx); err != nil {
		return "", "", err
	}
	if err := g.sync(); err != nil {
		return "", "", err
	}
	id := "mine-" + time.Now().Format("0102-150405")
	dir, branch = filepath.Join(g.Root, id), "guardian/"+id
	if _, err := git(g.Cfg.Repo, "worktree", "add", "-b", branch, dir, devBranch); err != nil {
		return "", "", err
	}
	return dir, branch, os.WriteFile(g.workspaceFile(), []byte(dir+"\n"+branch), 0o600)
}

// Ship queues the owner's workspace as a change. what describes it; empty
// means the commit messages.
func (g *Guardian) Ship(what string) (string, error) {
	b, err := os.ReadFile(g.workspaceFile())
	if err != nil {
		return "", errors.New("nothing to ship: open your project with mangoman code first")
	}
	dir, branch, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	commit(dir, "Owner's changes")
	log, _ := git(dir, "log", "--format=- %s", devBranch+".."+branch)
	if log == "" {
		return "", errors.New("no changes in your workspace yet")
	}
	if what == "" {
		what = "Your changes:\n" + log
	}
	id := g.add(Incident{Kind: "change", Check: "change", Problem: what, Opened: time.Now(), Manual: true, Work: dir, Branch: branch})
	if id == "" {
		return "", errors.New("could not queue the change")
	}
	_ = os.Remove(g.workspaceFile())
	g.notify(fmt.Sprintf("%s: your change %s is queued for testing in dev.", g.Cfg.App, id))
	return id, nil
}
