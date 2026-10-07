package router

import (
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/breaker"
	"github.com/pusyc74-prog/mangoman/internal/catalogue"
	"github.com/pusyc74-prog/mangoman/internal/classify"
	"github.com/pusyc74-prog/mangoman/internal/core"
	"github.com/pusyc74-prog/mangoman/internal/keys"
	"github.com/pusyc74-prog/mangoman/internal/quota"
)

// Candidate is one (model, provider) the router may try.
type Candidate struct {
	Model    catalogue.Model
	Provider catalogue.Provider
	Key      string
	QKey     quota.Key
	Score    float64
	// Weak marks a model clearly less skilled than the best connected one
	// for this request; the router asks before using it (see AllowWeaker).
	Weak  bool
	q     float64 // skill for this request's task class
	tight bool    // fits now, but a growing chat would soon outgrow it
}

// weakGap is how far below the best connected model's skill a model counts
// as weak. 0.12 keeps gpt-oss-120b, GLM, DeepSeek and Big Pickle level with
// Kimi, and treats the small, fast models as weak.
const weakGap = 0.12

// Target is the breaker key for a candidate.
func (c Candidate) Target() string { return c.Model.ID() }

// teamKey is the teammate whose key this candidate uses, or "" for the
// user's own key.
func (c Candidate) teamKey() string {
	if c.QKey.Account == keys.Own {
		return ""
	}
	return c.QKey.Account
}

// keyName is the key store entry this candidate's key came from.
func (c Candidate) keyName() string { return keys.Name(c.Provider.ID, c.QKey.Account) }

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
	Weaker        int // weak models left out because the user did not allow them
	LocalTooSmall int // local models that fit except for their context size
	OverMinute    int // models whose free tier takes fewer tokens a minute than the request needs
	MinuteCap     int // the largest such per-minute cap
	Need          int // tokens the request needs (input plus output)
}

// plan returns ranked candidates for a request.
func (rt *Router) plan(req *core.Request, class string) ([]Candidate, planInfo) {
	var info planInfo
	requested := strings.ToLower(strings.TrimSpace(req.Model))
	_, virtual := classify.Virtual[requested]
	explicit := requested != "" && !virtual
	// Strict scope: "strict/<model>" uses only that model, "group/<name>"
	// only the group's models. Nothing outside the scope is ever tried.
	scope := rt.scope(requested)
	inScope := func(m catalogue.Model) bool {
		if scope == nil {
			return true
		}
		return scopeIndex(scope, m) >= 0
	}

	need := req.EstTokens + 1024
	if req.MaxTokens > 0 {
		need = req.EstTokens + req.MaxTokens
	}
	info.Need = need

	split := rt.splitFavorites
	if req.Internal {
		split = func(cs []Candidate) ([]Candidate, []Candidate) { return nil, cs }
	}
	var cloud, local []Candidate
	var top float64
	for _, m := range rt.Cat.AllModels() {
		if !m.Free && !rt.Cfg.PaidFallback {
			continue
		}
		p, ok := rt.Cat.Provider(m.Provider)
		if !ok || rt.Cfg.Excluded(p.ID) || !inScope(m) {
			continue
		}
		info.Considered++
		slots := rt.keySlots(p)
		if len(slots) == 0 {
			info.NoKey++
			continue
		}
		capsOK := !(req.HasTools() && !m.Has("tools") ||
			req.WantsJSON && !m.Has("json") ||
			req.HasImages && !m.Has("vision"))
		if !capsOK || m.Context > 0 && need > m.Context {
			info.DoesNotFit++
			if capsOK && p.Local {
				info.LocalTooSmall++
			}
			continue
		}
		// One candidate per key: each has its own limits, so a teammate's key
		// takes over the same model when another key is used up.
		for _, s := range slots {
			c := Candidate{Model: m, Provider: p, Key: s.key, QKey: quota.Key{Provider: p.ID, Account: s.account, Model: m.Canonical}, q: m.QualityFor(class)}
			// The best skill among models that could take this request at all,
			// even if busy now: the bar for calling a model weak.
			top = max(top, c.q)
			// A request bigger than a whole minute's token allowance is always
			// refused (Groq's free tier: 8,000), so do not spend an attempt on it.
			size := m.Context
			if l := rt.Quota.Effective(c.QKey, m.Limits); l.TPM > 0 {
				if need > l.TPM {
					info.DoesNotFit++
					info.OverMinute++
					info.MinuteCap = max(info.MinuteCap, l.TPM)
					continue
				}
				if size == 0 || l.TPM < size {
					size = l.TPM
				}
			}
			c.tight = size > 0 && size < 2*need
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
			if r, ok := rt.Health.Rate(c.Target()); ok {
				health = r
			}
			if state == breaker.HalfOpen {
				health /= 2
			}
			speed := p.Speed
			if measured, ok := rt.Health.Speed(c.Target()); ok {
				speed = measured
			}
			c.Score = w.Q*c.q + w.A*rt.share(c) + w.H*health - w.L*(1-speed)
			if p.Local {
				local = append(local, c)
			} else {
				cloud = append(cloud, c)
			}
		}
	}

	for _, cs := range [][]Candidate{cloud, local} {
		for i := range cs {
			cs[i].Weak = cs[i].q < top-weakGap
		}
	}
	// Strong before weak, roomy before tight, then provider priority, then
	// score: a chat stays on a model that can hold it as it grows.
	sort.SliceStable(cloud, func(i, j int) bool {
		a, b := cloud[i], cloud[j]
		if a.Weak != b.Weak {
			return b.Weak
		}
		if a.tight != b.tight {
			return b.tight
		}
		if pa, pb := priority(a.Provider), priority(b.Provider); pa != pb {
			return pa < pb
		}
		return a.Score > b.Score
	})
	sort.SliceStable(local, func(i, j int) bool { return local[i].Score > local[j].Score })

	if scope != nil {
		// Scope order (the user's order), then score within one entry, so the
		// same model on its best provider goes first.
		all := append(cloud, local...)
		sort.SliceStable(all, func(i, j int) bool { return scopeIndex(scope, all[i].Model) < scopeIndex(scope, all[j].Model) })
		return rt.cap(all), info
	}
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
		fav, others := split(rest)
		out := append(first, fav...)
		out = append(out, sameModelFirst(others)...)
		return rt.cap(out), info
	}
	// My list first, in the user's order; then the router's own ranking,
	// led by the model this chat already uses; then the best local model as
	// the backstop, always kept last. Weak models, and with a My list every
	// model outside it, only if the user allows.
	allowWeak := req.AllowWeaker || req.Internal || rt.allowWeaker()
	stuck := rt.stuck(req)
	fav, others := split(append(cloud, local...))
	hasList := len(rt.Cfg.GetFavorites()) > 0
	var lead, restCloud, restLocal []Candidate
	for _, c := range others {
		switch {
		case (c.Weak || hasList) && !allowWeak:
			info.Weaker++
		case c.Provider.Local:
			restLocal = append(restLocal, c)
		case c.Model.Canonical == stuck && !c.Weak:
			lead = append(lead, c)
		default:
			restCloud = append(restCloud, c)
		}
	}
	out := rt.cap(append(fav, sameModelFirst(append(lead, restCloud...))...))
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

