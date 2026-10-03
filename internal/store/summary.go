package store

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"
)

// Row summarises attempts for one provider and model.
type Row struct {
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	Attempts    int    `json:"attempts"`
	OK          int    `json:"ok"`
	RateLimited int    `json:"rate_limited"`
	Errors      int    `json:"errors"`
	QualityFail int    `json:"quality_fail"`
	Tokens      int    `json:"tokens"`
	P50MS       int64  `json:"p50_ms"`
	lat         []int64
}

// Summary is usage over a period.
type Summary struct {
	Since      time.Time `json:"since"`
	Requests   int       `json:"requests"`    // distinct client requests
	Served     int       `json:"served"`      // requests that ended in ok
	FailedOver int       `json:"failed_over"` // served, but not on the first attempt
	BrainCalls int       `json:"brain_calls"` // decision brain calls, not in the counts above
	Rows       []Row     `json:"rows"`
}

// Summarize reads a usage log and aggregates events since a time.
func Summarize(path string, since time.Time) (Summary, error) {
	s := Summary{Since: since}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	defer f.Close()

	rows := map[string]*Row{}
	type reqState struct {
		ok      bool
		attempt int
	}
	reqs := map[string]*reqState{}
	brain := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Time.Before(since) {
			continue
		}
		k := e.Provider + "/" + e.Model
		r, ok := rows[k]
		if !ok {
			r = &Row{Provider: e.Provider, Model: e.Model}
			rows[k] = r
		}
		r.Attempts++
		r.Tokens += e.Tokens
		r.lat = append(r.lat, e.LatencyMS)
		rs, ok := reqs[e.RequestID]
		if !ok {
			rs = &reqState{}
			if e.Class != BrainClass {
				reqs[e.RequestID] = rs
			} else if !brain[e.RequestID] {
				brain[e.RequestID] = true
				s.BrainCalls++
			}
		}
		switch {
		case strings.HasPrefix(e.Outcome, "ok"):
			r.OK++
			rs.ok, rs.attempt = true, e.Attempt
		case e.Outcome == "rate_limited":
			r.RateLimited++
		case strings.HasPrefix(e.Outcome, "quality:"):
			r.QualityFail++
		case e.Outcome == "client_gone":
		default:
			r.Errors++
		}
	}
	if err := sc.Err(); err != nil {
		return s, err
	}
	s.Requests = len(reqs)
	for _, rs := range reqs {
		if rs.ok {
			s.Served++
			if rs.attempt > 1 {
				s.FailedOver++
			}
		}
	}
	for _, r := range rows {
		sort.Slice(r.lat, func(i, j int) bool { return r.lat[i] < r.lat[j] })
		if len(r.lat) > 0 {
			r.P50MS = r.lat[len(r.lat)/2]
		}
		s.Rows = append(s.Rows, *r)
	}
	sort.Slice(s.Rows, func(i, j int) bool {
		if s.Rows[i].Attempts != s.Rows[j].Attempts {
			return s.Rows[i].Attempts > s.Rows[j].Attempts
		}
		return s.Rows[i].Provider+s.Rows[i].Model < s.Rows[j].Provider+s.Rows[j].Model
	})
	return s, nil
}

// HourBucket counts attempts per provider in one hour.
type HourBucket struct {
	Hour     time.Time `json:"hour"`
	Provider string    `json:"provider"`
	Attempts int       `json:"attempts"`
	OK       int       `json:"ok"`
}

// Activity is what the dashboard shows: totals, per-model rows, requests per
// hour per provider, and the most recent user attempts (never any content).
type Activity struct {
	Summary
	Hourly []HourBucket `json:"hourly"`
	Recent []Event      `json:"recent"`
}

// Analyze reads the log once for the dashboard.
func Analyze(path string, since time.Time, recentN int) (Activity, error) {
	sum, err := Summarize(path, since)
	a := Activity{Summary: sum}
	if err != nil {
		return a, err
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return a, nil
	}
	if err != nil {
		return a, err
	}
	defer f.Close()
	type hk struct {
		h time.Time
		p string
	}
	hours := map[hk]*HourBucket{}
	var ring []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Time.Before(since) {
			continue
		}
		k := hk{e.Time.UTC().Truncate(time.Hour), e.Provider}
		b, ok := hours[k]
		if !ok {
			b = &HourBucket{Hour: k.h, Provider: e.Provider}
			hours[k] = b
		}
		b.Attempts++
		if strings.HasPrefix(e.Outcome, "ok") {
			b.OK++
		}
		if recentN > 0 && e.Class != BrainClass {
			ring = append(ring, e)
			if len(ring) > recentN {
				ring = ring[1:]
			}
		}
	}
	for _, b := range hours {
		a.Hourly = append(a.Hourly, *b)
	}
	sort.Slice(a.Hourly, func(i, j int) bool {
		if !a.Hourly[i].Hour.Equal(a.Hourly[j].Hour) {
			return a.Hourly[i].Hour.Before(a.Hourly[j].Hour)
		}
		return a.Hourly[i].Provider < a.Hourly[j].Provider
	})
	for i := len(ring) - 1; i >= 0; i-- {
		a.Recent = append(a.Recent, ring[i]) // newest first
	}
	return a, sc.Err()
}
