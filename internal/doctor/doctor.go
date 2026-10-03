// Package doctor runs live checks against every connected provider: is the
// key accepted, does the provider still serve the models the catalogue lists,
// does each model pass the conformance corpus, and do the rate-limit headers
// match what the catalogue expects. The report holds no keys and no answers,
// so it is safe to share.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/catalogue"
	"github.com/pusyc74-prog/mangoman/internal/conformance"
	"github.com/pusyc74-prog/mangoman/internal/core"
	"github.com/pusyc74-prog/mangoman/internal/keys"
	"github.com/pusyc74-prog/mangoman/internal/providers"
)

// Options narrow a run.
type Options struct {
	Providers []string // empty = all
	Models    []string // canonical or upstream ids; empty = all
	Cases     []string // case ids; empty = all
	Excluded  func(provider string) bool
	SkipLocal bool // leave out local providers (Ollama), e.g. on a cloud CI runner
	// Spacing between requests to one model. Default: from the seed RPM.
	Spacing func(m catalogue.Model) time.Duration
	// Timeout per check. Default 60s; a model that times out once has its
	// remaining checks skipped, so one slow model cannot stall a run.
	Timeout time.Duration
	// Progress receives one line per finished model. May be nil.
	Progress io.Writer
}

// ModelReport is the result for one model on one provider.
type ModelReport struct {
	Canonical string                   `json:"canonical"`
	Upstream  string                   `json:"upstream"`
	Listed    bool                     `json:"listed_by_provider"`
	Note      string                   `json:"note,omitempty"`
	Results   []conformance.CaseResult `json:"results"`
}

// Counts returns pass, warn, fail, skip counts.
func (m ModelReport) Counts() (p, w, f, s int) {
	for _, r := range m.Results {
		switch r.Status {
		case conformance.Pass:
			p++
		case conformance.Warn:
			w++
		case conformance.Fail:
			f++
		default:
			s++
		}
	}
	return
}

// Provider statuses.
const (
	StatusOK          = "ok"
	StatusNoKey       = "no_key"
	StatusKeyRejected = "key_rejected"
	StatusUnreachable = "unreachable"
	StatusNotRunning  = "not_running" // local Ollama
	StatusExcluded    = "excluded"
)

// ProviderReport is the result for one provider.
type ProviderReport struct {
	ID               string                  `json:"id"`
	Name             string                  `json:"name"`
	Status           string                  `json:"status"`
	Error            string                  `json:"error,omitempty"`
	ListedModels     int                     `json:"listed_models"`
	ListedIDs        []string                `json:"listed_model_ids,omitempty"`
	MissingUpstream  []string                `json:"catalogue_models_not_listed,omitempty"`
	NewFreeModels    []string                `json:"new_free_models,omitempty"`
	Models           []ModelReport           `json:"models,omitempty"`
	BadModel         *conformance.CaseResult `json:"bad_model,omitempty"`
	RateHeaders      map[string]string       `json:"rate_headers_seen,omitempty"`
	RateHeaderIssues []string                `json:"rate_header_issues,omitempty"`
}

// Report is a full doctor run.
type Report struct {
	Version   string           `json:"mangoman_version"`
	Catalogue string           `json:"catalogue"`
	Started   time.Time        `json:"started"`
	Duration  string           `json:"duration"`
	Requests  int              `json:"requests"`
	Providers []ProviderReport `json:"providers"`
}

// Doctor holds what a run needs.
type Doctor struct {
	Cat     *catalogue.Catalogue
	Keys    *keys.Resolver
	Client  *providers.Client
	Version string
}

