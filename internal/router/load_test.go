package router

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/breaker"
	"github.com/pusyc74-prog/mangoman/internal/catalogue"
)

// TestLoad sends many requests at once through one router, with one
// provider refusing every tenth request, and checks every one is answered
// (by failover where needed) and quickly. Skipped with -short.
func TestLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("load test")
	}
	var n atomic.Int64
	flaky := &fake{id: "a", model: "m1", quality: 0.8, limits: catalogue.Limits{RPM: 1 << 30}, handler: func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1)%10 == 0 {
			http.Error(w, `{"error":{"message":"busy"}}`, 503)
			return
		}
		okJSON("a")(w, r)
	}}
	steady := &fake{id: "b", model: "m2", quality: 0.8, limits: catalogue.Limits{RPM: 1 << 30}, handler: okJSON("b")}
	rt := setup(t, flaky, steady)
	rt.Breakers = breaker.New(1<<30, time.Second, time.Second) // keep both in play: this measures the router, not the breaker
	const total, workers = 3000, 300

	var failed atomic.Int64
	jobs := make(chan int)
	var wg sync.WaitGroup
	start := time.Now()
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				body := `{"messages":[{"role":"user","content":"hello ` + string(rune('a'+i%26)) + `"}]}`
				if w := do(t, rt, body); w.Code != 200 {
					failed.Add(1)
				}
			}
		}()
	}
	for i := range total {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	took := time.Since(start)
	rate := float64(total) / took.Seconds()
	t.Logf("%d requests, %d at once: %s, %.0f per second, %d failed", total, workers, took.Round(time.Millisecond), rate, failed.Load())
	if failed.Load() != 0 || rate < 50 {
		t.Fatalf("%d failed, %.0f per second", failed.Load(), rate)
	}
}
