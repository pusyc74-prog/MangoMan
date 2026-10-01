package radar

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/catalogue"
	"github.com/pusyc74-prog/mangoman/internal/keys"
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

func testCat(t *testing.T) *catalogue.Catalogue {
	t.Helper()
	c := catalogue.Catalogue{Version: "t", Providers: []catalogue.Provider{
		{ID: "or", Name: "OR", BaseURL: "https://or.example", NeedsKey: true,
			Discover: catalogue.Discover{Mode: "suffix", Suffix: ":free", Exclude: []string{"guard"}}},
		{ID: "nv", Name: "NV", BaseURL: "https://nv.example", NeedsKey: true,
			Discover: catalogue.Discover{Mode: "all", Exclude: []string{"embed"}}},
		{ID: "nokey", Name: "NoKey", BaseURL: "https://x.example", NeedsKey: true, Discover: catalogue.Discover{Mode: "all"}},
	}, Models: []catalogue.Model{
		{Canonical: "known", Provider: "or", Upstream: "acme/known:free", Free: true, Limits: catalogue.Limits{RPM: 20}},
	}}
	data, _ := json.Marshal(&c)
	parsed, err := catalogue.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestScanOfferAndPersist(t *testing.T) {
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	listing := map[string][]string{
		"or": {"acme/known:free", "acme/old:free", "acme/paid", "acme/guard-1:free"},
		"nv": {"nv/chat-1", "nv/embed-1"},
	}
	path := filepath.Join(t.TempDir(), "radar.json")
	cat := testCat(t)
	r := &Radar{Cat: cat, Keys: keys.NewResolver(mem{"or": "k", "nv": "k"}, nil), Path: path,
		Now: func() time.Time { return now },
		List: func(_ context.Context, p catalogue.Provider, _ string) ([]string, error) {
			if p.ID == "nokey" {
				t.Fatal("providers without a key must not be listed")
			}
			return listing[p.ID], nil
		}}
	_ = r.Load()
	if n, err := r.Scan(context.Background()); n != 0 || err != nil {
		t.Fatalf("first scan is the baseline: %d %v", n, err)
	}
	items, _ := r.Offer()
	if len(items) != 2 || items[0].New || items[1].New {
		t.Fatalf("baseline items %+v", items) // acme/old:free and nv/chat-1, neither marked new
	}

	// A day later a new free model appears on OpenRouter.
	now = now.Add(24 * time.Hour)
	listing["or"] = append(listing["or"], "acme/fresh-2:free")
	if n, _ := r.Scan(context.Background()); n != 1 {
		t.Fatalf("want 1 new model, got %d", n)
	}
	items, _ = r.Offer()
	if len(items) != 3 || items[0].Upstream != "acme/fresh-2:free" || !items[0].New || items[0].Name != "fresh-2" {
		t.Fatalf("new model should lead: %+v", items)
	}

	// Saved and restored.
	r2 := &Radar{Cat: testCat(t), Keys: r.Keys, Path: path, Now: r.Now, List: r.List}
	if err := r2.Load(); err != nil {
		t.Fatal(err)
	}
	if got, _ := r2.Offer(); len(got) != 3 || !got[0].New {
		t.Fatalf("restored %+v", got)
	}

	// Once added to the catalogue it is no longer offered.
	cat.AddModel(cat.DiscoveredModel("or", "acme/fresh-2:free"))
	if got, _ := r.Offer(); len(got) != 2 {
		t.Fatalf("added model still offered: %+v", got)
	}
	m := cat.DiscoveredModel("or", "acme/fresh-2:free")
	if m.Limits.RPM != 20 || !m.Free || m.Canonical != "fresh-2" {
		t.Fatalf("discovered model defaults %+v", m)
	}

	// Removed by the provider: dropped after it stops appearing.
	now = now.Add(24 * time.Hour)
	listing["nv"] = nil
	_, _ = r.Scan(context.Background())
	for _, it := range func() []Item { i, _ := r.Offer(); return i }() {
		if it.Upstream == "nv/chat-1" {
			t.Fatal("model the provider no longer lists is still offered")
		}
	}
}
