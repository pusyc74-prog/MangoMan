package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Scaffold starts a new agent folder named name in the current directory.
func Scaffold(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("name %q must be lower-case words joined by dashes", name)
	}
	if _, err := os.Stat(name); err == nil {
		return fmt.Errorf("%s already exists", name)
	}
	title := strings.ReplaceAll(name, "-", " ")
	files := map[string]string{
		"agent.json": fmt.Sprintf(`{
  "name": %q,
  "version": "1.0.0",
  "title": %q,
  "description": "What the agent does, in one or two sentences.",
  "skill": "mangoman-ecommerce-listing",
  "author": {"name": "Your name", "contact": "you@example.com"},
  "runs_on": "local",
  "permissions": {"network": [], "commands": []},
  "models": "free",
  "price_inr_month": 0,
  "data_policy": "Everything stays on the user's computer."
}
`, name, title),
		"SKILL.md": fmt.Sprintf(`---
name: %s
description: What the agent does and when to use it. Use when the user asks for ...
metadata:
  version: "1.0"
---
# %s

Steps the model follows. Run scripts only through the sandbox:

    mangoman agents exec %s hello.py

The free pack's scripts (named in agent.json "skill") and the shared helpers
(render, vizlib, brandkit, checks, tracenum) are available to your scripts.
`, name, title, name),
		"scripts/hello.py": "print(\"hello from the sandbox\")\n",
	}
	for n, c := range files {
		p := filepath.Join(name, n)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			return err
		}
	}
	return nil
}
