package ingress

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/pusyc74-prog/mangoman/internal/catalogue"
	"github.com/pusyc74-prog/mangoman/internal/radar"
)

func TestFavoritesAPI(t *testing.T) {
	h, s := dashServer(t, mem{"groq": "k"})
	w := call(h, "PUT", "/mangoman/favorites", "127.0.0.1:4141", authz, `{"models":["groq/gpt-oss-120b","kimi-k3"]}`)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := s.Cfg.GetFavorites(); len(got) != 2 || got[0] != "groq/gpt-oss-120b" {
		t.Fatalf("favorites %v", got)
	}
	w = call(h, "PUT", "/mangoman/favorites", "127.0.0.1:4141", authz, `{"models":["no-such-model"]}`)
	if w.Code != 422 {
		t.Fatalf("unknown model accepted: %d", w.Code)
	}
	if len(s.Cfg.GetFavorites()) != 2 {
		t.Fatal("rejected update changed the list")
	}
	w = call(h, "GET", "/mangoman/overview", "127.0.0.1:4141", authz, "")
	var ov Overview
	_ = json.Unmarshal(w.Body.Bytes(), &ov)
	if len(ov.Favorites) != 2 || ov.Favorites[1] != "kimi-k3" {
		t.Fatalf("overview favorites %v", ov.Favorites)
	}
	if w := call(h, "PUT", "/mangoman/favorites", "127.0.0.1:4141", nil, `{"models":[]}`); w.Code != 401 {
		t.Fatalf("favorites must need the token: %d", w.Code)
	}
}

func TestRadarAddToMyList(t *testing.T) {
	_, s := dashServer(t, mem{"openrouter": "k"})
	listing := []string{"nvidia/nemotron-3-ultra-550b-a55b:free", "acme/fresh-2:free"}
	s.Radar = &radar.Radar{Cat: s.Router.Cat, Keys: s.Router.Keys,
		List: func(_ context.Context, p catalogue.Provider, _ string) ([]string, error) {
			if p.ID == "openrouter" {
				return listing, nil
			}
			return nil, nil
		}}
	_ = s.Radar.Load()
	h := s.Handler()

	w := call(h, "POST", "/mangoman/radar/scan", "127.0.0.1:4141", authz, "")
	var rv RadarView
	if err := json.Unmarshal(w.Body.Bytes(), &rv); err != nil || w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	found := false
	for _, it := range rv.Items {
		if it.Upstream == "acme/fresh-2:free" {
			found = true
		}
		if it.Upstream == "nvidia/nemotron-3-ultra-550b-a55b:free" {
			t.Fatal("catalogue model offered as new")
		}
	}
	if !found || rv.LastScan == nil {
		t.Fatalf("radar %+v", rv)
	}

	// A model the provider never listed cannot be smuggled in.
	if w := call(h, "POST", "/mangoman/radar/add", "127.0.0.1:4141", authz,
		`{"provider":"openrouter","upstream":"evil/model"}`); w.Code != 404 {
		t.Fatalf("unlisted model added: %d", w.Code)
	}

	w = call(h, "POST", "/mangoman/radar/add", "127.0.0.1:4141", authz, `{"provider":"openrouter","upstream":"acme/fresh-2:free"}`)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if !s.Router.Cat.HasUpstream("openrouter", "acme/fresh-2:free") {
		t.Fatal("model not added to the catalogue")
	}
	if f := s.Cfg.GetFavorites(); len(f) != 1 || f[0] != "openrouter/fresh-2" {
		t.Fatalf("favorites %v", f)
	}
	if cm := s.Cfg.GetCustomModels(); len(cm) != 1 || cm[0].Upstream != "acme/fresh-2:free" {
		t.Fatalf("custom models %v", cm)
	}
	// Adding twice is harmless.
	call(h, "POST", "/mangoman/radar/add", "127.0.0.1:4141", authz, `{"provider":"openrouter","upstream":"acme/fresh-2:free"}`)
	if len(s.Cfg.GetFavorites()) != 1 || len(s.Cfg.GetCustomModels()) != 1 {
		t.Fatal("second add duplicated entries")
	}
	w = call(h, "GET", "/mangoman/radar", "127.0.0.1:4141", authz, "")
	_ = json.Unmarshal(w.Body.Bytes(), &rv)
	for _, it := range rv.Items {
		if it.Upstream == "acme/fresh-2:free" {
			t.Fatal("added model still offered")
		}
	}
}
