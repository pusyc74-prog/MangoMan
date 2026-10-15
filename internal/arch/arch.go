// Package arch is Mango Arch, the shared learning between MangoMans.
//
// Every MangoMan whose owner agreed sends, once an hour, how each free model
// behaved for it: counts only (answered, busy, slow, garbled, empty,
// failed), never prompts, answers, keys or files, under a random id that
// says nothing about the person and changes every day. The server adds the
// counts up. When many independent MangoMans, on different networks, see the same model
// fail in the same hour, the server signs a lesson ("try nvidia/glm-5.3-flash last
// until 15:00") and every MangoMan picks it up within the hour, or as soon
// as it is next online.
//
// Safety by design: a lesson must carry the server's Ed25519 signature, may
// only change the order models are tried in (never take one away), and
// ends within three hours. A wrong or forged lesson costs a little speed,
// never a task.
package arch

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// URL and Key are set when MangoMan is built for release:
// -X github.com/pusyc74-prog/mangoman/internal/arch.URL=https://...
// -X github.com/pusyc74-prog/mangoman/internal/arch.Key=<base64 public key>.
// Empty means Mango Arch is not live yet, and MangoMan sends nothing.
var (
	URL string
	Key string
)

// MaxLife is the longest a lesson can last after the server issued it.
const MaxLife = 3 * time.Hour

// Lesson is one thing learned. Kind "demote": try Target behind the other
// models until Until. Other kinds are skipped, so older MangoMans keep
// working when new kinds are added.
type Lesson struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Target   string    `json:"target"` // provider/model
	Until    time.Time `json:"until"`
	Reason   string    `json:"reason,omitempty"`   // busy, slow, garbled, empty or failed
	Installs int       `json:"installs,omitempty"` // networks where MangoMans saw it
}

// Bundle is what the server signs: every lesson in force.
type Bundle struct {
	Issued  time.Time `json:"issued"`
	Lessons []Lesson  `json:"lessons"`
}

// Signed is a bundle as the server sends it: the exact bytes that were
// signed, and the signature, both base64.
type Signed struct {
	Data string `json:"data"`
	Sig  string `json:"sig"`
}

// Verify checks the signature and reads the bundle.
func Verify(s Signed, pub ed25519.PublicKey) (Bundle, error) {
	var b Bundle
	data, err1 := base64.StdEncoding.DecodeString(s.Data)
	sig, err2 := base64.StdEncoding.DecodeString(s.Sig)
	if err1 != nil || err2 != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, data, sig) {
		return b, errors.New("lessons are not signed by Mango Arch")
	}
	if err := json.Unmarshal(data, &b); err != nil {
		return b, fmt.Errorf("lessons: %w", err)
	}
	return b, nil
}

// Report is one hour of counts from one MangoMan.
type Report struct {
	Install string                    `json:"install"`
	Models  map[string]map[string]int `json:"models"`
}

// Client syncs with the Mango Arch server.
type Client struct {
	URL    string
	Key    ed25519.PublicKey
	Dir    string                               // MangoMan's folder: the install id and the last lessons
	Known  func(target string) bool             // a cloud model from the catalogue; others are never sent or changed
	Demote func(target string, until time.Time) // the router's line
	Tally  func() map[string]map[string]int     // counts since the last call
	Share  func() bool                          // the owner agreed to send counts
	HTTP   *http.Client
	Now    func() time.Time

	mu      sync.Mutex
	synced  time.Time
	current Bundle
}

