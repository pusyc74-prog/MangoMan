package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/pusyc74-prog/mangoman/internal/agents"
	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/skills"
)

const agentsUsage = `Usage:
  mangoman agents                       list installed advanced agents
  mangoman agents search [WORDS]        agents in the marketplace
  mangoman agents install NAME|FILE.mmagent  check signatures and install
  mangoman agents remove NAME           remove an agent
  mangoman agents exec NAME SCRIPT [ARGS]  run an agent's script in the sandbox
  mangoman agents eval NAME [--cases DIR] [--runner CMD]  score it against its free pack

For creators:
  mangoman agents new NAME              start an agent folder from a template
  mangoman agents keygen                make your signing key (keep it safe)
  mangoman agents pack DIR [--out FILE] sign the folder into FILE.mmagent
  mangoman agents review FILE.mmagent|DIR  the marketplace's safety review

For the marketplace:
  mangoman agents index DIR --key FILE  review DIR's packages and write the signed index
`

func agentsDir() (string, error) { return config.Path("agents") }

func cmdAgents(args []string) error {
	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	dir, err := agentsDir()
	if err != nil {
		return err
	}
	need := func(n int) error {
		if len(args) < n {
			fmt.Print(agentsUsage)
			return errors.New("missing argument")
		}
		return nil
	}
	switch sub {
	case "", "list", "ls":
		list, err := agents.List(dir)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Println("No advanced agents installed. Install one with: mangoman agents install FILE.mmagent")
			return nil
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "AGENT\tVERSION\tBY\tRUNS ON\tNETWORK\tIMPROVES ON")
		for _, m := range list {
			net := strings.Join(m.Permissions.Network, ", ")
			if net == "" {
				net = "none"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", m.Name, m.Version, m.Author.Name, m.RunsOn, net, m.Skill)
		}
		return tw.Flush()
	case "install":
		if err := need(1); err != nil {
			return err
		}
		var m agents.Manifest
		if _, statErr := os.Stat(args[0]); statErr != nil && !strings.HasSuffix(args[0], ".mmagent") {
			l, err := agents.DefaultRegistry().InstallListed(args[0], dir)
			if err != nil {
				return err
			}
			m = l.Manifest
		} else if m, err = agents.Install(args[0], dir); err != nil {
			return err
		}
		fmt.Printf("Installed %s %s by %s (%s).\n", m.Name, m.Version, m.Author.Name, m.Author.Contact)
		if len(m.Permissions.Network) > 0 {
			fmt.Println("It may reach:", strings.Join(m.Permissions.Network, ", "))
		}
		fmt.Println("Data policy:", m.DataPolicy)
		return nil
	case "remove", "rm":
		if err := need(1); err != nil {
			return err
		}
		if err := agents.Remove(dir, args[0]); err != nil {
			return err
		}
		fmt.Println("Removed", args[0])
		return nil
	case "exec":
		if err := need(2); err != nil {
			return err
		}
		wd, _ := os.Getwd()
		cmd, err := agents.Command(dir, args[0], wd, args[1], args[2:])
		if err != nil {
			return err
		}
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			os.Exit(exitCode(err))
		}
		return nil
	case "eval":
		if err := need(1); err != nil {
			return err
		}
		return cmdEval(dir, args)
	case "new":
		if err := need(1); err != nil {
			return err
		}
		if err := agents.Scaffold(args[0]); err != nil {
			return err
		}
		fmt.Printf("Started %s/. Edit agent.json and SKILL.md, then: mangoman agents pack %s\n", args[0], args[0])
		return nil
	case "keygen":
		key, err := config.Path("creator.key")
		if err != nil {
			return err
		}
		pub, err := agents.Keygen(key)
		if err != nil {
			return err
		}
		fmt.Printf("Your signing key is in %s. Back it up: updates to your agents must be signed with it.\nPublic key: %s\n", key, pub)
		return nil
	case "pack":
		if err := need(1); err != nil {
			return err
		}
		key, err := config.Path("creator.key")
		if err != nil {
			return err
		}
		src := args[0]
		out := flagValue(args, "--out")
		m, err := agents.Load(src)
		if err != nil {
			return err
		}
		if out == "" {
			out = fmt.Sprintf("%s-%s.mmagent", m.Name, m.Version)
		}
		if _, err := agents.Pack(src, key, out); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return errors.New("no signing key yet: run mangoman agents keygen")
			}
			return err
		}
		fmt.Println("Wrote", out)
		return nil
	case "search":
		ix, err := agents.DefaultRegistry().Fetch()
		if err != nil {
			return err
		}
		q := strings.ToLower(strings.Join(args, " "))
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "AGENT\tVERSION\tBY\tPRICE\tSCORE (PACK / AGENT)\tWHAT IT DOES")
		for _, l := range ix.Agents {
			if q != "" && !strings.Contains(strings.ToLower(l.Name+" "+l.Title+" "+l.Description+" "+l.Skill), q) {
				continue
			}
			price, score := "free", "not scored"
			if l.PriceINR > 0 {
				price = fmt.Sprintf("Rs %d a month", l.PriceINR)
			}
			if l.Score != nil {
				score = fmt.Sprintf("%.0f / %.0f", l.Score.Pack, l.Score.Agent)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", l.Name, l.Version, l.Author.Name, price, score, l.Title)
		}
		return tw.Flush()
	case "index":
		if err := need(1); err != nil {
			return err
		}
		index, sig, err := agents.BuildIndex(args[0], flagValue(args, "--key"))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(args[0], "..", "index.json"), index, 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(args[0], "..", "index.json.sig"), append(sig, '\n'), 0o644); err != nil {
			return err
		}
		fmt.Println("Wrote index.json and index.json.sig next to", args[0])
		return nil
	case "review":
		if err := need(1); err != nil {
			return err
		}
		return cmdReview(args[0])
	case "help", "-h", "--help":
		fmt.Print(agentsUsage)
		return nil
	}
	fmt.Print(agentsUsage)
	return errors.New("unknown agents command " + sub)
}

