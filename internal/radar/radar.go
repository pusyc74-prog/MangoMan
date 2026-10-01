// Package radar watches each connected provider's live model list and
// remembers when every free chat model first appeared. Models that are not
// in the catalogue are offered to the user as new models they can add to
// My list. It reads model lists only; it never sends prompts.
package radar

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/catalogue"
	"github.com/pusyc74-prog/mangoman/internal/keys"
)

// Lister lists a provider's model ids.
type Lister func(ctx context.Context, p catalogue.Provider, key string) ([]string, error)

// Entry is one model seen in a provider's list.
type Entry struct {
	Provider  string    `json:"provider"`
	Upstream  string    `json:"upstream"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	// Baseline marks models already listed on the very first scan: they are
	// available, but not news.
	Baseline bool `json:"baseline"`
}

// Radar holds what has been seen. Safe for concurrent use.
type Radar struct {
	Cat      *catalogue.Catalogue
	Keys     *keys.Resolver
	List     Lister
	Excluded func(provider string) bool
	Path     string // where entries persist; empty = memory only
	Now      func() time.Time

	mu       sync.Mutex
	entries  map[string]*Entry
	lastScan time.Time
	scanned  bool
}

type state struct {
	LastScan time.Time `json:"last_scan"`
	Entries  []*Entry  `json:"entries"`
}

func key(provider, upstream string) string { return provider + "|" + upstream }

func (r *Radar) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Load restores saved entries. A missing file is fine.
func (r *Radar) Load() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = map[string]*Entry{}
	if r.Path == "" {
		return nil
	}
	data, err := os.ReadFile(r.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		return err
	}
	for _, e := range st.Entries {
		r.entries[key(e.Provider, e.Upstream)] = e
	}
	r.lastScan, r.scanned = st.LastScan, !st.LastScan.IsZero()
	return nil
}

func (r *Radar) saveLocked() error {
	if r.Path == "" {
		return nil
	}
	st := state{LastScan: r.lastScan}
	for _, e := range r.entries {
		st.Entries = append(st.Entries, e)
	}
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := r.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.Path)
}

// Scan lists every connected provider once and records what it serves.
// It returns how many models were seen for the first time.
func (r *Radar) Scan(ctx context.Context) (int, error) {
	type found struct {
		p   string
		ids []string
	}
	var results []found
	var firstErr error
	for _, p := range r.Cat.AllProviders() {
		if p.Local || p.Discover.Mode == "" || (r.Excluded != nil && r.Excluded(p.ID)) {
			continue
		}
		k := ""
		if p.NeedsKey {
			if k, _ = r.Keys.Get(p.ID); k == "" {
				continue
			}
		}
		lctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		ids, err := r.List(lctx, p, k)
		cancel()
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		var keep []string
		for _, id := range ids {
			if p.Discover.Matches(id) {
				keep = append(keep, id)
			}
		}
		results = append(results, found{p.ID, keep})
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = map[string]*Entry{}
	}
	now := r.now()
	first := !r.scanned
	added := 0
	for _, f := range results {
		for _, id := range f.ids {
			k := key(f.p, id)
			if e, ok := r.entries[k]; ok {
				e.LastSeen = now
				continue
			}
			r.entries[k] = &Entry{Provider: f.p, Upstream: id, FirstSeen: now, LastSeen: now, Baseline: first}
			if !first {
				added++
			}
		}
	}
	if len(results) > 0 {
		r.lastScan, r.scanned = now, true
	}
	if err := r.saveLocked(); err != nil && firstErr == nil {
		firstErr = err
	}
	return added, firstErr
}

// Item is one model offered on the dashboard.
type Item struct {
	Provider  string    `json:"provider"`
	Upstream  string    `json:"upstream"`
	Name      string    `json:"name"`
	FirstSeen time.Time `json:"first_seen"`
	New       bool      `json:"new"` // appeared after the first scan, within the last 14 days
	Policy    string    `json:"data_policy"`
	Trains    string    `json:"trains_on_data"`
}

// Offer returns models a provider currently lists that are not in the
// catalogue, newest first.
func (r *Radar) Offer() (items []Item, lastScan time.Time) {
	r.mu.Lock()
	var list []Entry
	for _, e := range r.entries {
		list = append(list, *e)
	}
	lastScan = r.lastScan
	r.mu.Unlock()

	now := r.now()
	for _, e := range list {
		// Gone from the provider's list for over two scans: no longer offered.
		if !lastScan.IsZero() && lastScan.Sub(e.LastSeen) > 13*time.Hour {
			continue
		}
		if r.Cat.HasUpstream(e.Provider, e.Upstream) {
			continue
		}
		p, ok := r.Cat.Provider(e.Provider)
		if !ok {
			continue
		}
		m := r.Cat.DiscoveredModel(e.Provider, e.Upstream)
		items = append(items, Item{
			Provider: e.Provider, Upstream: e.Upstream, Name: m.Canonical, FirstSeen: e.FirstSeen,
			New:    !e.Baseline && now.Sub(e.FirstSeen) < 14*24*time.Hour,
			Policy: p.Policy.Label(), Trains: p.Policy.TrainsOnData,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].New != items[j].New {
			return items[i].New
		}
		if !items[i].FirstSeen.Equal(items[j].FirstSeen) {
			return items[i].FirstSeen.After(items[j].FirstSeen)
		}
		return items[i].Provider+items[i].Upstream < items[j].Provider+items[j].Upstream
	})
	return items, lastScan
}

// Listed reports whether a provider currently lists a model.
func (r *Radar) Listed(provider, upstream string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.entries[key(provider, upstream)]
	return ok
}

// Run scans after a short delay, then every interval, until ctx ends.
func (r *Radar) Run(ctx context.Context, delay, every time.Duration, logf func(string, ...any)) {
	t := time.NewTimer(delay)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		n, err := r.Scan(ctx)
		if logf != nil {
			if err != nil {
				logf("radar: %v", err)
			}
			if n > 0 {
				logf("radar: %d new free models (see the dashboard)", n)
			}
		}
		t.Reset(every)
	}
}
