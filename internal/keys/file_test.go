package keys

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFileStoreRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "keys.enc")
	pass := func() (string, error) { return "correct horse battery", nil }
	s := NewFileStore(p, pass)
	if _, err := s.Get("groq"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
	if err := s.Set("groq", "gsk_secret_value"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if strings.Contains(string(raw), "gsk_secret_value") {
		t.Fatal("key stored in plain text")
	}
	// Unix only: Windows has no owner-only mode bits (ACLs protect the
	// user's profile directory instead).
	if fi, _ := os.Stat(p); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm %v", fi.Mode().Perm())
	}
	if v, err := s.Get("groq"); err != nil || v != "gsk_secret_value" {
		t.Fatalf("got %q %v", v, err)
	}
	wrong := NewFileStore(p, func() (string, error) { return "wrong passphrase", nil })
	if _, err := wrong.Get("groq"); err == nil {
		t.Fatal("wrong passphrase accepted")
	}
	if err := s.Delete("groq"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("groq"); !errors.Is(err, ErrNotFound) {
		t.Fatal("delete failed")
	}
}

func TestShortPassphraseRejected(t *testing.T) {
	s := NewFileStore(filepath.Join(t.TempDir(), "k"), func() (string, error) { return "short", nil })
	if err := s.Set("a", "b"); err == nil {
		t.Fatal("short passphrase accepted")
	}
}

type mem map[string]string

func (m mem) Get(p string) (string, error) {
	if v, ok := m[p]; ok {
		return v, nil
	}
	return "", ErrNotFound
}
func (m mem) Set(p, k string) error { m[p] = k; return nil }
func (m mem) Delete(p string) error { delete(m, p); return nil }
func (mem) Name() string            { return "mem" }

func TestResolverEnvWinsAndDisable(t *testing.T) {
	t.Setenv("TEST_GROQ_KEY", "from-env")
	r := NewResolver(mem{"groq": "from-store", "nvidia": "nv"}, map[string]string{"groq": "TEST_GROQ_KEY"})
	if v, src := r.Get("groq"); v != "from-env" || src != FromEnv {
		t.Fatalf("got %q %q", v, src)
	}
	if v, src := r.Get("nvidia"); v != "nv" || src != FromStore {
		t.Fatalf("got %q %q", v, src)
	}
	r.Disable("nvidia")
	if v, _ := r.Get("nvidia"); v != "" {
		t.Fatal("disabled key still returned")
	}
}

func TestPassphraseAskedOnce(t *testing.T) {
	asked := 0
	s := NewFileStore(filepath.Join(t.TempDir(), "k"), func() (string, error) { asked++; return "correct horse battery", nil })
	_ = s.Set("a", "1")
	_ = s.Set("b", "2")
	if v, _ := s.Get("a"); v != "1" || asked != 1 {
		t.Fatalf("asked %d times, got %q", asked, v)
	}
}

func TestMissingKeyAndWrongPassphraseAskOnce(t *testing.T) {
	p := filepath.Join(t.TempDir(), "k")
	if err := NewFileStore(p, func() (string, error) { return "correct horse", nil }).Set("groq", "k1"); err != nil {
		t.Fatal(err)
	}
	asks := 0
	r := NewResolver(NewFileStore(p, func() (string, error) { asks++; return "correct horse", nil }), nil)
	for i := 0; i < 5; i++ {
		if v, src := r.Get("cerebras"); v != "" || src != NoKey {
			t.Fatalf("got %q %q", v, src)
		}
	}
	if v, _ := r.Get("groq"); v != "k1" || asks != 1 {
		t.Fatalf("got %q, asked %d times", v, asks)
	}
	asks = 0
	bad := NewResolver(NewFileStore(p, func() (string, error) { asks++; return "wrong passphrase", nil }), nil)
	bad.Get("groq")
	bad.Get("cerebras")
	if asks != 1 {
		t.Fatalf("wrong passphrase asked %d times", asks)
	}
}

// Two stores on one file (the router and the command line): a key one adds
// must be seen by the other, and the other's next save must not drop it.
func TestFileStoreSeesOtherWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.enc")
	pass := func() (string, error) { return "a long passphrase", nil }
	router, cli := NewFileStore(path, pass), NewFileStore(path, pass)
	if err := router.Set("groq", "g1"); err != nil {
		t.Fatal(err)
	}
	if err := cli.Set("nvidia#asha", "n1"); err != nil {
		t.Fatal(err)
	}
	if v, err := router.Get("nvidia#asha"); err != nil || v != "n1" {
		t.Fatalf("router did not see the new key: %q %v", v, err)
	}
	if err := router.Set("zen", "z1"); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"groq", "nvidia#asha", "zen"} {
		if _, err := cli.Get(k); err != nil {
			t.Fatalf("%s lost after the other store saved: %v", k, err)
		}
	}
}
