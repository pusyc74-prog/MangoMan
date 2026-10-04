package guardian

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGuardianWatchesFixesAndReports(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixes run through sh in this test")
	}
	dir := t.TempDir()
	healthy := filepath.Join(dir, "healthy")
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := os.Stat(healthy); err != nil {
			http.Error(w, "down", 502)
		}
	}))
	defer site.Close()
	logf := filepath.Join(dir, "app.log")
	if err := os.WriteFile(logf, []byte("ERROR old line, before Guardian\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{App: "shop", Webhook: "https://hooks.example/x", Checks: []Check{
		{Name: "site", URL: site.URL, Fix: []string{"false", "touch " + healthy}},
		{Name: "errors", Log: logf, Fix: []string{"true"}},
		{Name: "jobs", Command: "echo 3 jobs stuck; exit 1"},
	}}
	var sent []string
	var asked atomic.Int32
	g := &Guardian{Cfg: cfg, Dir: dir,
		Ask:  func(p string) (string, error) { asked.Add(1); return "The upstream crashed.", nil },
		Send: func(_ string, b []byte) error { sent = append(sent, string(b)); return nil }}

	ev, err := g.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Event{}
	for _, e := range ev {
		by[e.Check] = e
	}
	if s := by["site"]; s.Status != "fixed" || len(s.Done) != 2 || !strings.Contains(s.Done[0], "failed") || s.Cause == "" {
		t.Fatalf("site: %+v", s)
	}
	if by["errors"].Status != "ok" {
		t.Fatalf("old log lines must not count: %+v", by["errors"])
	}
	if j := by["jobs"]; j.Status != "failing" || !strings.Contains(j.Detail, "3 jobs stuck") {
		t.Fatalf("jobs: %+v", j)
	}
	if len(sent) != 2 || asked.Load() != 2 {
		t.Fatalf("webhook %d, asked %d", len(sent), asked.Load())
	}

	f, _ := os.OpenFile(logf, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("INFO fine\npanic: nil map\n")
	f.Close()
	ev, _ = g.Run(context.Background())
	if ev[0].Status != "ok" || ev[1].Status != "fixed" || !strings.Contains(ev[1].Detail, "panic: nil map") {
		t.Fatalf("second run: %+v", ev)
	}

	rep, err := g.Report(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"site: failed 1 of 2 times, fixed by Guardian 1 of them", "Likely cause: The upstream crashed.", "Needs you: jobs still failing."} {
		if !strings.Contains(rep, want) {
			t.Fatalf("report missing %q:\n%s", want, rep)
		}
	}
}

func TestLoadRejectsBadChecks(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{`{"checks":[]}`, `{"checks":[{"name":"x"}]}`, `{"checks":[{"name":"x","url":"u","log":"l"}]}`, `{"checks":[{"name":"x","log":"l","pattern":"("}]}`} {
		p := filepath.Join(dir, "g.json")
		os.WriteFile(p, []byte(bad), 0o644)
		if _, err := Load(p); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

// gitRepo makes a project whose test passes only once app.txt says "fixed".
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "app.txt"), []byte("broken\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "Makefile"), []byte("test:\n\tgrep -q fixed app.txt\n"), 0o644)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-q", "-m", "start"}} {
		if _, err := git(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// fakeAgent fixes app.txt when asked to fix, and as the QA agent writes a
// test but also tries to change app code (which must be undone).
func fakeAgent(fixes *atomic.Int32, works bool) func(context.Context, string, string) error {
	return func(_ context.Context, dir, prompt string) error {
		if strings.Contains(prompt, "You are Guardian") {
			fixes.Add(1)
			if works {
				os.WriteFile(filepath.Join(dir, "app.txt"), []byte("fixed\n"), 0o644)
			}
			os.WriteFile(filepath.Join(dir, "GUARDIAN_FIX.md"), []byte("Root cause: the text said broken."), 0o644)
			return nil
		}
		os.MkdirAll(filepath.Join(dir, "tests"), 0o755)
		os.WriteFile(filepath.Join(dir, "tests", "test_app.py"), []byte("# regression test\n"), 0o644)
		os.WriteFile(filepath.Join(dir, "sneaky.txt"), []byte("not a test"), 0o644)
		return nil
	}
}

func TestIncidentFixApproveLive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh and make")
	}
	repo := gitRepo(t)
	var fixes atomic.Int32
	cfg := &Config{App: "shop", Repo: repo,
		Checks: []Check{{Name: "page", Command: "grep -q fixed " + filepath.Join(repo, "app.txt")}},
		Prod:   Env{Deploy: "touch deployed"}}
	g := &Guardian{Cfg: cfg, Dir: t.TempDir(), Work: t.TempDir(), Agent: fakeAgent(&fixes, true)}
	ev, _ := g.Run(context.Background())
	ids := g.Open(ev)
	if len(ids) != 1 || len(g.Open(ev)) != 0 {
		t.Fatalf("one incident per failing check: %v", ids)
	}
	if err := g.Fix(context.Background(), ids[0]); err != nil {
		t.Fatal(err)
	}
	inc := g.Incidents()[0]
	if inc.Status != "ready" || !strings.Contains(inc.Summary, "Root cause") || !strings.Contains(inc.QA, "make tests passed") {
		t.Fatalf("after fixing: %+v", inc)
	}
	if _, err := os.Stat(filepath.Join(inc.Work, "sneaky.txt")); err == nil {
		t.Fatal("the QA agent may only change tests")
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "app.txt")); string(b) != "broken\n" {
		t.Fatal("production code changed before approval")
	}
	if err := g.Approve(context.Background(), inc.ID); err != nil {
		t.Fatal(err)
	}
	inc = g.Incidents()[0]
	if b, _ := os.ReadFile(filepath.Join(repo, "app.txt")); inc.Status != "live" || string(b) != "fixed\n" {
		t.Fatalf("after approval: %+v %q", inc, b)
	}
	if _, err := os.Stat(filepath.Join(repo, "deployed")); err != nil {
		t.Fatal("deploy command did not run in the project")
	}
	if !strings.Contains(g.incidentLines(), "live and passed QA in production") {
		t.Fatalf("report: %s", g.incidentLines())
	}
}

