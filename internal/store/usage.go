// Package store keeps the local usage log. It records outcomes and token
// counts only, never prompt or answer content. Phase 1 uses an append-only
// JSONL file; SQLite replaces it in M2 when the dashboard needs queries.
package store

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Event is one attempt against one candidate.
type Event struct {
	Time      time.Time `json:"time"`
	RequestID string    `json:"request_id"`
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	Class     string    `json:"class"`
	Outcome   string    `json:"outcome"` // ok, rate_limited, error, timeout, quality:<reason>, ...
	Status    int       `json:"status,omitempty"`
	LatencyMS int64     `json:"latency_ms"`
	Tokens    int       `json:"tokens,omitempty"` // total, from usage or estimated
	Attempt   int       `json:"attempt"`
	Stream    bool      `json:"stream"`
	// Parts estimates where the request's input tokens went (instructions,
	// tool definitions, tool results...). Set on the first attempt only.
	Parts map[string]int `json:"parts,omitempty"`
	// Secret: the request seemed to carry a key or password (first attempt).
	Secret bool `json:"secret,omitempty"`
	// Person who sent the request through their own local token (first attempt).
	Person string `json:"person,omitempty"`
}

// BrainClass marks the decision brain's own calls. They use free quota, so
// they count per model, but they are not user requests.
const BrainClass = "brain"

// Log appends events to a file. A nil *Log discards events.
type Log struct {
	mu sync.Mutex
	f  *os.File
}

// OpenLog opens (or creates) the log file.
func OpenLog(path string) (*Log, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &Log{f: f}, nil
}

// Add writes one event.
func (l *Log) Add(e Event) {
	if l == nil {
		return
	}
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.f.Write(append(data, '\n'))
}

// Close closes the file.
func (l *Log) Close() error {
	if l == nil {
		return nil
	}
	return l.f.Close()
}
