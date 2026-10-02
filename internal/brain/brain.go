// Package brain is the decision brain: it asks one fast model short, typed
// questions (pick one, yes or no) and gets back an answer with a confidence.
// The router uses it where its own rules are unsure. Every call has a time
// budget; a slow, failed or unsure answer means "no decision" and the caller
// keeps its rule-based default, so the brain can never make routing worse
// than it was without it.
package brain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Caller sends one Chat Completions request body (through the router) and
// returns the HTTP status and body.
type Caller func(ctx context.Context, body []byte) (int, []byte, error)

// Decision is one recorded brain call, for the dashboard.
type Decision struct {
	Time       time.Time `json:"time"`
	Kind       string    `json:"kind"`   // "task" or "non_answer"
	Answer     string    `json:"answer"` // "" when no decision
	Confidence float64   `json:"confidence"`
	LatencyMS  int64     `json:"latency_ms"`
	Cached     bool      `json:"cached,omitempty"`
	Fallback   string    `json:"fallback,omitempty"` // why no decision: timeout, error, unsure, bad_answer
}

// Stats summarise brain activity since start.
type Stats struct {
	Model     string     `json:"model"`
	Enabled   bool       `json:"enabled"`
	Calls     int        `json:"calls"`
	Decided   int        `json:"decided"`
	Fallbacks int        `json:"fallbacks"`
	CacheHits int        `json:"cache_hits"`
	AvgMS     int64      `json:"avg_ms"`
	Recent    []Decision `json:"recent"`
}

// Brain makes typed decisions. Safe for concurrent use.
type Brain struct {
	Call    Caller
	Model   string        // e.g. "free/fast", "strict/jev-1.13-free", "ollama/qwen3:4b"
	Budget  time.Duration // per decision; default 1.5 s
	MinConf float64       // below this the answer is ignored; default 0.6

	off atomic.Bool

	mu      sync.Mutex
	cache   map[string]cached
	stats   Stats
	totalMS int64
	timed   int
}

type cached struct {
	answer string
	conf   float64
	until  time.Time
}

const (
	cacheTTL  = 15 * time.Minute
	cacheMax  = 1024
	recentMax = 12
)

func (b *Brain) budget() time.Duration {
	if b.Budget > 0 {
		return b.Budget
	}
	return 1500 * time.Millisecond
}

func (b *Brain) minConf() float64 {
	if b.MinConf > 0 {
		return b.MinConf
	}
	return 0.6
}

// Key builds a cache key from parts.
func Key(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}

const system = `You are a fast decision model inside an AI router. You answer one typed question.
Reply with only a JSON object and nothing else: {"answer": "<one of the allowed answers>", "confidence": <number from 0 to 1>}.
Confidence is how likely your answer is right. If unsure, say so with a low confidence.`

// Choose asks a pick-one question. ok is false when there is no confident
// decision; the caller then keeps its default. key, when not empty, caches
// the decision (for example per conversation).
func (b *Brain) Choose(ctx context.Context, kind, key, question string, options []string) (answer string, conf float64, ok bool) {
	if b == nil || b.Call == nil || len(options) == 0 || b.off.Load() {
		return "", 0, false
	}
	if key != "" {
		if a, c, hit := b.cached(key); hit {
			b.record(Decision{Time: time.Now(), Kind: kind, Answer: a, Confidence: c, Cached: true}, 0, true)
			return a, c, a != ""
		}
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, b.budget())
	defer cancel()
	ans, c, why := b.ask(ctx, question, options)
	d := Decision{Time: start, Kind: kind, Answer: ans, Confidence: c, LatencyMS: time.Since(start).Milliseconds(), Fallback: why}
	if why == "" && c < b.minConf() {
		d.Answer, d.Fallback = "", "unsure"
	}
	b.record(d, d.LatencyMS, false)
	if key != "" && (d.Fallback == "" || d.Fallback == "unsure") {
		b.store(key, d.Answer, c)
	}
	return d.Answer, c, d.Answer != ""
}

// YesNo asks a yes-or-no question.
func (b *Brain) YesNo(ctx context.Context, kind, key, question string) (yes bool, conf float64, ok bool) {
	a, c, ok := b.Choose(ctx, kind, key, question, []string{"yes", "no"})
	return a == "yes", c, ok
}

// SetEnabled turns decisions on or off at runtime.
func (b *Brain) SetEnabled(on bool) { b.off.Store(!on) }

