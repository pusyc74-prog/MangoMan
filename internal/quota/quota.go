// Package quota tracks free-tier usage per (provider, account, model) so the
// router moves on before a limit is hit. Buckets are seeded from catalogue
// limits and corrected from rate-limit headers and 429 responses.
package quota

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/catalogue"
)

// Key identifies one bucket set.
type Key struct {
	Provider string `json:"provider"`
	Account  string `json:"account"`
	Model    string `json:"model"`
}

func (k Key) String() string { return k.Provider + "|" + k.Account + "|" + k.Model }

type window struct {
	Used  int       `json:"used"`
	Start time.Time `json:"start"`
}

type bucket struct {
	ReqMin, ReqDay, TokMin, TokDay window
	BlockedUntil                   time.Time `json:"blocked_until"`
	// Learned holds limits reported by the provider's own headers. They
	// override the catalogue seed, which may be out of date.
	Learned catalogue.Limits `json:"learned"`
}

// effective merges learned limits over catalogue limits.
func (b *bucket) effective(l catalogue.Limits) catalogue.Limits {
	if b.Learned.RPM > 0 {
		l.RPM = b.Learned.RPM
	}
	if b.Learned.RPD > 0 {
		l.RPD = b.Learned.RPD
	}
	if b.Learned.TPM > 0 {
		l.TPM = b.Learned.TPM
	}
	if b.Learned.TPD > 0 {
		l.TPD = b.Learned.TPD
	}
	return l
}

// Tracker holds all buckets. Safe for concurrent use.
type Tracker struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	keys    map[string]Key
	now     func() time.Time
}

// New returns an empty tracker.
func New() *Tracker {
	return &Tracker{buckets: map[string]*bucket{}, keys: map[string]Key{}, now: time.Now}
}

// SetClock replaces the clock, for tests.
func (t *Tracker) SetClock(now func() time.Time) { t.now = now }

func (t *Tracker) get(k Key) *bucket {
	s := k.String()
	b, ok := t.buckets[s]
	if !ok {
		b = &bucket{}
		t.buckets[s] = b
		t.keys[s] = k
	}
	return b
}

// roll resets a window whose period has passed and returns when it resets.
func roll(w *window, period time.Duration, now time.Time) time.Time {
	if w.Start.IsZero() || now.Sub(w.Start) >= period {
		w.Start = now
		w.Used = 0
	}
	return w.Start.Add(period)
}

// Allow is the pre-flight check: it reports whether a request with the given
// estimated tokens fits every window, and if not, when the earliest blocking
// window resets.
func (t *Tracker) Allow(k Key, l catalogue.Limits, estTokens int) (bool, time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	b := t.get(k)
	if now.Before(b.BlockedUntil) {
		return false, b.BlockedUntil
	}
	l = b.effective(l)
	var resetAt time.Time
	check := func(w *window, period time.Duration, limit, add int) {
		if limit <= 0 {
			return
		}
		r := roll(w, period, now)
		if w.Used+add > limit {
			if resetAt.IsZero() || r.Before(resetAt) {
				resetAt = r
			}
		}
	}
	check(&b.ReqMin, time.Minute, l.RPM, 1)
	check(&b.ReqDay, 24*time.Hour, l.RPD, 1)
	// A single request larger than a whole per-minute token budget can never
	// fit; do not block on it, let the provider decide.
	if l.TPM <= 0 || estTokens <= l.TPM {
		check(&b.TokMin, time.Minute, l.TPM, estTokens)
	}
	check(&b.TokDay, 24*time.Hour, l.TPD, estTokens)
	return resetAt.IsZero(), resetAt
}

// Record counts a completed request.
func (t *Tracker) Record(k Key, tokens int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	b := t.get(k)
	roll(&b.ReqMin, time.Minute, now)
	roll(&b.ReqDay, 24*time.Hour, now)
	roll(&b.TokMin, time.Minute, now)
	roll(&b.TokDay, 24*time.Hour, now)
	b.ReqMin.Used++
	b.ReqDay.Used++
	b.TokMin.Used += tokens
	b.TokDay.Used += tokens
}

// Block marks a bucket exhausted until a time (after a 429).
func (t *Tracker) Block(k Key, until time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	b := t.get(k)
	if until.After(b.BlockedUntil) {
		b.BlockedUntil = until
	}
}

// Share returns remaining capacity as a 0..1 share of the tightest limit.
func (t *Tracker) Share(k Key, l catalogue.Limits) float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	b := t.get(k)
	if now.Before(b.BlockedUntil) {
		return 0
	}
	l = b.effective(l)
	share := 1.0
	f := func(w *window, period time.Duration, limit int) {
		if limit <= 0 {
			return
		}
		roll(w, period, now)
		s := 1 - float64(w.Used)/float64(limit)
		if s < share {
			share = s
		}
	}
	f(&b.ReqMin, time.Minute, l.RPM)
	f(&b.ReqDay, 24*time.Hour, l.RPD)
	f(&b.TokMin, time.Minute, l.TPM)
	f(&b.TokDay, 24*time.Hour, l.TPD)
	if share < 0 {
		share = 0
	}
	return share
}

