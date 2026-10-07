package ingress

import (
	"github.com/pusyc74-prog/mangoman/internal/catalogue"
)

// taskCost is what one task through a skill pack really used, measured on
// free models (see TaskCosts.Measured). Big: ad copy, a product listing, an
// SEO article, a website's copy. Small: an email campaign, a resume, social
// posts. Medians, so one slow run does not move them.
var taskCost = TaskCosts{
	BigRequests: 20, BigTokens: 290_000,
	SmallRequests: 14, SmallTokens: 150_000,
	Measured: "measured on 7 Oct 2026 with Nemotron 3 Ultra on NVIDIA, one run of each task",
}

// TaskCosts says what a task costs, for the free AI estimate.
type TaskCosts struct {
	BigRequests   int    `json:"big_requests"`
	BigTokens     int    `json:"big_tokens"`
	SmallRequests int    `json:"small_requests"`
	SmallTokens   int    `json:"small_tokens"`
	Measured      string `json:"measured"`
}

// FreeRow is how much one connected provider gives each day.
type FreeRow struct {
	Provider string `json:"provider"`
	Keys     int    `json:"keys"`  // your own and your teammates' that work
	Model    string `json:"model"` // the model with the most room
	// Daily limits across all keys; 0 = none published.
	RPD int `json:"rpd,omitempty"`
	TPD int `json:"tpd,omitempty"`
	RPM int `json:"rpm,omitempty"`
	TPM int `json:"tpm,omitempty"` // per key: a request bigger than this never fits
	// Tasks a day on that model; -1 = no daily limit published.
	BigTasks   int `json:"big_tasks"`
	SmallTasks int `json:"small_tasks"`
}

// freeAI works out, for each connected cloud provider, how many tasks a day
// its free limits allow, from the limits on the dashboard's models.
func freeAI(provs []DashProvider, models []DashModel, cat *catalogue.Catalogue) []FreeRow {
	var out []FreeRow
	for _, dp := range provs {
		if dp.Status != PSConnected {
			continue
		}
		n := 0
		if dp.KeySource != "team" {
			n = 1
		}
		for _, t := range dp.TeamKeys {
			if !dp.NoTeamKeys && (t.Status == TKWorking || t.Status == TKResting) {
				n++
			}
		}
		if n == 0 {
			continue
		}
		p, _ := cat.Provider(dp.ID)
		var best FreeRow
		for _, m := range models {
			if m.Provider != dp.ID {
				continue
			}
			l := m.Limits
			if a := p.AccountLimits.RPD; a > 0 && (l.RPD == 0 || a < l.RPD) {
				l.RPD = a
			}
			r := FreeRow{Provider: dp.ID, Keys: n, Model: m.Model, RPD: l.RPD * n, TPD: l.TPD * n, RPM: l.RPM * n, TPM: l.TPM,
				BigTasks:   tasks(l, n, taskCost.BigRequests, taskCost.BigTokens),
				SmallTasks: tasks(l, n, taskCost.SmallRequests, taskCost.SmallTokens)}
			if best.Model == "" || more(r.BigTasks, best.BigTasks) {
				best = r
			}
		}
		if best.Model != "" {
			out = append(out, best)
		}
	}
	return out
}

// tasks is how many tasks of the given cost fit in a day's limits on n keys;
// -1 when no daily limit is published. A per-minute token limit smaller than
// one of the task's requests means none fit: the provider turns them away.
func tasks(l catalogue.Limits, n, req, tok int) int {
	if l.TPM > 0 && l.TPM < tok/req {
		return 0
	}
	t := -1
	if l.RPD > 0 {
		t = l.RPD * n / req
	}
	if l.TPD > 0 && (t < 0 || l.TPD*n/tok < t) {
		t = l.TPD * n / tok
	}
	return t
}

// more reports whether a is more tasks than b, where -1 means no limit.
func more(a, b int) bool {
	if a < 0 || b < 0 {
		return a < 0 && b >= 0
	}
	return a > b
}