func in(list []string, v string) bool {
	if len(list) == 0 {
		return true
	}
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

// DefaultSpacing keeps a run under each model's per-minute request limit.
func DefaultSpacing(m catalogue.Model) time.Duration {
	if m.Limits.RPM > 0 {
		d := time.Minute / time.Duration(m.Limits.RPM)
		return d + d/10
	}
	return time.Second
}

// Plan returns how many requests a run would make per provider, so the CLI
// can warn before spending a small daily quota.
func (d *Doctor) Plan(o Options) map[string]int {
	cases := d.cases(o)
	out := map[string]int{}
	for _, m := range d.Cat.AllModels() {
		if !in(o.Providers, m.Provider) || !(in(o.Models, m.Canonical) || in(o.Models, m.Upstream)) {
			continue
		}
		for _, c := range cases {
			if !c.DirectOnly && (c.Needs == "" || m.Has(c.Needs)) {
				out[m.Provider]++
			}
		}
	}
	for id := range out {
		out[id]++ // the bad_model check, once per provider
		if p, ok := d.Cat.Provider(id); ok && p.AccountLimits.RPD > 0 && out[id] > p.AccountLimits.RPD/2 {
			out[id] = p.AccountLimits.RPD / 2
		}
	}
	return out
}

func (d *Doctor) cases(o Options) []conformance.Case {
	var out []conformance.Case
	for _, c := range conformance.Cases() {
		if in(o.Cases, c.ID) {
			out = append(out, c)
		}
	}
	return out
}

// Run checks every selected provider, providers in parallel. When ctx is
// cancelled it returns what it has so far.
func (d *Doctor) Run(ctx context.Context, o Options) Report {
	start := time.Now()
	rep := Report{Version: d.Version, Catalogue: d.Cat.Version, Started: start}
	if o.Spacing == nil {
		o.Spacing = DefaultSpacing
	}
	if o.Timeout <= 0 {
		o.Timeout = 60 * time.Second
	}
	if o.Progress != nil {
		o.Progress = &lockedWriter{w: o.Progress}
	}
	cases := d.cases(o)
	var (
		wg       sync.WaitGroup
		requests atomic.Int64
	)
	for _, p := range d.Cat.AllProviders() {
		if !in(o.Providers, p.ID) || (o.SkipLocal && p.Local && len(o.Providers) == 0) {
			continue
		}
		pr := ProviderReport{ID: p.ID, Name: p.Name}
		if o.Excluded != nil && o.Excluded(p.ID) {
			pr.Status = StatusExcluded
		}
		rep.Providers = append(rep.Providers, pr)
	}
	for i := range rep.Providers {
		pr := &rep.Providers[i]
		if pr.Status == StatusExcluded {
			continue
		}
		p, _ := d.Cat.Provider(pr.ID)
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.checkProvider(ctx, p, cases, o, pr, &requests)
		}()
	}
	wg.Wait()
	rep.Requests = int(requests.Load())
	rep.Duration = time.Since(start).Round(time.Second).String()
	return rep
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(b)
}