// FromHeaders reads rate-limit headers. With rules (from the catalogue) it
// learns the provider's real limits and syncs the local window to the
// provider's count. Without rules it only blocks when a generic
// x-ratelimit-remaining-* header reaches zero.
func (t *Tracker) FromHeaders(k Key, h http.Header, rules []catalogue.RateHeader) {
	if len(rules) == 0 {
		t.genericHeaders(k, h)
		return
	}
	for _, r := range rules {
		rem, ok := headerInt(h, r.Remaining)
		if !ok {
			continue
		}
		limit, _ := headerInt(h, r.Limit)
		t.Observe(k, r.Kind, r.Window, limit, rem, ParseReset(h.Get(r.Reset)))
	}
}

func (t *Tracker) genericHeaders(k Key, h http.Header) {
	for _, kind := range []string{"requests", "tokens"} {
		n, ok := headerInt(h, "x-ratelimit-remaining-"+kind)
		if !ok || n > 0 {
			continue
		}
		d := ParseReset(h.Get("x-ratelimit-reset-" + kind))
		if d <= 0 {
			d = time.Minute
		}
		t.Block(k, t.now().Add(d))
	}
}

func headerInt(h http.Header, name string) (int, bool) {
	if name == "" {
		return 0, false
	}
	v := strings.TrimSpace(h.Get(name))
	if v == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, false
	}
	return int(f), true
}

// Observe applies one provider-reported window: kind is "requests" or
// "tokens", window is "minute" or "day". limit may be 0 when unknown.
func (t *Tracker) Observe(k Key, kind, win string, limit, remaining int, resetIn time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	b := t.get(k)
	var w *window
	var period time.Duration
	var lim *int
	switch {
	case kind == "requests" && win == "minute":
		w, period, lim = &b.ReqMin, time.Minute, &b.Learned.RPM
	case kind == "requests" && win == "day":
		w, period, lim = &b.ReqDay, 24*time.Hour, &b.Learned.RPD
	case kind == "tokens" && win == "minute":
		w, period, lim = &b.TokMin, time.Minute, &b.Learned.TPM
	case kind == "tokens" && win == "day":
		w, period, lim = &b.TokDay, 24*time.Hour, &b.Learned.TPD
	default:
		return
	}
	if limit > 0 {
		*lim = limit
		used := limit - remaining
		if used < 0 {
			used = 0
		}
		w.Used = used
		// Align our window with the provider's reset time.
		if resetIn > 0 && resetIn <= period {
			w.Start = now.Add(resetIn - period)
		} else if w.Start.IsZero() {
			w.Start = now
		}
	}
	if remaining <= 0 {
		until := now.Add(resetIn)
		if resetIn <= 0 {
			until = now.Add(time.Minute)
		}
		if until.After(b.BlockedUntil) {
			b.BlockedUntil = until
		}
	}
}

// Learned returns limits learned from headers for a bucket.
func (t *Tracker) Learned(k Key) catalogue.Limits {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.get(k).Learned
}

// RetryAfter returns how long a 429 asks us to wait, defaulting to a minute.
func RetryAfter(h http.Header) time.Duration {
	if v := h.Get("Retry-After"); v != "" {
		if s, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && s > 0 {
			return time.Duration(s) * time.Second
		}
		if ts, err := http.ParseTime(v); err == nil {
			if d := time.Until(ts); d > 0 {
				return d
			}
		}
	}
	for _, kind := range []string{"requests", "tokens"} {
		if d := ParseReset(h.Get("x-ratelimit-reset-" + kind)); d > 0 {
			return d
		}
	}
	return time.Minute
}

// ParseReset parses reset values such as "2m59.56s", "7.66s", "30" (seconds)
// or a unix timestamp in milliseconds.
func ParseReset(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		if f > 1e12 { // unix ms
			return time.Until(time.UnixMilli(int64(f)))
		}
		return time.Duration(f * float64(time.Second))
	}
	return 0
}

// Status is a read-only view of one bucket.
type Status struct {
	Key          Key       `json:"key"`
	ReqToday     int       `json:"requests_today"`
	TokToday     int       `json:"tokens_today"`
	BlockedUntil time.Time `json:"blocked_until,omitempty"`
}

// Snapshot returns all buckets.
func (t *Tracker) Snapshot() []Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	out := make([]Status, 0, len(t.buckets))
	for s, b := range t.buckets {
		roll(&b.ReqDay, 24*time.Hour, now)
		roll(&b.TokDay, 24*time.Hour, now)
		st := Status{Key: t.keys[s], ReqToday: b.ReqDay.Used, TokToday: b.TokDay.Used}
		if now.Before(b.BlockedUntil) {
			st.BlockedUntil = b.BlockedUntil
		}
		out = append(out, st)
	}
	return out
}

type persisted struct {
	Key    Key     `json:"key"`
	Bucket *bucket `json:"bucket"`
}

// Save writes buckets to a file so daily counts survive restarts.
func (t *Tracker) Save(path string) error {
	t.mu.Lock()
	list := make([]persisted, 0, len(t.buckets))
	for s, b := range t.buckets {
		list = append(list, persisted{Key: t.keys[s], Bucket: b})
	}
	data, err := json.Marshal(list)
	t.mu.Unlock()
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load restores buckets saved by Save. A missing file is not an error.
func (t *Tracker) Load(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var list []persisted
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, p := range list {
		if p.Bucket != nil {
			t.buckets[p.Key.String()] = p.Bucket
			t.keys[p.Key.String()] = p.Key
		}
	}
	return nil
}
