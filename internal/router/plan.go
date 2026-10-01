package router

import (
	"sort"
	"strings"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/breaker"
	"github.com/pusyc74-prog/mangoman/internal/catalogue"
	"github.com/pusyc74-prog/mangoman/internal/classify"
	"github.com/pusyc74-prog/mangoman/internal/core"
	"github.com/pusyc74-prog/mangoman/internal/quota"
)

// Candidate is one (model, provider) the router may try.
type Candidate struct {
	Model    catalogue.Model
	Provider catalogue.Provider
	Key      string
	QKey     quota.Key
	Score    float64
}

// Target is the breaker key for a candidate.
func (c Candidate) Target() string { return c.Model.ID() }

// Weights tune the score per task class:
// score = Q*wq + A*wa + H*wh - L*wl
type Weights struct{ Q, A, H, L float64 }

var defaultWeights = Weights{Q: 0.55, A: 0.2, H: 0.15, L: 0.1}

var classWeights = map[string]Weights{
	classify.Reasoning:   {Q: 0.7, A: 0.15, H: 0.1, L: 0.05},
	classify.Code:        {Q: 0.65, A: 0.15, H: 0.15, L: 0.05},
	classify.Fast:        {Q: 0.25, A: 0.2, H: 0.15, L: 0.4},
	classify.LongContext: {Q: 0.5, A: 0.25, H: 0.15, L: 0.1},
}

// Skip reasons, reported when no candidate is left.
type planInfo struct {
	EarliestReset time.Time
	Considered    int
	NoKey         int
	QuotaBlocked  int
	BreakerOpen   int
	DoesNotFit    int
}

// plan returns ranked candidates for a request.
func (rt *Router) plan(req *core.Request, class string) ([]Candidate, planInfo) {
	var info planInfo
	requested := strings.ToLower(strings.TrimSpace(req.Model))
	_, virtual := classify.Virtual[requested]
	explicit := requested != "" && !virtual

	need := req.EstTokens + 1024
	if req.MaxTokens > 0 {
		need = req.EstTokens + req.MaxTokens
	}

	var cloud, local []Candidate
	for _, m := range rt.Cat.AllModels() {
		if !m.Free && !rt.Cfg.PaidFallback {
			continue
		}
		p, ok := rt.Cat.Provider(m.Provider)
		if !ok || rt.Cfg.Excluded(p.ID) {
			continue
		}
		info.Considered++
		key := ""
		if p.NeedsKey {
			k, src := rt.Keys.Get(p.ID)
			if src == "" || k == "" {
				info.NoKey++
				continue
			}
			key = k
		}
		if m.Context > 0 && need > m.Context ||
			req.HasTools() && !m.Has("tools") ||
			req.WantsJSON && !m.Has("json") ||
			req.HasImages && !m.Has("vision") {
			info.DoesNotFit++
			continue
		}
		c := Candidate{Model: m, Provider: p, Key: key, QKey: quota.Key{Provider: p.ID, Account: "default", Model: m.Canonical}}
		state := rt.Breakers.StateOf(c.Target())
		if state == breaker.Open {
			info.BreakerOpen++
			continue
		}
		if ok, reset := rt.Quota.Allow(c.QKey, m.Limits, req.EstTokens); !ok {
			info.QuotaBlocked++
			if info.EarliestReset.IsZero() || reset.Before(info.EarliestReset) {
				info.EarliestReset = reset
			}
			continue
		}
		w, ok := classWeights[class]
		if !ok {
			w = defaultWeights
		}
		health := 1.0
		if state == breaker.HalfOpen {
			health = 0.5
		}
		c.Score = w.Q*m.QualityFor(class) + w.A*rt.Quota.Share(c.QKey, m.Limits) + w.H*health - w.L*(1-p.Speed)
		if p.Local {
			local = append(local, c)
		} else {
			cloud = append(cloud, c)
		}
	}

	byScore := func(cs []Candidate) {
		sort.SliceStable(cs, func(i, j int) bool { return cs[i].Score > cs[j].Score })
	}
	byScore(cloud)
	byScore(local)

	if explicit {
		// The asked-for model on every provider first, then everything else.
		match := func(c Candidate) bool {
			return strings.ToLower(c.Model.Canonical) == requested || strings.ToLower(c.Model.ID()) == requested
		}
		var first, rest []Candidate
		for _, c := range append(cloud, local...) {
			if match(c) {
				first = append(first, c)
			} else {
				rest = append(rest, c)
			}
		}
		// A provider-qualified id ("groq/llama-3.3-70b") pins that provider first.
		sort.SliceStable(first, func(i, j int) bool {
			return strings.ToLower(first[i].Model.ID()) == requested && strings.ToLower(first[j].Model.ID()) != requested
		})
		out := append(first, sameModelFirst(rest)...)
		return rt.cap(out), info
	}
	// Local models are the backstop: the best one is always tried last, after
	// the capped list of cloud candidates.
	out := rt.cap(sameModelFirst(cloud))
	if len(local) > 0 {
		out = append(out, local[0])
	}
	return out, info
}

// sameModelFirst keeps score order but, after each model, puts the same
// canonical model on other providers next, so a failover keeps the output
// style consistent before switching to a different model.
func sameModelFirst(cs []Candidate) []Candidate {
	out := make([]Candidate, 0, len(cs))
	used := make([]bool, len(cs))
	for i := range cs {
		if used[i] {
			continue
		}
		used[i] = true
		out = append(out, cs[i])
		for j := i + 1; j < len(cs); j++ {
			if !used[j] && cs[j].Model.Canonical == cs[i].Model.Canonical {
				used[j] = true
				out = append(out, cs[j])
			}
		}
	}
	return out
}

func (rt *Router) cap(cs []Candidate) []Candidate {
	if n := rt.Cfg.MaxAttempts; n > 0 && len(cs) > n {
		return cs[:n]
	}
	return cs
}
