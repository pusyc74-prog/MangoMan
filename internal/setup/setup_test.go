package setup

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

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

func wizard(t *testing.T, store mem, answers string, secrets []string) (*Wizard, *bytes.Buffer, *[]string) {
	t.Helper()
	cat, err := catalogue.Seed()
	if err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	opened := &[]string{}
	w := &Wizard{
		Cat: cat, Store: store, Resolver: keys.NewResolver(store, nil),
		Validate: func(_ context.Context, p catalogue.Provider, key string) error {
			if strings.HasPrefix(key, "bad") {
				return errors.New("rejected the key (HTTP 401)")
			}
			return nil
		},
		In:  bufio.NewReader(strings.NewReader(answers)),
		Out: out,
		ReadSecret: func(string) (string, error) {
			if len(secrets) == 0 {
				return "", nil
			}
			s := secrets[0]
			secrets = secrets[1:]
			return s, nil
		},
		OpenURL: func(u string) error { *opened = append(*opened, u); return nil },
		Ollama:  func(context.Context) (bool, int) { return true, 2 },
	}
	return w, out, opened
}

func TestWizardConnectsSkipsAndRetries(t *testing.T) {
	store := mem{"zen": "already"}
	// nvidia: Enter, bad key then good key. groq: skip. openrouter: Enter
	// then empty key (skip). zen: already connected. cerebras: quit.
	w, out, opened := wizard(t, store, "\ns\n\nq\n", []string{"bad-key", "nvapi-good", ""})
	res := w.Run(context.Background())

	if store["nvidia"] != "nvapi-good" {
		t.Fatalf("nvidia key not stored: %v", store)
	}
	if _, ok := store["groq"]; ok {
		t.Fatal("skipped provider got a key")
	}
	if len(res.New) != 1 || res.New[0] != "nvidia" || len(res.Connected) != 2 || res.Ollama != 2 {
		t.Fatalf("result %+v", res)
	}
	if len(*opened) != 2 || !strings.Contains((*opened)[0], "nvidia") {
		t.Fatalf("opened %v", *opened)
	}
	text := out.String()
	for _, want := range []string{"failed: rejected the key", "ok, connected", "Connected (store)", "2 of 5 cloud providers connected, plus 2 local Ollama models", "mangoman dashboard"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
}

func TestWizardOffersOllamaModel(t *testing.T) {
	w, out, _ := wizard(t, mem{}, "q\ny\n", nil)
	w.Ollama = func(context.Context) (bool, int) { return true, 0 }
	pulled := ""
	w.Pull = func(m string) error { pulled = m; return nil }
	// quit skips the providers but still reaches the Ollama step; the y
	// answer is not used there because quit also skips the download offer.
	res := w.Run(context.Background())
	if pulled != "" || res.Ollama != 0 {
		t.Fatalf("download should not be offered after quit")
	}
	w2, out2, _ := wizard(t, mem{}, "s\ns\ns\ns\ns\ny\n", nil)
	w2.Ollama = func(context.Context) (bool, int) { return true, 0 }
	w2.Pull = func(m string) error { pulled = m; return nil }
	res = w2.Run(context.Background())
	if pulled != SmallModel || res.Ollama != 1 || !strings.Contains(out2.String(), "Downloaded") {
		t.Fatalf("pulled %q, result %+v\n%s", pulled, res, out2.String())
	}
	_ = out
}

func TestConnectKeyErrors(t *testing.T) {
	cat, _ := catalogue.Seed()
	if _, err := ConnectKey(context.Background(), cat, mem{}, nil, "nope", "", "k"); err == nil {
		t.Fatal("unknown provider accepted")
	}
	if _, err := ConnectKey(context.Background(), cat, mem{}, nil, "ollama", "", "k"); err == nil {
		t.Fatal("keyless provider accepted a key")
	}
	if _, err := ConnectKey(context.Background(), cat, mem{}, nil, "groq", "", "  "); err == nil {
		t.Fatal("empty key accepted")
	}
	offline := func(context.Context, catalogue.Provider, string) error {
		return &url.Error{Op: "Get", URL: "https://integrate.api.nvidia.com/v1/models", Err: errors.New("Forbidden")}
	}
	if _, err := ConnectKey(context.Background(), cat, mem{}, offline, "nvidia", "", "k"); err == nil || !strings.Contains(err.Error(), "internet connection") {
		t.Fatalf("no network should say so plainly: %v", err)
	}
}