// New returns a client for the built-in server, or nil when Mango Arch is
// not live in this build.
func New(dir string) *Client {
	pub, err := base64.StdEncoding.DecodeString(Key)
	if URL == "" || err != nil || len(pub) != ed25519.PublicKeySize {
		return nil
	}
	return &Client{URL: strings.TrimSuffix(URL, "/"), Key: pub, Dir: dir}
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Run applies the lessons saved last time, then syncs now and every hour
// until ctx ends. A failed sync is tried again at the next hour.
func (c *Client) Run(ctx context.Context, every time.Duration, logf func(string, ...any)) {
	if b, err := os.ReadFile(filepath.Join(c.Dir, "arch-lessons.json")); err == nil {
		var s Signed
		if json.Unmarshal(b, &s) == nil {
			if bundle, err := Verify(s, c.Key); err == nil {
				c.apply(bundle)
			}
		}
	}
	for {
		if err := c.Sync(ctx); err != nil && ctx.Err() == nil {
			logf("mango arch: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// Sync sends this hour's counts (when the owner agreed) and takes the
// lessons in force.
func (c *Client) Sync(ctx context.Context) error {
	sent := c.send(ctx)
	if err := c.fetch(ctx); err != nil {
		return err
	}
	return sent
}

// send sends this hour's counts when the owner agreed. A failed send does
// not stop lessons coming in.
func (c *Client) send(ctx context.Context) error {
	counts := c.Tally() // taken either way, so counts from before agreeing are never sent
	if c.Share() {
		models := map[string]map[string]int{}
		for t, m := range counts {
			if c.Known(t) {
				models[t] = m
			}
		}
		if len(models) > 0 {
			id, err := c.install()
			if err != nil {
				return err
			}
			body, _ := json.Marshal(Report{Install: id, Models: models})
			return c.call(ctx, http.MethodPost, "/v1/report", body, nil)
		}
	}
	return nil
}

// fetch takes the lessons in force.
func (c *Client) fetch(ctx context.Context) error {
	var s Signed
	if err := c.call(ctx, http.MethodGet, "/v1/lessons", nil, &s); err != nil {
		return err
	}
	b, err := Verify(s, c.Key)
	if err != nil {
		return err
	}
	c.mu.Lock()
	old := c.current.Issued
	c.mu.Unlock()
	if b.Issued.Before(old) {
		return errors.New("lessons older than the ones in force; kept those")
	}
	raw, _ := json.Marshal(s)
	if err := os.WriteFile(filepath.Join(c.Dir, "arch-lessons.json"), raw, 0o600); err != nil {
		return err
	}
	c.apply(b)
	return nil
}

// apply puts lessons in force: signed, in force now, about a known model,
// and never longer than MaxLife after they were issued.
func (c *Client) apply(b Bundle) {
	now := c.now()
	if b.Issued.After(now.Add(10 * time.Minute)) {
		return // from the future: a wrong clock on one side; wait for the next one
	}
	var kept []Lesson
	for _, l := range b.Lessons {
		until := l.Until
		if until.After(b.Issued.Add(MaxLife)) {
			until = b.Issued.Add(MaxLife)
		}
		if l.Kind != "demote" || !until.After(now) || !c.Known(l.Target) {
			continue
		}
		l.Until = until
		c.Demote(l.Target, until)
		kept = append(kept, l)
	}
	c.mu.Lock()
	c.current, c.synced = Bundle{Issued: b.Issued, Lessons: kept}, now
	c.mu.Unlock()
}

// Status is what the dashboard shows: when it last synced and the lessons
// in force.
func (c *Client) Status() (time.Time, []Lesson) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	var in []Lesson
	for _, l := range c.current.Lessons {
		if l.Until.After(now) {
			in = append(in, l)
		}
	}
	return c.synced, in
}

// install is this MangoMan's random id for today. A new one is made each
// day (UTC), so reports from different days cannot be linked. It is not
// derived from anything about the computer or the person.
func (c *Client) install() (string, error) {
	path := filepath.Join(c.Dir, "arch-id")
	day := c.now().UTC().Format("2006-01-02")
	if b, err := os.ReadFile(path); err == nil {
		if d, id, ok := strings.Cut(strings.TrimSpace(string(b)), " "); ok && d == day && len(id) == 32 {
			return id, nil
		}
	}
	r := make([]byte, 16)
	if _, err := rand.Read(r); err != nil {
		return "", err
	}
	id := hex.EncodeToString(r)
	return id, os.WriteFile(path, []byte(day+" "+id), 0o600)
}

func (c *Client) call(ctx context.Context, method, path string, body []byte, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s: HTTP %d", method, path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}
