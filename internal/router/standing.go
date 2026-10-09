package router

import (
	"math/rand"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/guard"
)

// How a model earns its place in line. After every attempt, ruleFor decides
// what the outcome does to the model, and nothing else does:
//
//   - strikes: failures in a row. At three the model is skipped for 30 s,
//     doubling each time up to 5 minutes, then one probe request is let
//     through. A skipped model is left out even when it is the only one.
//   - last: the model stays in the list but is tried behind the others for
//     a while. Used for trouble that comes and goes, measured in real runs.
//   - word: why the model was given up on, for the coding screen.
//
// Rate limits are not here: the provider says when they end (package quota).

// Tried-last times. Measured on NVIDIA: about one request in five came back
// overloaded, too few in a row to trip a skip, so every request lost time on
// the same busy model first; Kimi K3 and DeepSeek stayed silent past the
// first-word limit 23 times in one run, each costing the user a minute; a
// garbled answer is a broken deployment, which does not mend in minutes.
const (
	busyFor    = 2 * time.Minute
	silentFor  = 10 * time.Minute
	garbledFor = 30 * time.Minute
)

// Skip settings: strikes in a row before a skip, the first skip, the longest.
const (
	skipAfter = 3
	skipBase  = 30 * time.Second
	skipMax   = 5 * time.Minute
)

// rule is what one attempt's outcome does to the model's place in line.
type rule struct {
	strikes int           // added to the strikes; clear wipes them
	clear   bool          // the model answered: strikes and skips are forgotten
	last    time.Duration // tried behind the others for this long
	word    string        // why it was given up on: busy, slow, garbled, empty or failed
}

// ruleFor is the one place that decides demotion, its reason and its expiry.
func ruleFor(res attemptResult) rule {
	switch o := res.outcome; {
	case strings.HasPrefix(o, "ok"):
		return rule{clear: true}
	case overloaded(res):
		return rule{strikes: 1, last: busyFor, word: "busy"}
	case o == "timeout":
		return rule{strikes: 1, last: silentFor, word: "slow"}
	case o == "garbled":
		return rule{strikes: 1, last: garbledFor, word: "garbled"}
	case o == "no_answer":
		return rule{strikes: 1, last: busyFor, word: "empty"}
	case o == "model_forbidden", o == "model_not_found":
		// About this one model (removed, retired, locked to a tier or region),
		// not the key or the provider: skip it at once.
		return rule{strikes: skipAfter, word: "failed"}
	case o == "network_error", o == "read_error", o == "error_in_200", o == "not_a_stream",
		o == "stream_error", o == "stream_broken_after_commit", o == "server_error",
		o == "quality:"+guard.Unparseable:
		return rule{strikes: 1, word: "failed"}
	case strings.HasPrefix(o, "quality:"):
		// The model answered; the answer failed a check. Not a fault of the model's service.
		return rule{clear: true, word: "failed"}
	case o == "rate_limited":
		return rule{word: "busy"}
	}
	// A bad key, a request the provider refused, the client hanging up: they
	// say nothing about the model.
	return rule{word: "failed"}
}

// Line keeps every model's place in line.
type Line struct {
	mu        sync.Mutex
	m         map[string]*place
	skipAfter int
	now       func() time.Time
	jitter    func(time.Duration) time.Duration
}

type place struct {
	strikes   int
	skips     int       // skips in a row; each doubles the next
	skipUntil time.Time // zero: not skipped
	lastUntil time.Time // tried behind the others until then
	probing   bool      // a probe is out after a skip ended
}

// NewLine returns an empty line.
func NewLine() *Line {
	return &Line{
		m: map[string]*place{}, skipAfter: skipAfter, now: time.Now,
		jitter: func(d time.Duration) time.Duration { return time.Duration(rand.Int63n(int64(d)/5 + 1)) },
	}
}

// SetClock replaces the clock and removes jitter, for tests.
func (l *Line) SetClock(now func() time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.now = now
	l.jitter = func(time.Duration) time.Duration { return 0 }
}

func (l *Line) get(t string) *place {
	p, ok := l.m[t]
	if !ok {
		p = &place{}
		l.m[t] = p
	}
	return p
}

// judge applies one attempt's rule to a model, and ends its probe if one was out.
func (l *Line) judge(t string, r rule) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p := l.get(t)
	now := l.now()
	if r.last > 0 {
		p.lastUntil = now.Add(r.last)
	}
	switch {
	case r.clear:
		p.strikes, p.skips, p.skipUntil = 0, 0, time.Time{}
	case r.strikes > 0:
		p.strikes += r.strikes
		if p.probing || p.strikes >= l.skipAfter {
			d := skipBase << p.skips
			if d > skipMax || d <= 0 {
				d = skipMax
			}
			p.skipUntil = now.Add(d + l.jitter(d))
			p.skips++
			p.strikes = 0
		}
	}
	p.probing = false
}

// allow reports whether a request may go to the model. When its skip has
// ended, a single probe is let through.
func (l *Line) allow(t string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	p := l.get(t)
	switch {
	case p.skipUntil.IsZero():
		return true
	case l.now().Before(p.skipUntil), p.probing:
		return false
	}
	p.probing = true
	return true
}

// SkippedUntil returns when a skipped model may be asked again; zero when it
// is not skipped.
func (l *Line) SkippedUntil(t string) time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	if p, ok := l.m[t]; ok && l.now().Before(p.skipUntil) {
		return p.skipUntil
	}
	return time.Time{}
}

// probation reports whether a model's skip ended and it has not answered
// since: it ranks lower until it does.
func (l *Line) probation(t string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	p, ok := l.m[t]
	return ok && !p.skipUntil.IsZero() && !l.now().Before(p.skipUntil)
}

// order moves models tried last behind the others. They stay in the list:
// with one model to choose from (strict mode), it is still asked.
func (l *Line) order(cs []Candidate) []Candidate {
	l.mu.Lock()
	now := l.now()
	last := map[string]bool{}
	for _, c := range cs {
		if p, ok := l.m[c.Target()]; ok && now.Before(p.lastUntil) {
			last[c.Target()] = true
		}
	}
	l.mu.Unlock()
	if len(last) == 0 {
		return cs
	}
	out := slices.Clone(cs)
	sort.SliceStable(out, func(i, j int) bool { return !last[out[i].Target()] && last[out[j].Target()] })
	return out
}
