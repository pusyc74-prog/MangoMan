// Package keys stores provider API keys on the user's machine only: the OS
// keychain first, an encrypted file as fallback. Keys are never sent anywhere
// except to the provider they belong to.
package keys

import (
	"errors"
	"os"
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

// Source says where a resolved key came from.
type Source string

const (
	FromEnv   Source = "env"
	FromStore Source = "store"
	NoKey     Source = ""
)

// Resolver finds a key for a provider: an environment variable override
// first, then the store. Resolved keys are cached in process memory.
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
	if v, ok := r.cache[provider]; ok {
		return v, FromStore
	}
	if r.store == nil {
		return "", NoKey
	}
	v, err := r.store.Get(provider)
	if err != nil || v == "" {
		return "", NoKey
	}
	r.cache[provider] = v
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