// Enabled reports whether the brain makes decisions.
func (b *Brain) Enabled() bool { return b != nil && !b.off.Load() }

// SetModel changes the engine at runtime and forgets cached decisions.
func (b *Brain) SetModel(m string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Model = m
	b.cache = nil
}

func (b *Brain) model() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.Model == "" {
		return DefaultModel
	}
	return b.Model
}

// DefaultModel is the engine when none is set: the fastest free models.
const DefaultModel = "free/fast"

func (b *Brain) ask(ctx context.Context, question string, options []string) (string, float64, string) {
	user := question + "\n\nAllowed answers: " + strings.Join(quoteAll(options), ", ")
	body, _ := json.Marshal(map[string]any{
		"model": b.model(),
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"temperature":     0,
		"max_tokens":      400, // reasoning models spend tokens before answering
		"response_format": map[string]string{"type": "json_object"},
	})
	status, resp, err := b.Call(ctx, body)
	if err != nil || ctx.Err() != nil {
		if ctx.Err() != nil {
			return "", 0, "timeout"
		}
		return "", 0, "error"
	}
	if status != 200 {
		return "", 0, "error"
	}
	ans, conf, err := parseAnswer(resp, options)
	if err != nil {
		return "", 0, "bad_answer"
	}
	return ans, conf, ""
}

func quoteAll(s []string) []string {
	out := make([]string, len(s))
	for i, x := range s {
		out[i] = fmt.Sprintf("%q", x)
	}
	return out
}

// parseAnswer reads {"answer":..,"confidence":..} from a chat reply,
// tolerating code fences and text around the object.
func parseAnswer(resp []byte, options []string) (string, float64, error) {
	var chat struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(resp, &chat) != nil || len(chat.Choices) == 0 {
		return "", 0, errors.New("no choices")
	}
	text := chat.Choices[0].Message.Content
	i, j := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if i < 0 || j <= i {
		return "", 0, errors.New("no json object")
	}
	var out struct {
		Answer     any      `json:"answer"`
		Confidence *float64 `json:"confidence"`
	}
	if json.Unmarshal([]byte(text[i:j+1]), &out) != nil {
		return "", 0, errors.New("bad json")
	}
	a := strings.ToLower(strings.TrimSpace(fmt.Sprint(out.Answer)))
	if out.Answer == true {
		a = "yes"
	} else if out.Answer == false {
		a = "no"
	}
	match := ""
	for _, o := range options {
		if strings.EqualFold(o, a) {
			match = o
		}
	}
	if match == "" {
		return "", 0, errors.New("answer not allowed")
	}
	conf := 0.5
	if out.Confidence != nil {
		conf = *out.Confidence
		if conf > 1 && conf <= 100 {
			conf /= 100 // some models answer in percent
		}
		if conf < 0 || conf > 1 {
			conf = 0.5
		}
	}
	return match, conf, nil
}

func (b *Brain) cached(key string) (string, float64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, ok := b.cache[key]
	if !ok || time.Now().After(c.until) {
		return "", 0, false
	}
	return c.answer, c.conf, true
}

func (b *Brain) store(key, answer string, conf float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cache == nil || len(b.cache) >= cacheMax {
		b.cache = map[string]cached{}
	}
	b.cache[key] = cached{answer: answer, conf: conf, until: time.Now().Add(cacheTTL)}
}

func (b *Brain) record(d Decision, ms int64, hit bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if hit {
		b.stats.CacheHits++
	} else {
		b.stats.Calls++
		b.totalMS += ms
		b.timed++
		if d.Answer != "" {
			b.stats.Decided++
		} else {
			b.stats.Fallbacks++
		}
	}
	b.stats.Recent = append([]Decision{d}, b.stats.Recent...)
	if len(b.stats.Recent) > recentMax {
		b.stats.Recent = b.stats.Recent[:recentMax]
	}
}

// Snapshot returns the stats.
func (b *Brain) Snapshot() Stats {
	if b == nil {
		return Stats{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.stats
	s.Model = b.Model
	if s.Model == "" {
		s.Model = DefaultModel
	}
	s.Enabled = !b.off.Load()
	s.Recent = append([]Decision(nil), b.stats.Recent...)
	if b.timed > 0 {
		s.AvgMS = b.totalMS / int64(b.timed)
	}
	return s
}
