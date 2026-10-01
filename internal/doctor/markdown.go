package doctor

import (
	"fmt"
	"strings"

	"github.com/pusyc74-prog/mangoman/internal/conformance"
)

// Markdown renders a report as a short summary, for a CI run page.
func Markdown(rep Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## MangoMan doctor\n\nVersion %s, catalogue %s, %d requests in %s.\n\n", rep.Version, rep.Catalogue, rep.Requests, rep.Duration)

	b.WriteString("| Provider | Status | Models listed | Catalogue ids missing | New free models |\n|---|---|---|---|---|\n")
	for _, p := range rep.Providers {
		st := p.Status
		if p.Error != "" && p.Status != StatusOK {
			st += ": " + mdEscape(clip(p.Error, 60))
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %d |\n", p.ID, st, p.ListedModels, len(p.MissingUpstream), len(p.NewFreeModels))
	}

	var ids []string
	for _, c := range conformance.Cases() {
		if !c.DirectOnly {
			ids = append(ids, c.ID)
		}
	}
	hasModels := false
	for _, p := range rep.Providers {
		if len(p.Models) > 0 {
			hasModels = true
		}
	}
	if hasModels {
		b.WriteString("\n| Provider | Model | " + strings.Join(ids, " | ") + " |\n|---|---|" + strings.Repeat("---|", len(ids)) + "\n")
		for _, p := range rep.Providers {
			for _, m := range p.Models {
				got := map[string]conformance.Status{}
				for _, r := range m.Results {
					got[r.Case] = r.Status
				}
				cells := make([]string, len(ids))
				for i, id := range ids {
					cells[i] = icon(got[id])
				}
				fmt.Fprintf(&b, "| %s | %s | %s |\n", p.ID, m.Canonical, strings.Join(cells, " | "))
			}
		}
		b.WriteString("\npass, ~ warn, x fail, - skipped or not run\n")
	}

	var notes []string
	for _, p := range rep.Providers {
		for _, m := range p.Models {
			for _, r := range m.Results {
				if r.Status == conformance.Fail || r.Status == conformance.Warn {
					notes = append(notes, fmt.Sprintf("- **%s** %s/%s `%s`: %s", r.Status, p.ID, m.Canonical, r.Case, mdEscape(r.Detail)))
				}
			}
		}
		for _, id := range p.MissingUpstream {
			notes = append(notes, fmt.Sprintf("- **catalogue** %s: `%s` is not in the provider's model list", p.ID, id))
		}
		for _, i := range p.RateHeaderIssues {
			notes = append(notes, fmt.Sprintf("- **headers** %s: %s", p.ID, mdEscape(i)))
		}
		if len(p.NewFreeModels) > 0 {
			notes = append(notes, fmt.Sprintf("- **radar** %s: %d free models not in the catalogue", p.ID, len(p.NewFreeModels)))
		}
	}
	if len(notes) > 0 {
		b.WriteString("\n### Notes\n\n" + strings.Join(notes, "\n") + "\n")
	}
	return b.String()
}

func icon(s conformance.Status) string {
	switch s {
	case conformance.Pass:
		return "pass"
	case conformance.Warn:
		return "~"
	case conformance.Fail:
		return "x"
	default:
		return "-"
	}
}

func mdEscape(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