func TestIncidentGivesUpAndRollsBack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh and make")
	}
	repo := gitRepo(t)
	var fixes atomic.Int32
	cfg := &Config{App: "shop", Repo: repo, Checks: []Check{{Name: "page", Command: "false"}}}
	var sent []string
	g := &Guardian{Cfg: cfg, Dir: t.TempDir(), Work: t.TempDir(), Agent: fakeAgent(&fixes, false),
		TG: nil, Send: func(_ string, b []byte) error { sent = append(sent, string(b)); return nil }}
	cfg.Webhook = "https://hooks.example/x"
	ev, _ := g.Run(context.Background())
	id := g.Open(ev)[0]
	g.Fix(context.Background(), id)
	if inc := g.Incidents()[0]; inc.Status != "needs_you" || fixes.Load() != maxRounds {
		t.Fatalf("should give up after %d rounds: %+v (fixes %d)", maxRounds, inc, fixes.Load())
	}

	// A fix that passes in development but fails in production is rolled back.
	repo2 := gitRepo(t)
	fixes.Store(0)
	cfg2 := &Config{App: "shop", Repo: repo2, Checks: []Check{{Name: "page", Command: "false"}},
		Prod: Env{Deploy: "true", Rollback: "touch rolled-back"}}
	g2 := &Guardian{Cfg: cfg2, Dir: t.TempDir(), Work: t.TempDir(), Agent: fakeAgent(&fixes, true)}
	ev, _ = g2.Run(context.Background())
	id = g2.Open(ev)[0]
	g2.Fix(context.Background(), id)
	g2.Approve(context.Background(), id)
	if _, err := os.Stat(filepath.Join(repo2, "rolled-back")); err != nil {
		t.Fatal("rollback did not run")
	}
	if b, _ := os.ReadFile(filepath.Join(repo2, "app.txt")); string(b) != "broken\n" {
		t.Fatalf("merge not reverted: %q", b)
	}
	if fixes.Load() < 2 {
		t.Fatal("Guardian should work on it again after the rollback")
	}
}

func TestTelegramApprovalAndReports(t *testing.T) {
	var got []map[string]any
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		in["method"] = filepath.Base(r.URL.Path)
		got = append(got, in)
		if in["method"] == "getUpdates" {
			w.Write([]byte(`{"ok":true,"result":[{"update_id":7,"callback_query":{"id":"cb","data":"reject:x1","message":{"chat":{"id":42}}}},
{"update_id":8,"callback_query":{"id":"cb2","data":"approve:x1","message":{"chat":{"id":99}}}}]}`))
			return
		}
		w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer api.Close()
	tg := &Telegram{Token: "T", Chat: "42", API: api.URL}
	dir := t.TempDir()
	g := &Guardian{Cfg: &Config{App: "shop", Reports: []string{"08:00", "20:00"}}, Dir: dir, TG: tg}
	os.WriteFile(filepath.Join(dir, "incidents.json"), []byte(`{"x1":{"id":"x1","check":"site","status":"ready"}}`), 0o600)
	ups, err := tg.Updates(context.Background(), 0, 0)
	if err != nil || len(ups) != 2 {
		t.Fatalf("updates %v %v", ups, err)
	}
	queued := 0
	for _, u := range ups {
		g.answer(context.Background(), u, func(func()) { queued++ })
	}
	if inc := g.Incidents()[0]; inc.Status != "rejected" || queued != 0 {
		t.Fatalf("owner's reject must apply and a stranger's approve must not: %+v queued %d", inc, queued)
	}

	g.record([]Event{{Time: time.Now(), Check: "site", Status: "ok"}})
	day := time.Date(2026, 10, 4, 0, 0, 0, 0, time.Local)
	n := len(got)
	g.dueReports(day.Add(7 * time.Hour)) // before 08:00: nothing
	g.dueReports(day.Add(8*time.Hour + time.Minute))
	g.dueReports(day.Add(9 * time.Hour)) // already sent
	g.dueReports(day.Add(20*time.Hour + 5*time.Minute))
	var reports []string
	for _, m := range got[n:] {
		reports = append(reports, m["text"].(string))
	}
	if len(reports) != 2 || !strings.Contains(reports[0], "All good") {
		t.Fatalf("want morning and evening reports even on a quiet day: %q", reports)
	}
}

func TestDevEnvDropsSecrets(t *testing.T) {
	t.Setenv("STRIPE_SECRET_KEY", "live")
	t.Setenv("DATABASE_URL", "postgres://live")
	t.Setenv("HARMLESS", "yes")
	env := strings.Join(devEnv(map[string]string{"DATABASE_URL": "postgres://test"}, 1234), "\n")
	if strings.Contains(env, "STRIPE_SECRET_KEY") || strings.Contains(env, "postgres://live") || !strings.Contains(env, "HARMLESS=yes") || !strings.Contains(env, "DATABASE_URL=postgres://test") || !strings.Contains(env, "PORT=1234") {
		t.Fatalf("dev env: %s", env)
	}
}
