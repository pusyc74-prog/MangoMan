package router

import (
	"sort"
	"sync"
	"time"
)

// Health keeps live measurements per target (provider/model): how fast it
// answers and how often it succeeds. Routing uses measured speed once a
// target has a few samples, instead of the catalogue's estimate, so slow
// models sink in the ranking on their own.
type Health struct {
	mu sync.Mutex
	m  map[string]*TargetStats
}

// TargetStats is what we know about one target from this run.
type TargetStats struct {
	Target    string    `json:"target"`
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	Samples   int       `json:"samples"`
	OK        int       `json:"ok"`
	Failed    int       `json:"failed"`
	Timed     int       `json:"timed"`      // attempts that gave a latency
	LatencyMS float64   `json:"latency_ms"` // moving average: time to first output (streams) or full answer
	LastUsed  time.Time `json:"last_used"`
	LastOut   string    `json:"last_outcome"`
}

// NewHealth returns an empty tracker.
func NewHealth() *Health { return &Health{m: map[string]*TargetStats{}} }

const latencyAlpha = 0.3

// Observe records one attempt. latency is ignored when zero (no answer).
func (h *Health) Observe(c Candidate, outcome string, ok bool, latency time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	t := c.Target()
	s, found := h.m[t]
	if !found {
		s = &TargetStats{Target: t, Provider: c.Provider.ID, Model: c.Model.Canonical}
		h.m[t] = s
	}
	s.Samples++
	if ok {
		s.OK++
	} else {
		s.Failed++
	}
	// Any latency given is counted, success or not. The caller decides
	// which failures were slow enough to say something about speed.
	if latency > 0 {
		s.Timed++
		ms := float64(latency.Milliseconds())
		if s.LatencyMS == 0 {
			s.LatencyMS = ms
		} else {
			s.LatencyMS = latencyAlpha*ms + (1-latencyAlpha)*s.LatencyMS
		}
	}
	s.LastUsed = time.Now()
	s.LastOut = outcome
}

// Speed returns a 0..1 speed score from measurements, and false when there
// are too few samples to trust it. The curve is 1/(1+t/2s):
// 0.3 s scores 0.87, 2 s 0.5, 10 s 0.17, 60 s 0.03.
func (h *Health) Speed(target string) (float64, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.m[target]
	if !ok || s.Timed < 2 {
		return 0, false
	}
	return 1 / (1 + s.LatencyMS/2000), true
}

// Rate returns the share of attempts that succeeded, and false when there
// are fewer than 3 attempts to judge by.
func (h *Health) Rate(target string) (float64, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.m[target]
	if !ok || s.Samples < 3 {
		return 0, false
	}
	return float64(s.OK) / float64(s.Samples), true
}

// Snapshot returns all targets, most used first.
func (h *Health) Snapshot() []TargetStats {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]TargetStats, 0, len(h.m))
	for _, s := range h.m {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Samples != out[j].Samples {
			return out[i].Samples > out[j].Samples
		}
		return out[i].Target < out[j].Target
	})
	return out
}
