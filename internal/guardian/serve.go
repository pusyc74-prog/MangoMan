package guardian

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Serve keeps watching until ctx ends: checks every `every`, works queued
// changes one at a time in the background (development holds one change at
// a time), takes approvals and change requests from Telegram, copies data
// to dev nightly, and sends the daily reports. show prints each run's events.
func (g *Guardian) Serve(ctx context.Context, every time.Duration, show func([]Event)) error {
	jobs := make(chan func(), 16)
	go func() {
		for job := range jobs {
			job()
		}
	}()
	defer close(jobs)
	queue := func(job func()) {
		select {
		case jobs <- job:
		default:
			g.logf("too much waiting work; this job waits for the next run")
		}
	}
	if g.Cfg.Repo != "" && g.Agent != nil {
		if err := g.Setup(ctx); err != nil { // day 1: dev next to production
			g.logf("development environment: %v", err)
		}
	}
	// Pick up work left when Guardian last stopped.
	for _, i := range g.Incidents() {
		switch i.Status {
		case "working":
			g.update(i.ID, func(i *Incident) { i.Status = "queued" })
		case "deploying":
			g.update(i.ID, func(i *Incident) {
				i.Status, i.Note = "needs_you", "Guardian stopped while deploying: check production"
			})
		}
	}
	running := false // a Work job is queued or running; only Serve's loop reads and sets it
	done := make(chan struct{}, 1)
	offset := 0
	for {
		events, err := g.Run(ctx)
		if err != nil {
			return err
		}
		show(events)
		g.Open(events)
		select {
		case <-done:
			running = false
		default:
		}
		if id := g.next(); id != "" && !running {
			running = true
			queue(func() {
				if err := g.Work(ctx, id); err != nil && err != errBusy {
					g.logf("%s: %v", id, err)
				}
				done <- struct{}{}
			})
		}
		g.dueReports(time.Now())
		go g.dueData(ctx, time.Now())
		next := time.Now().Add(every)
		for time.Now().Before(next) {
			if ctx.Err() != nil {
				return nil
			}
			if g.TG == nil {
				select {
				case <-ctx.Done():
				case <-time.After(time.Until(next)):
				}
				continue
			}
			ups, err := g.TG.Updates(ctx, offset, min(time.Until(next), 50*time.Second))
			if err != nil {
				select { // offline: try again shortly
				case <-ctx.Done():
				case <-time.After(min(time.Until(next), 30*time.Second)):
				}
				continue
			}
			for _, u := range ups {
				offset = u.ID + 1
				g.answer(ctx, u, queue)
			}
		}
	}
}

// answer handles one Telegram update from the owner: a button, /report, or
// in plain words a change they want.
func (g *Guardian) answer(ctx context.Context, u Update, queue func(func())) {
	if u.Chat != g.TG.Chat {
		return // someone else found the bot
	}
	action, id, _ := strings.Cut(u.Data, ":")
	switch {
	case action == "approve":
		g.TG.Ack(u.Button, "Deploying to production")
		queue(func() {
			if err := g.Approve(ctx, id); err != nil {
				g.notify(g.Cfg.App + ": " + err.Error())
			}
		})
	case action == "reject":
		g.TG.Ack(u.Button, "Rejected")
		queue(func() {
			if err := g.Reject(ctx, id); err == nil {
				g.notify(g.Cfg.App + ": " + id + " rejected; production unchanged.")
			}
		})
	case strings.HasPrefix(u.Text, "/report"):
		if rep, err := g.Report(false); err == nil {
			_ = g.TG.Send(rep)
		}
	case action == "change":
		g.TG.Ack(u.Button, "Queued")
		if what, ok := g.asked[id]; ok {
			delete(g.asked, id)
			if _, err := g.Request(what); err != nil {
				_ = g.TG.Send(err.Error())
			}
		}
	case u.Text != "" && !strings.HasPrefix(u.Text, "/"):
		// A message in plain words may be a change; ask before queuing it.
		if g.asked == nil {
			g.asked = map[string]string{}
		}
		key := strconv.Itoa(u.ID)
		g.asked[key] = u.Text
		_ = g.TG.Send("Make this change? It goes to development first, and to production only after you approve.\n"+cut(u.Text, 300),
			[2]string{"Yes, make it", "change:" + key})
	}
}

// dueReports sends each daily report once its time has passed today.
func (g *Guardian) dueReports(now time.Time) {
	path := filepath.Join(g.Dir, "reports-sent.json")
	var last time.Time
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &last)
	}
	due := false
	for _, r := range g.Cfg.Reports {
		t, err := time.ParseInLocation("15:04", r, now.Location())
		if err != nil {
			continue
		}
		at := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
		due = due || !now.Before(at) && last.Before(at)
	}
	if !due {
		return
	}
	if _, err := g.Report(true); err == nil {
		b, _ := json.Marshal(now)
		_ = os.WriteFile(path, b, 0o600)
	}
}
