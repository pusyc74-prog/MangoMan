// Package keys stores provider API keys on the user's machine only: the OS
// keychain first, an encrypted file as fallback. Keys are never sent anywhere
// except to the provider they belong to.
package keys

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"
)

// ErrNotFound means no key is stored for a provider.
var ErrNotFound = errors.New("key not found")

// Store is a place keys live.
type Store interface {
	Get(provider string) (string, error)
	Set(provider, key string) error
	Delete(provider string) error
	Name() string
}

const service = "mangoman"

// Keychain uses the OS keychain (macOS Keychain, Windows Credential Manager,
// Linux Secret Service).
type Keychain struct{}

func (Keychain) Name() string { return "os-keychain" }

func (Keychain) Get(p string) (string, error) {
	v, err := keyring.Get(service, p)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return v, err
}

func (Keychain) Set(p, k string) error { return keyring.Set(service, p, k) }

func (Keychain) Delete(p string) error {
	err := keyring.Delete(service, p)
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// KeychainAvailable probes the OS keychain.
func KeychainAvailable() bool {
	_, err := keyring.Get(service, "__probe__")
	return err == nil || errors.Is(err, keyring.ErrNotFound)
}

// Open picks the keychain when it works, else the encrypted file store.
// passphrase is only called when the file store is needed.
func Open(filePath string, passphrase func() (string, error)) (Store, error) {
	if os.Getenv("MANGOMAN_KEYSTORE") != "file" && KeychainAvailable() {
		return Keychain{}, nil
	}
	return NewFileStore(filePath, passphrase), nil
}

// Where says in plain words where a store keeps keys.
func Where(s Store) string {
	if _, ok := s.(Keychain); ok {
		return "this computer's keychain"
	}
	return "an encrypted file on this computer"
}

// Own is the account name of the user's own key for a provider. A team key
// carries the teammate's name instead.
const Own = "default"

// Name is the store entry for a key: the provider id for the user's own key,
// provider#name for a team key.
func Name(provider, account string) string {
	if account == "" || account == Own {
		return provider
	}
	return provider + "#" + account
}

var teamNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,19}$`)

// TeamName checks and normalises a teammate's name for a team key: lower
// case letters, digits and dashes, up to 20 characters.
func TeamName(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if !teamNameRE.MatchString(s) || s == Own {
		return "", errors.New("a team key needs a short name: letters, digits and dashes, up to 20 characters (for example ravi)")
	}
	return s, nil
}

// Source says where a resolved key came from.
type Source string

const (
	FromEnv   Source = "env"
	FromStore Source = "store"
	NoKey     Source = ""
)

// Resolver finds a key for a provider: an environment variable override
// first, then the store. Results, including "no key", are cached in process
// memory until Forget.
type Resolver struct {
	store    Store
	env      map[string]string // provider -> env var name
	mu       sync.Mutex
	cache    map[string]string
	rejected map[string]bool
}

// NewResolver builds a resolver. env maps provider id to its env var name.
func NewResolver(s Store, env map[string]string) *Resolver {
	return &Resolver{store: s, env: env, cache: map[string]string{}, rejected: map[string]bool{}}
}

// Get returns the key for a provider and where it came from.
func (r *Resolver) Get(provider string) (string, Source) {
	if name := r.env[provider]; name != "" {
		if v := os.Getenv(name); v != "" {
			return v, FromEnv
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.cache[provider]
	if !ok && r.store != nil {
		if k, err := r.store.Get(provider); err == nil {
			v = k
		}
		r.cache[provider] = v
	}
	if v == "" {
		return "", NoKey
	}
	return v, FromStore
}

// Disable drops a cached key, for example after a 401.
func (r *Resolver) Disable(provider string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[provider] = ""
	r.rejected[provider] = true
}

// Rejected reports whether the provider refused this key since it was added.
func (r *Resolver) Rejected(provider string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rejected[provider]
}

// Forget drops a cached key so the next Get reads the store again (after a
// key is added or removed).
func (r *Resolver) Forget(provider string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cache, provider)
	delete(r.rejected, provider)
}

// Store returns the underlying key store.
func (r *Resolver) Store() Store { return r.store }

// EnvName returns the environment variable that overrides a provider's key.
func (r *Resolver) EnvName(provider string) string { return r.env[provider] }
