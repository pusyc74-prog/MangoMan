package breaker

import (
	"testing"
	"time"
)

func TestBreakerCycle(t *testing.T) {
	now := time.Unix(0, 0)
	s := New(3, 30*time.Second, 5*time.Minute)
	s.SetClock(func() time.Time { return now })
	k := "groq/m"
	s.Failure(k)
	s.Failure(k)
	if !s.Allow(k) || s.StateOf(k) != Closed {
		t.Fatal("should stay closed below threshold")
	}
	s.Failure(k)
	if s.Allow(k) || s.StateOf(k) != Open {
		t.Fatal("should open at threshold")
	}
	now = now.Add(31 * time.Second)
	if s.StateOf(k) != HalfOpen || !s.Allow(k) {
		t.Fatal("should let one probe through")
	}
	if s.Allow(k) {
		t.Fatal("only one probe at a time")
	}
	s.Failure(k) // probe failed: re-open with doubled cooldown
	now = now.Add(31 * time.Second)
	if s.Allow(k) {
		t.Fatal("cooldown should have doubled to 60s")
	}
	now = now.Add(30 * time.Second)
	if !s.Allow(k) {
		t.Fatal("probe after 60s")
	}
	s.Success(k)
	if s.StateOf(k) != Closed {
		t.Fatal("success closes")
	}
}
