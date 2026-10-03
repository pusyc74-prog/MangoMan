package catalogue

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
)

func TestSeedValid(t *testing.T) {
	c, err := Seed()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.AllProviders()) < 5 || len(c.AllModels()) < 5 {
		t.Fatal("seed too small")
	}
	for _, m := range c.AllModels() {
		if !m.Free {
			t.Errorf("%s is not free", m.ID())
		}
	}
	p, ok := c.Provider("ollama")
	if !ok || !p.Local || p.NeedsKey {
		t.Fatal("ollama should be local and keyless")
	}
}

func TestValidationRejectsHTTP(t *testing.T) {
	_, err := Parse([]byte(`{"providers":[{"id":"x","base_url":"http://evil"}],"models":[]}`))
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("want https error, got %v", err)
	}
	_, err = Parse([]byte(`{"providers":[],"models":[{"canonical":"m","provider":"nope","upstream":"m"}]}`))
	if err == nil {
		t.Fatal("unknown provider should fail")
	}
}

func TestSignedFeed(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sig := ed25519.Sign(priv, seedJSON)
	if _, err := VerifyAndParse(seedJSON, sig, pub); err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), seedJSON...)
	tampered[10] ^= 1
	if _, err := VerifyAndParse(tampered, sig, pub); err == nil {
		t.Fatal("tampered feed accepted")
	}
}

func TestLabelASCII(t *testing.T) {
	l := DataPolicy{Retention: "none", TrainsOnData: "no", Jurisdiction: "US"}.Label()
	for _, r := range l {
		if r > 127 {
			t.Fatalf("label %q is not ASCII", l)
		}
	}
	if !strings.Contains(l, "unverified") {
		t.Fatal("unverified policies must say so")
	}
}

func TestReplaceProviderModels(t *testing.T) {
	c, _ := Seed()
	before := len(c.AllModels())
	c.ReplaceProviderModels("ollama", []Model{{Canonical: "qwen3:8b", Provider: "ollama", Upstream: "qwen3:8b", Free: true}})
	if len(c.AllModels()) != before+1 {
		t.Fatal("ollama model not added")
	}
	c.ReplaceProviderModels("ollama", nil)
	if len(c.AllModels()) != before {
		t.Fatal("ollama models not removed")
	}
}

func TestDiscoveredModelKeepsNamesApart(t *testing.T) {
	c := &Catalogue{Models: []Model{{Canonical: "llama-3.3-70b", Provider: "nv", Upstream: "meta/llama-3.3-70b"}}}
	if m := c.DiscoveredModel("nv", "acme/llama-3.3-70b"); m.Canonical != "acme/llama-3.3-70b" {
		t.Fatalf("name %q clashes with the catalogue model", m.Canonical)
	}
	if m := c.DiscoveredModel("nv", "acme/fresh:free"); m.Canonical != "fresh" {
		t.Fatalf("name %q", m.Canonical)
	}
}