func (d *Doctor) checkProvider(ctx context.Context, p catalogue.Provider, cases []conformance.Case, o Options, pr *ProviderReport, requests *atomic.Int64) {
	key := ""
	if p.NeedsKey {
		k, _ := d.Keys.Get(p.ID)
		if k == "" {
			pr.Status = StatusNoKey
			return
		}
		key = k
	}

	lctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	ids, err := d.Client.ListModels(lctx, p, key)
	cancel()
	switch {
	case err != nil && p.Local:
		pr.Status, pr.Error = StatusNotRunning, "Ollama is not running on this machine"
		return
	case errors.Is(err, providers.ErrKeyRejected):
		pr.Status, pr.Error = StatusKeyRejected, err.Error()
		return
	case err != nil:
		pr.Status, pr.Error = StatusUnreachable, err.Error()
		return
	}
	pr.Status = StatusOK
	pr.ListedModels = len(ids)
	pr.ListedIDs = append([]string(nil), ids...)
	sort.Strings(pr.ListedIDs)
	listed := map[string]bool{}
	for _, id := range ids {
		listed[id] = true
	}

	var models []catalogue.Model
	known := map[string]bool{}
	for _, m := range d.Cat.AllModels() {
		if m.Provider != p.ID {
			continue
		}
		known[m.Upstream] = true
		if !listed[m.Upstream] {
			pr.MissingUpstream = append(pr.MissingUpstream, m.Upstream)
		}
		if in(o.Models, m.Canonical) || in(o.Models, m.Upstream) {
			models = append(models, m)
		}
	}
	if p.Local && len(models) == 0 {
		// Ollama models are discovered, not seeded: test what is installed.
		for _, id := range ids {
			if in(o.Models, id) {
				models = append(models, catalogue.Model{Canonical: id, Provider: p.ID, Upstream: id, Free: true, Caps: []string{"tools", "json", "streaming"}})
			}
		}
	}
	if !p.Local {
		for _, id := range ids {
			if !known[id] && looksFree(p.ID, id) {
				pr.NewFreeModels = append(pr.NewFreeModels, id)
			}
		}
		sort.Strings(pr.NewFreeModels)
	}

	var lastCtx context.Context // the most recent check's context, to spot timeouts
	send := func(ctx context.Context, body []byte, stream bool) (*http.Response, error) {
		req, err := core.ParseChat(body)
		if err != nil {
			return nil, err
		}
		q := p.Quirks
		up, _, err := req.BodyWith(req.Model, core.Upstream{Drop: q.DropParams, MaxTokensField: q.MaxTokensField, StreamUsage: q.StreamUsage})
		if err != nil {
			return nil, err
		}
		cctx, cancel := context.WithTimeout(ctx, o.Timeout)
		lastCtx = cctx
		resp, err := d.Client.Chat(cctx, p, key, up, stream)
		if err != nil {
			cancel()
			return nil, err
		}
		resp.Body = cancelOnClose{resp.Body, cancel}
		return resp, nil
	}

	// Stay within half of an account-wide daily cap (OpenRouter free), so a
	// check never uses up the user's free requests for the day.
	budget, used := 0, 0
	if p.AccountLimits.RPD > 0 {
		budget = p.AccountLimits.RPD / 2
	}
	spend := func() bool {
		if budget > 0 && used >= budget {
			return false
		}
		used++
		return true
	}

	seen := map[string]string{}
	wait := func(m catalogue.Model) {
		select {
		case <-ctx.Done():
		case <-time.After(o.Spacing(m)):
		}
	}
	for _, m := range models {
		mr := ModelReport{Canonical: m.Canonical, Upstream: m.Upstream, Listed: listed[m.Upstream]}
		run := cases
		if !mr.Listed {
			mr.Note = "not in the provider's model list; ran basic only to confirm the id"
			run = nil
			for _, c := range cases {
				if c.ID == "basic" {
					run = append(run, c)
				}
			}
		}
		stopped := "" // why the rest of this model's checks are skipped
		for _, c := range run {
			if c.DirectOnly {
				continue
			}
			if c.Needs != "" && !m.Has(c.Needs) {
				mr.Results = append(mr.Results, conformance.CaseResult{Case: c.ID, Status: conformance.Skip, Detail: "catalogue does not declare " + c.Needs})
				continue
			}
			if ctx.Err() != nil {
				stopped = "run stopped before this check"
			}
			if stopped != "" {
				mr.Results = append(mr.Results, conformance.CaseResult{Case: c.ID, Status: conformance.Skip, Detail: stopped})
				continue
			}
			if !spend() {
				mr.Results = append(mr.Results, conformance.CaseResult{Case: c.ID, Status: conformance.Skip,
					Detail: fmt.Sprintf("skipped to keep within half of the %d requests/day free limit", p.AccountLimits.RPD)})
				continue
			}
			lastCtx = nil
			res := conformance.Run(ctx, send, c, m.Upstream)
			requests.Add(1)
			for k, v := range res.RateHeaders {
				seen[k] = v
			}
			switch {
			case res.HTTP == http.StatusTooManyRequests:
				stopped = "rate limited earlier in this run"
			case lastCtx != nil && errors.Is(lastCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil:
				res.Status = conformance.Fail
				res.Detail = fmt.Sprintf("no complete answer within %s", o.Timeout)
				stopped = "skipped: timed out earlier in this run"
			}
			mr.Results = append(mr.Results, res)
			wait(m)
		}
		pr.Models = append(pr.Models, mr)
		if o.Progress != nil {
			fmt.Fprintln(o.Progress, ProgressLine(p.ID, mr))
		}
	}

	for _, c := range cases {
		if c.DirectOnly && ctx.Err() == nil && spend() {
			res := conformance.Run(ctx, send, c, "")
			requests.Add(1)
			pr.BadModel = &res
		}
	}

	if len(seen) > 0 {
		pr.RateHeaders = seen
	}
	pr.RateHeaderIssues = rateHeaderIssues(p, seen)
}

// ProgressLine is a one-line summary of a model's results.
func ProgressLine(provider string, m ModelReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-11s %-26s", provider, m.Canonical)
	for _, r := range m.Results {
		fmt.Fprintf(&b, " %s:%s", r.Case, r.Status.Short())
	}
	if m.Note != "" {
		b.WriteString("  (" + m.Note + ")")
	}
	return b.String()
}

func looksFree(provider, id string) bool {
	if provider == "openrouter" {
		return strings.HasSuffix(id, ":free")
	}
	return false
}

func rateHeaderIssues(p catalogue.Provider, seen map[string]string) []string {
	if len(seen) == 0 {
		return nil
	}
	var names []string
	for k := range seen {
		names = append(names, k)
	}
	sort.Strings(names)
	if len(p.RateHeaders) == 0 {
		return []string{"catalogue has no rate_headers rules for this provider; headers seen: " + strings.Join(names, ", ")}
	}
	var out []string
	for _, r := range p.RateHeaders {
		if _, ok := seen[strings.ToLower(r.Remaining)]; !ok {
			out = append(out, fmt.Sprintf("expected %s (%s per %s) but it was not sent; headers seen: %s", r.Remaining, r.Kind, r.Window, strings.Join(names, ", ")))
		}
	}
	return out
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}
