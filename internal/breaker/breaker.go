// Package breaker implements per-target circuit breakers (closed, open,
// half-open) with exponential cooldown and jitter.
package breaker

import (
	"math/rand"
	"sync"
	"time"
)

// State of one breaker.
type State string

const (
	Closed   State = "closed"
	Open     State = "open"
	HalfOpen State = "half-open"
)

type entry struct {
	fails     int
	trips     int
	openUntil time.Time
	probing   bool
}

// Set holds breakers keyed by target (for example "groq/llama-3.3-70b").
type Set struct {
	mu        sync.Mutex
	m         map[string]*entry
	threshold int
	base, max time.Duration
	now       func() time.Time
	jitter    func(time.Duration) time.Duration
}

// New returns a breaker set: it opens after threshold consecutive failures,
// for base cooldown doubling per trip up to max.
func New(threshold int, base, max time.Duration) *Set {
	return &Set{
		m: map[string]*entry{}, threshold: threshold, base: base, max: max, now: time.Now,
		jitter: func(d time.Duration) time.Duration { return time.Duration(rand.Int63n(int64(d)/5 + 1)) },
	}
}

// SetClock replaces the clock and removes jitter, for tests.
func (s *Set) SetClock(now func() time.Time) {
	s.now = now
	s.jitter = func(time.Duration) time.Duration { return 0 }
}

func (s *Set) get(k string) *entry {
	e, ok := s.m[k]
	if !ok {
		e = &entry{}
		s.m[k] = e
	}
	return e
}

// Allow reports whether a request may go to the target. In half-open state a
// single probe is let through.
func (s *Set) Allow(k string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.get(k)
	if e.openUntil.IsZero() {
		return true
	}
	if s.now().Before(e.openUntil) {
		return false
	}
	if e.probing {
		return false
	}
	e.probing = true
	return true
}

// Success closes the breaker.
func (s *Set) Success(k string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.get(k)
	*e = entry{}
}

// Failure records a failure and opens the breaker at the threshold, or
// re-opens it with a longer cooldown if a half-open probe failed.
func (s *Set) Failure(k string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.get(k)
	e.fails++
	if e.probing || e.fails >= s.threshold {
		cool := s.base << e.trips
		if cool > s.max || cool <= 0 {
			cool = s.max
		}
		e.openUntil = s.now().Add(cool + s.jitter(cool))
		e.trips++
		e.fails = 0
		e.probing = false
	}
}

// Release ends a half-open probe that proved nothing either way (a rate
// limit, a client error, the client hanging up), so the next request may
// probe again. It does nothing after Success or Failure.
func (s *Set) Release(k string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.m[k]; ok {
		e.probing = false
	}
}

// OpenUntil returns when an open breaker lets a probe through again; zero
// when it is not open.
func (s *Set) OpenUntil(k string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.m[k]; ok && s.now().Before(e.openUntil) {
		return e.openUntil
	}
	return time.Time{}
}

// StateOf returns the current state of a target.
func (s *Set) StateOf(k string) State {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[k]
	switch {
	case !ok || e.openUntil.IsZero():
		return Closed
	case s.now().Before(e.openUntil):
		return Open
	default:
		return HalfOpen
	}
}