func flagValue(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, name+"="); ok {
			return v
		}
	}
	return ""
}

// cmdEval scores an installed agent against its free pack on the pack's
// public test set, running both through the same headless OpenCode.
func cmdEval(dir string, args []string) error {
	name := args[0]
	m, err := agents.Load(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	if _, err := codeSkillsDir(); err != nil { // installs the pack and agent for the runner
		return err
	}
	cases := flagValue(args, "--cases")
	if cases == "" {
		// The pack's test set comes from the binary: installed packs leave
		// it out so the model never sees the scorer.
		if cases, err = os.MkdirTemp("", "mangoman-cases-"); err != nil {
			return err
		}
		defer os.RemoveAll(cases)
		if err := skills.Tests(m.Skill, cases); err != nil {
			return fmt.Errorf("no test set for %s: %w", m.Skill, err)
		}
	}
	runner := strings.Fields(flagValue(args, "--runner"))
	if len(runner) == 0 {
		self, err := os.Executable()
		if err != nil {
			return err
		}
		runner = []string{self, "code", "--no-web", "run", "--auto", "--dir", "{dir}", "{prompt}"}
	}
	work, err := os.MkdirTemp("", "mangoman-eval-")
	if err != nil {
		return err
	}
	fmt.Printf("Scoring %s against %s on %s (work in %s)...\n", name, m.Skill, cases, work)
	results, err := agents.Eval(cases, []string{m.Skill, name}, work, func(contender, d, prompt string) error {
		argv := make([]string, len(runner))
		for i, a := range runner {
			argv[i] = strings.NewReplacer("{dir}", d, "{prompt}", prompt, "{skill}", contender).Replace(a)
		}
		c := exec.Command(argv[0], argv[1:]...)
		c.Dir = d
		out, err := c.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v: %s", err, lastLine(string(out)))
		}
		return nil
	})
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintf(tw, "CASE\t%s\t%s\n", m.Skill, name)
	for _, r := range results {
		fmt.Fprintf(tw, "%s\t%.0f\t%.0f\n", r.Case, r.Scores[m.Skill], r.Scores[name])
	}
	pa, aa, beats := agents.Verdict(results, m.Skill, name)
	fmt.Fprintf(tw, "AVERAGE\t%.1f\t%.1f\n", pa, aa)
	_ = tw.Flush()
	b, _ := json.MarshalIndent(results, "", " ")
	if err := os.WriteFile("eval.json", b, 0o644); err != nil {
		return err
	}
	if !beats {
		return fmt.Errorf("%s does not beat the free pack yet (details in eval.json)", name)
	}
	fmt.Printf("%s beats the free pack: it can list as Advanced. Details in eval.json.\n", name)
	return nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	fmt.Fprintln(os.Stderr, "Error:", err)
	return 1
}

// cmdReview runs the marketplace's safety review on a package or folder.
func cmdReview(src string) error {
	if st, err := os.Stat(src); err == nil && !st.IsDir() {
		tmp, err := os.MkdirTemp("", "mangoman-review-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		pub, err := agents.Unpack(src, tmp)
		if err != nil {
			return err
		}
		fmt.Println("Signed by", pub)
		src = tmp
	}
	m, findings, err := agents.Review(src)
	if err != nil {
		return err
	}
	blocked := 0
	for _, f := range findings {
		fmt.Printf("%s  %s:%d  %s\n", strings.ToUpper(f.Level), f.File, f.Line, f.Reason)
		if f.Level == "block" {
			blocked++
		}
	}
	if blocked > 0 {
		return fmt.Errorf("%s %s cannot list: %d blocking finding(s)", m.Name, m.Version, blocked)
	}
	fmt.Printf("%s %s passes the safety review (%d point(s) for a reviewer to look at).\n", m.Name, m.Version, len(findings))
	return nil
}
