package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/pusyc74-prog/mangoman/internal/agents"
	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/skills"
)

const skillsUsage = `Usage:
  mangoman skills                          list the skill packs
  mangoman skills install [--for T] [--dir PATH]   install them for your coding tools
  mangoman skills remove  [--for T] [--dir PATH]   remove them (only MangoMan's own)

T is claude (Claude Code; OpenCode reads it too), codex, agents (OpenCode and
other tools that read ~/.agents/skills) or all (default). mangoman code loads
the packs automatically without installing anything.
`

func skillTargets(forT, dir string) ([]string, error) {
	if dir != "" {
		return []string{dir}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	m := map[string]string{
		"claude": filepath.Join(home, ".claude", "skills"),
		"codex":  filepath.Join(home, ".codex", "skills"),
		"agents": filepath.Join(home, ".agents", "skills"),
	}
	if forT == "" || forT == "all" {
		return []string{m["claude"], m["codex"], m["agents"]}, nil
	}
	var out []string
	for _, t := range strings.Split(forT, ",") {
		d, ok := m[strings.TrimSpace(t)]
		if !ok {
			return nil, fmt.Errorf("unknown --for %q: use claude, codex, agents or all", t)
		}
		out = append(out, d)
	}
	return out, nil
}

func cmdSkills(args []string) error {
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	flag := func(name string) string { return flagValue(args, name) }
	switch sub {
	case "", "list", "ls":
		packs, err := skills.List()
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "SKILL PACK\tVERSION\tWHAT IT DOES")
		for _, p := range packs {
			d := p.Description
			if i := strings.Index(d, ". Use when"); i > 0 {
				d = d[:i+1]
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", p.Name, p.Version, d)
		}
		_ = tw.Flush()
		fmt.Println("\nmangoman code loads them automatically; mangoman skills install adds them to Claude Code, Codex and OpenCode.")
		return nil
	case "install", "remove":
		dirs, err := skillTargets(flag("--for"), flag("--dir"))
		if err != nil {
			return err
		}
		for _, d := range dirs {
			if sub == "remove" {
				removed, err := skills.Remove(d)
				if err != nil {
					return err
				}
				fmt.Printf("%s: removed %d pack(s)\n", d, len(removed))
				continue
			}
			got, skipped, err := skills.Install(d)
			if err != nil {
				return err
			}
			fmt.Printf("%s: installed %d pack(s)\n", d, len(got))
			for _, s := range skipped {
				fmt.Printf("  left alone %s (a skill of yours with the same name)\n", s)
			}
		}
		if sub == "install" {
			fmt.Println("\nThe packs run Python 3 scripts. For PDFs install Chrome or Playwright; for PowerPoint: pip install python-pptx")
		}
		return nil
	case "help", "-h", "--help":
		fmt.Print(skillsUsage)
		return nil
	}
	fmt.Print(skillsUsage)
	return errors.New("unknown skills command " + sub)
}

// codeSkillsDir prepares the OpenCode config folder mangoman code points
// OPENCODE_CONFIG_DIR at, with the current packs inside.
func codeSkillsDir() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	oc := filepath.Join(dir, "opencode")
	if _, _, err := skills.Install(filepath.Join(oc, "skills")); err != nil {
		return "", err
	}
	ad, err := agentsDir()
	if err != nil {
		return "", err
	}
	return oc, agents.Expose(ad, filepath.Join(oc, "skills"))
}
