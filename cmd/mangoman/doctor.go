package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/catalogue"
	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/conformance"
	"github.com/pusyc74-prog/mangoman/internal/doctor"
	"github.com/pusyc74-prog/mangoman/internal/keys"
	"github.com/pusyc74-prog/mangoman/internal/providers"
	"github.com/pusyc74-prog/mangoman/internal/store"
)

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	prov := fs.String("provider", "", "comma-separated provider ids (default: all)")
	model := fs.String("model", "", "comma-separated model ids (default: all)")
	cases := fs.String("cases", "", "comma-separated case ids (default: all): "+caseIDs())
	quick := fs.Bool("quick", false, "run only "+strings.Join(conformance.QuickIDs, ", "))
	yes := fs.Bool("yes", false, "do not ask before spending free quota")
	out := fs.String("out", "", "report path (default: config dir/doctor-<time>.json)")
	summary := fs.String("summary", "", "also write a markdown summary to this file (for CI run pages)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cat, err := catalogue.Seed()
	if err != nil {
		return err
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	d := &doctor.Doctor{Cat: cat, Keys: keys.NewResolver(st, envMap(cat)), Client: providers.NewClient(), Version: version}
	o := doctor.Options{Providers: splitList(*prov), Models: splitList(*model), Cases: splitList(*cases), Excluded: cfg.Excluded, Progress: os.Stdout}
	if *quick {
		o.Cases = append(conformance.QuickIDs, "bad_model")
	}

	plan := d.Plan(o)
	total := 0
	var ids []string
	for p, n := range plan {
		if k, _ := d.Keys.Get(p); k == "" {
			if prv, ok := cat.Provider(p); ok && prv.NeedsKey {
				continue
			}
		}
		ids = append(ids, p)
		total += n
	}
	sort.Strings(ids)
	fmt.Println("MangoMan doctor: live checks against your connected providers.")
	fmt.Println("Requests go straight from this machine to each provider with your keys; nothing goes to MangoMan.")
	fmt.Println()
	for _, p := range ids {
		note := ""
		for _, m := range cat.AllModels() {
			if m.Provider == p && m.Limits.RPD > 0 && plan[p] > m.Limits.RPD/3 {
				note = fmt.Sprintf("  (about %d%% of the %d requests/day free limit)", plan[p]*100/m.Limits.RPD, m.Limits.RPD)
				break
			}
		}
		fmt.Printf("  %-11s up to %d requests%s\n", p, plan[p], note)
	}
	if total == 0 {
		fmt.Println("  nothing to check: add a key with `mangoman keys add groq`, or start Ollama")
	}
	if !*yes && total > 0 {
		fmt.Printf("\nThis uses up to %d requests of free quota. Continue? [y/N] ", total)
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Println("Cancelled.")
			return nil
		}
	}
	fmt.Println()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	rep := d.Run(ctx, o)
	printDoctor(rep)

	path := *out
	if path == "" {
		dir, _ := config.Dir()
		path = filepath.Join(dir, "doctor-"+rep.Started.Format("20060102-150405")+".json")
	}
	data, _ := json.MarshalIndent(rep, "", "  ")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	if *summary != "" {
		f, err := os.OpenFile(*summary, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, err = f.WriteString(doctor.Markdown(rep))
		f.Close()
		if err != nil {
			return err
		}
	}
	fmt.Printf("\nFull report: %s\nIt holds no keys and no model answers, so it is safe to share.\n", path)
	return nil
}

func caseIDs() string {
	var ids []string
	for _, c := range conformance.Cases() {
		ids = append(ids, c.ID)
	}
	return strings.Join(ids, ", ")
}

func printDoctor(rep doctor.Report) {
	fmt.Printf("\nDone in %s, %d requests.\n\n", rep.Duration, rep.Requests)
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "PROVIDER\tSTATUS\tMODELS LISTED\tCATALOGUE MODELS MISSING\tNEW FREE MODELS")
	for _, p := range rep.Providers {
		st := p.Status
		if p.Error != "" && p.Status != doctor.StatusOK {
			st += ": " + clipS(p.Error, 50)
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\n", p.ID, st, p.ListedModels, len(p.MissingUpstream), len(p.NewFreeModels))
	}
	_ = tw.Flush()

	fmt.Println()
	tw = tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "PROVIDER\tMODEL\tPASS\tWARN\tFAIL\tSKIP\tVERDICT")
	for _, p := range rep.Providers {
		for _, m := range p.Models {
			pa, w, f, s := m.Counts()
			verdict := "ready"
			switch {
			case !m.Listed && f > 0:
				verdict = "fix catalogue id"
			case f > 0:
				verdict = "needs work"
			case pa == 0:
				verdict = "not tested"
			case w > 0:
				verdict = "ready, with notes"
			}
			fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%d\t%s\n", p.ID, m.Canonical, pa, w, f, s, verdict)
		}
	}
	_ = tw.Flush()

	var notes []string
	for _, p := range rep.Providers {
		for _, m := range p.Models {
			for _, r := range m.Results {
				if r.Status == conformance.Fail || r.Status == conformance.Warn {
					notes = append(notes, fmt.Sprintf("  %s %s/%s %s: %s", strings.ToUpper(string(r.Status)), p.ID, m.Canonical, r.Case, r.Detail))
				}
			}
		}
		if p.BadModel != nil && p.BadModel.Status != conformance.Pass {
			notes = append(notes, fmt.Sprintf("  %s %s bad_model: %s", strings.ToUpper(string(p.BadModel.Status)), p.ID, p.BadModel.Detail))
		}
		for _, id := range p.MissingUpstream {
			notes = append(notes, fmt.Sprintf("  CATALOGUE %s: %q is not in the provider's model list", p.ID, id))
		}
		for _, i := range p.RateHeaderIssues {
			notes = append(notes, fmt.Sprintf("  HEADERS %s: %s", p.ID, i))
		}
		if len(p.NewFreeModels) > 0 {
			notes = append(notes, fmt.Sprintf("  RADAR %s: %d free models not in the catalogue, e.g. %s", p.ID, len(p.NewFreeModels), strings.Join(firstN(p.NewFreeModels, 3), ", ")))
		}
	}
	if len(notes) > 0 {
		fmt.Println("\nNotes:")
		for _, n := range notes {
			fmt.Println(n)
		}
	}
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func clipS(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func cmdUsage(args []string) error {
	fs := flag.NewFlagSet("usage", flag.ContinueOnError)
	days := fs.Int("days", 1, "how many days back")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := config.Path("usage.jsonl")
	if err != nil {
		return err
	}
	s, err := store.Summarize(p, time.Now().Add(-time.Duration(*days)*24*time.Hour))
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(s)
	}
	if s.Requests == 0 {
		fmt.Printf("No requests in the last %d day(s).\n", *days)
		return nil
	}
	rate := func(a, b int) string {
		if b == 0 {
			return "-"
		}
		return fmt.Sprintf("%d%%", a*100/b)
	}
	fmt.Printf("Last %d day(s): %d requests, %d served (%s), %d needed a failover.\n\n",
		*days, s.Requests, s.Served, rate(s.Served, s.Requests), s.FailedOver)
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "PROVIDER\tMODEL\tATTEMPTS\tOK\tRATE LIMITED\tERRORS\tBAD ANSWERS\tTOKENS\tP50 MS")
	for _, r := range s.Rows {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%d\t%d\t%d\t%d\t%d\n", r.Provider, r.Model, r.Attempts, rate(r.OK, r.Attempts), r.RateLimited, r.Errors, r.QualityFail, r.Tokens, r.P50MS)
	}
	return tw.Flush()
}