type keySlot struct{ account, key string }

// keySlots lists the keys this machine has for a provider: the user's own
// first, then each team key in the order it was added. A provider that needs
// no key has one empty slot; one with no usable key has none.
func (rt *Router) keySlots(p catalogue.Provider) []keySlot {
	if !p.NeedsKey {
		return []keySlot{{account: keys.Own}}
	}
	var out []keySlot
	if k, _ := rt.Keys.Get(p.ID); k != "" {
		out = append(out, keySlot{keys.Own, k})
	}
	if p.NoTeamKeys {
		return out
	}
	for _, name := range rt.Cfg.GetTeamKeys(p.ID) {
		if k, _ := rt.Keys.Get(keys.Name(p.ID, name)); k != "" {
			out = append(out, keySlot{name, k})
		}
	}
	return out
}

// HasKey reports whether this machine has a usable key for a provider, the
// user's own or a teammate's.
func (rt *Router) HasKey(p catalogue.Provider) bool { return len(rt.keySlots(p)) > 0 }

// takeTurns spreads requests across the keys of one model: each request
// starts on the next key in turn, so no teammate's key always goes first and
// keys reach their limits less often. The order between models is unchanged.
func (rt *Router) takeTurns(cs []Candidate) []Candidate {
	pos := map[string][]int{}
	for i, c := range cs {
		pos[c.Target()] = append(pos[c.Target()], i)
	}
	out := slices.Clone(cs)
	for t, idx := range pos {
		if len(idx) < 2 {
			continue
		}
		// Rotate from a fixed order (by key name): the ranking order moves as
		// each key's remaining limit changes, and rotating a moving order
		// would not give each key its turn.
		group := make([]Candidate, len(idx))
		for j, i := range idx {
			group[j] = cs[i]
		}
		sort.Slice(group, func(a, b int) bool { return group[a].QKey.Account < group[b].QKey.Account })
		n := rt.turn(t)
		for j, i := range idx {
			out[i] = group[(j+n)%len(group)]
		}
	}
	return out
}

// priority is a provider's place in the router's own ranking (1 first).
func priority(p catalogue.Provider) int {
	if p.Priority == 0 {
		return 3
	}
	return p.Priority
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

// StrictPrefix and GroupPrefix select a strict scope in the model field.
const (
	StrictPrefix = "strict/"
	GroupPrefix  = "group/"
)

// scope returns the entries a strict request may use, or nil for normal
// routing. An unknown group yields an empty, non-nil scope (no candidates).
func (rt *Router) scope(requested string) []string {
	switch {
	case strings.HasPrefix(requested, StrictPrefix):
		return []string{strings.TrimPrefix(requested, StrictPrefix)}
	case strings.HasPrefix(requested, GroupPrefix):
		g := rt.Cfg.Group(strings.TrimPrefix(requested, GroupPrefix))
		if g == nil {
			return []string{}
		}
		return g
	}
	return nil
}

// scopeIndex is the position of the first scope entry naming m, or -1.
// An entry is a model name (any provider) or provider/model.
func scopeIndex(scope []string, m catalogue.Model) int {
	for i, e := range scope {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == strings.ToLower(m.Canonical) || e == strings.ToLower(m.ID()) {
			return i
		}
	}
	return -1
}
