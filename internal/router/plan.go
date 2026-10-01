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
		if ok, reset := rt.allow(c, req.EstTokens); !ok {
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
		speed := p.Speed
		if measured, ok := rt.Health.Speed(c.Target()); ok {
			speed = measured
		}
		c.Score = w.Q*m.QualityFor(class) + w.A*rt.share(c) + w.H*health - w.L*(1-speed)
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
		fav, others := rt.splitFavorites(rest)
		out := append(first, fav...)
		out = append(out, sameModelFirst(others)...)
		return rt.cap(out), info
	}
	// My list first, in the user's order; then the router's own ranking;
	// then the best local model as the backstop, always kept last.
	fav, others := rt.splitFavorites(append(cloud, local...))
	var restCloud, restLocal []Candidate
	for _, c := range others {
		if c.Provider.Local {
			restLocal = append(restLocal, c)
		} else {
			restCloud = append(restCloud, c)
		}
	}
	out := rt.cap(append(fav, sameModelFirst(restCloud)...))
	if len(restLocal) > 0 {
		out = append(out, restLocal[0])
	}
	return out, info
}

// splitFavorites moves candidates on My list to the front, ordered by their
// position on the list (score order within one entry, so the same model on
// its best provider is tried first). Anything exhausted or failing was
// already filtered out by plan, so the list naturally falls through.
func (rt *Router) splitFavorites(cs []Candidate) (fav, rest []Candidate) {
	list := rt.Cfg.GetFavorites()
	if len(list) == 0 {
		return nil, cs
	}
	idx := func(c Candidate) int {
		for i, f := range list {
			f = strings.ToLower(f)
			if f == strings.ToLower(c.Model.Canonical) || f == strings.ToLower(c.Model.ID()) {
				return i
			}
		}
		return -1
	}
	pos := map[string]int{}
	for _, c := range cs {
		if i := idx(c); i >= 0 {
			pos[c.Target()] = i
			fav = append(fav, c)
		} else {
			rest = append(rest, c)
		}
	}
	sort.SliceStable(fav, func(i, j int) bool { return pos[fav[i].Target()] < pos[fav[j].Target()] })
	return fav, rest
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

// accountKey is the bucket shared by every model of a provider.
func accountKey(c Candidate) quota.Key {
	return quota.Key{Provider: c.Provider.ID, Account: c.QKey.Account, Model: "*"}
}

func hasLimits(l catalogue.Limits) bool { return l.RPM+l.RPD+l.TPM+l.TPD > 0 }

// allow checks the model's bucket and, if set, the provider account bucket.
func (rt *Router) allow(c Candidate, est int) (bool, time.Time) {
	ok, reset := rt.Quota.Allow(c.QKey, c.Model.Limits, est)
	if !ok || !hasLimits(c.Provider.AccountLimits) {
		return ok, reset
	}
	return rt.Quota.Allow(accountKey(c), c.Provider.AccountLimits, est)
}

func (rt *Router) record(c Candidate, tokens int) {
	rt.Quota.Record(c.QKey, tokens)
	if hasLimits(c.Provider.AccountLimits) {
		rt.Quota.Record(accountKey(c), tokens)
	}
}

func (rt *Router) share(c Candidate) float64 {
	s := rt.Quota.Share(c.QKey, c.Model.Limits)
	if hasLimits(c.Provider.AccountLimits) {
		if a := rt.Quota.Share(accountKey(c), c.Provider.AccountLimits); a < s {
			s = a
		}
	}
	return s
}
