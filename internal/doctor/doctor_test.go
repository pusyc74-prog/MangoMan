package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/catalogue"
	"github.com/pusyc74-prog/mangoman/internal/conformance"
	"github.com/pusyc74-prog/mangoman/internal/keys"
	"github.com/pusyc74-prog/mangoman/internal/providers"
)

type mem map[string]string

func (m mem) Get(p string) (string, error) {
	if v, ok := m[p]; ok {
		return v, nil
	}
	return "", keys.ErrNotFound
}
func (m mem) Set(p, k string) error { m[p] = k; return nil }
func (m mem) Delete(p string) error { delete(m, p); return nil }
func (mem) Name() string            { return "mem" }

func TestDoctor(t *testing.T) {
	good := httptest.NewTLSServer(conformance.Reference(false, []string{"good-model", "brand-new:free"}))
	defer good.Close()
	rejecting := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"bad key"}`, 401)
	}))
	defer rejecting.Close()

	cat := catalogue.Catalogue{Version: "t", Providers: []catalogue.Provider{
		// "openrouter" id so :free models are reported as new.
		{ID: "openrouter", Name: "Good", BaseURL: good.URL, Kind: "openai", NeedsKey: true, Speed: 0.5,
			RateHeaders: []catalogue.RateHeader{{Kind: "requests", Window: "day", Remaining: "x-ratelimit-remaining-requests-day"}}},
		{ID: "bad", Name: "Bad", BaseURL: rejecting.URL, Kind: "openai", NeedsKey: true},
		{ID: "nokey", Name: "NoKey", BaseURL: "https://nokey.invalid", Kind: "openai", NeedsKey: true},
	}, Models: []catalogue.Model{
		{Canonical: "good", Provider: "openrouter", Upstream: "good-model", Free: true, Caps: []string{"tools", "json", "streaming"}},
		{Canonical: "plain", Provider: "openrouter", Upstream: "renamed-upstream", Free: true, Caps: []string{"streaming"}},
		{Canonical: "x", Provider: "bad", Upstream: "x", Free: true},
	}}
	data, _ := json.Marshal(&cat)
	parsed, err := catalogue.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	client := providers.NewClient()
	client.HTTP = good.Client()
	d := &Doctor{Cat: parsed, Keys: keys.NewResolver(mem{"openrouter": "k", "bad": "k"}, nil), Client: client, Version: "t"}

	// good: 8 corpus cases; plain (no tools or json): basic, multiturn,
	// stream, truncation; plus one bad_model check.
	if plan := d.Plan(Options{}); plan["openrouter"] != 8+4+1 {
		t.Fatalf("plan %v", plan)
	}

	var progress bytes.Buffer
	rep := d.Run(context.Background(), Options{Spacing: func(catalogue.Model) time.Duration { return 0 }, Progress: &progress})
	by := map[string]ProviderReport{}
	for _, p := range rep.Providers {
		by[p.ID] = p
	}
	g := by["openrouter"]
	if g.Status != StatusOK || g.ListedModels != 2 || len(g.ListedIDs) != 2 || g.ListedIDs[0] != "brand-new:free" {
		t.Fatalf("good provider: %+v", g)
	}
	if len(g.MissingUpstream) != 1 || g.MissingUpstream[0] != "renamed-upstream" {
		t.Fatalf("missing upstream: %v", g.MissingUpstream)
	}
	if len(g.NewFreeModels) != 1 || g.NewFreeModels[0] != "brand-new:free" {
		t.Fatalf("new free models: %v", g.NewFreeModels)
	}
	for _, m := range g.Models {
		p, w, f, _ := m.Counts()
		switch m.Canonical {
		case "good":
			if f+w != 0 || p != 8 {
				t.Fatalf("good model results: %+v", m.Results)
			}
		case "plain":
			if m.Listed || len(m.Results) != 1 || m.Results[0].Case != "basic" {
				t.Fatalf("unlisted model should run basic only: %+v", m)
			}
		}
	}
	if g.BadModel == nil || g.BadModel.Status != conformance.Pass {
		t.Fatalf("bad model check: %+v", g.BadModel)
	}
	if len(g.RateHeaderIssues) != 1 || !strings.Contains(g.RateHeaderIssues[0], "x-ratelimit-remaining-requests-day") {
		t.Fatalf("rate header issues: %v", g.RateHeaderIssues)
	}
	if by["bad"].Status != StatusKeyRejected || by["nokey"].Status != StatusNoKey {
		t.Fatalf("statuses: bad=%s nokey=%s", by["bad"].Status, by["nokey"].Status)
	}
	if !strings.Contains(progress.String(), "openrouter  good") {
		t.Fatalf("progress: %s", progress.String())
	}
	// The report must never contain keys.
	js, _ := json.Marshal(rep)
	if strings.Contains(string(js), `"k"`) {
		t.Fatal("report contains a key")
	}
	md := Markdown(rep)
	for _, want := range []string{"| openrouter | ok | 2 | 1 | 1 |", "| openrouter | good | pass | pass |", "renamed-upstream", "**radar**"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q:\n%s", want, md)
		}
	}
}
