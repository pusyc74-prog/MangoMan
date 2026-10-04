package guardian

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Serve keeps watching until ctx ends: checks every `every`, opens and works
// incidents one at a time in the background, takes approvals from Telegram,
// and sends the daily reports. show prints each run's events.
func (g *Guardian) Serve(ctx context.Context, every time.Duration, show func([]Event)) error {
	jobs := make(chan func(), 64)
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
	fix := func(id string) func() {
		return func() {
			if err := g.Fix(ctx, id); err != nil {
				g.logf("%s: %v", id, err)
			}
		}
	}
	// Pick up work left when Guardian last stopped.
	for _, i := range g.Incidents() {
		switch i.Status {
		case "fixing":
			queue(fix(i.ID))
		case "deploying":
			g.update(i.ID, func(i *Incident) {
				i.Status, i.Note = "needs_you", "Guardian stopped while deploying: check production"
			})
		}
	}
	offset := 0
	for {
		events, err := g.Run(ctx)
		if err != nil {
			return err
		}
		show(events)
		for _, id := range g.Open(events) {
			queue(fix(id))
		}
		g.dueReports(time.Now())
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
			wait := min(time.Until(next), 50*time.Second)
			ups, err := g.TG.Updates(ctx, offset, wait)
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

// answer handles one Telegram update from the owner.
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
		if err := g.Reject(id); err == nil {
			g.notify(g.Cfg.App + ": fix " + id + " rejected; production unchanged.")
		}
	case strings.HasPrefix(u.Text, "/report"):
		if rep, err := g.Report(false); err == nil {
			_ = g.TG.Send(rep)
		}
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
