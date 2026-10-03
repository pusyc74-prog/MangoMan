package keys

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
)

// FileStore is the fallback when no OS keychain is available: one file,
// AES-256-GCM, key derived from a user passphrase with PBKDF2-SHA256.
// Standard library only, to keep the dependency surface small.
type FileStore struct {
	path       string
	passphrase func() (string, error)
	mu         sync.Mutex
	pass       string            // remembered after the first successful use
	keys       map[string]string // decrypted once, then kept in memory
	err        error             // a failed unlock, returned without asking again
}

const pbkdf2Iter = 600_000

type envelope struct {
	V     int    `json:"v"`
	Salt  []byte `json:"salt"`
	Nonce []byte `json:"nonce"`
	Data  []byte `json:"data"`
}

// NewFileStore returns a file store at path.
func NewFileStore(path string, passphrase func() (string, error)) *FileStore {
	return &FileStore{path: path, passphrase: passphrase}
}

func (f *FileStore) Name() string { return "encrypted-file" }

func (f *FileStore) derive(salt []byte) (cipher.AEAD, error) {
	if f.passphrase == nil {
		return nil, errors.New("no passphrase source")
	}
	pass := f.pass
	if pass == "" {
		p, err := f.passphrase()
		if err != nil {
			f.err = err
			return nil, err
		}
		pass = p
	}
	if len(pass) < 8 {
		return nil, errors.New("passphrase must be at least 8 characters")
	}
	f.pass = pass // cleared again if it fails to decrypt
	k, err := pbkdf2.Key(sha256.New, pass, salt, pbkdf2Iter, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// load returns the decrypted keys. The file is decrypted once; a wrong
// passphrase fails every later call at once instead of asking again.
func (f *FileStore) load() (map[string]string, error) {
	if f.keys != nil || f.err != nil {
		return f.keys, f.err
	}
	data, err := os.ReadFile(f.path)
	if os.IsNotExist(err) {
		f.keys = map[string]string{}
		return f.keys, nil
	}
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("key file corrupt: %w", err)
	}
	aead, err := f.derive(env.Salt)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, env.Nonce, env.Data, nil)
	if err != nil {
		f.pass = ""
		f.err = errors.New("wrong passphrase or tampered key file")
		return nil, f.err
	}
	m := map[string]string{}
	err = json.Unmarshal(plain, &m)
	clear(plain)
	if err != nil {
		return nil, err
	}
	f.keys = m
	return m, nil
}

func (f *FileStore) save(m map[string]string) error {
	salt := make([]byte, 16)
	nonce := make([]byte, 12)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	aead, err := f.derive(salt)
	if err != nil {
		return err
	}
	plain, err := json.Marshal(m)
	if err != nil {
		return err
	}
	env := envelope{V: 1, Salt: salt, Nonce: nonce, Data: aead.Seal(nil, nonce, plain, nil)}
	clear(plain)
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}

func (f *FileStore) Get(p string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return "", err
	}
	v, ok := m[p]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (f *FileStore) Set(p, k string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	old, had := m[p]
	m[p] = k
	if err := f.save(m); err != nil {
		if had {
			m[p] = old
		} else {
			delete(m, p)
		}
		return err
	}
	return nil
}

func (f *FileStore) Delete(p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	if _, ok := m[p]; !ok {
		return ErrNotFound
	}
	old := m[p]
	delete(m, p)
	if err := f.save(m); err != nil {
		m[p] = old
		return err
	}
	return nil
}
