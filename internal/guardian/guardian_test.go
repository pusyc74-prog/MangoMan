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
	for _, bad := range []string{`{"checks":[]}`, `{"checks":[],"reports":["8am"],"repo":"."}`, `{"checks":[{"name":"x"}]}`, `{"checks":[{"name":"x","url":"u","log":"l"}]}`, `{"checks":[{"name":"x","log":"l","pattern":"("}]}`} {
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
		if strings.Contains(prompt, "asked for:\nadd a contact page") {
			return os.WriteFile(filepath.Join(dir, "contact.html"), []byte("<h1>Contact</h1>"), 0o644)
		}
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

func read(t *testing.T, path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

func TestDevEnvironmentFixApproveLive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh and make")
	}
	repo := gitRepo(t)
	var fixes atomic.Int32
	cfg := &Config{App: "shop", Repo: repo,
		Checks: []Check{{Name: "page", Command: "grep -q fixed " + filepath.Join(repo, "app.txt")}},
		Dev:    Env{Deploy: "date >> deploys"}, Prod: Env{Deploy: "touch deployed"}}
	g := &Guardian{Cfg: cfg, Dir: t.TempDir(), Root: t.TempDir(), Agent: fakeAgent(&fixes, true)}
	ctx := context.Background()
	ev, _ := g.Run(ctx)
	ids := g.Open(ev)
	if len(ids) != 1 || len(g.Open(ev)) != 0 {
		t.Fatalf("one fix per failing check: %v", ids)
	}
	other, _ := g.Request("add a contact page")
	if err := g.Work(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	inc := g.Incidents()[1]
	if inc.ID != ids[0] || inc.Status != "ready" || !strings.Contains(inc.Summary, "Root cause") || !strings.Contains(inc.QA, "make tests passed") {
		t.Fatalf("after working: %+v", inc)
	}
	dev := g.devDir()
	if read(t, filepath.Join(dev, "app.txt")) != "fixed\n" || read(t, filepath.Join(repo, "app.txt")) != "broken\n" {
		t.Fatal("the fix must be running in dev and not yet in production")
	}
	if strings.Count(read(t, filepath.Join(dev, "deploys")), "\n") < 2 {
		t.Fatal("dev should be deployed on day 1 and again with the fix")
	}
	if _, err := os.Stat(filepath.Join(dev, "sneaky.txt")); err == nil {
		t.Fatal("the QA agent may only change tests")
	}
	if err := g.Work(ctx, other); err != errBusy {
		t.Fatalf("dev holds one change at a time: %v", err)
	}
	if err := g.Approve(ctx, inc.ID); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(repo, "app.txt")) != "fixed\n" || g.Incidents()[1].Status != "live" {
		t.Fatalf("after approval: %+v", g.Incidents()[1])
	}
	if _, err := os.Stat(filepath.Join(repo, "deployed")); err != nil {
		t.Fatal("deploy command did not run in the project")
	}
	ev, _ = g.Run(ctx)
	if last := ev[len(ev)-1]; last.Check != "dev" || last.Status != "ok" {
		t.Fatalf("dev should match production again: %+v", last)
	}
	// The owner's change goes the same way.
	if err := g.Work(ctx, other); err != nil || g.Incidents()[0].Status != "ready" {
		t.Fatalf("owner's change: %v %+v", err, g.Incidents()[0])
	}
	// Someone edits dev by hand: drift is reported, not "fixed" by Guardian.
	g.Reject(ctx, other)
	os.WriteFile(filepath.Join(dev, "app.txt"), []byte("hand edit\n"), 0o644)
	commit(dev, "hand edit")
	ev, _ = g.Run(ctx)
	if last := ev[len(ev)-1]; last.Status != "failing" || !strings.Contains(last.Detail, "differs") || len(g.Open(ev)) != 0 {
		t.Fatalf("drift: %+v", last)
	}
}

func TestDevGivesUpAndProductionRollsBack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh and make")
	}
	ctx := context.Background()
	repo := gitRepo(t)
	var fixes atomic.Int32
	cfg := &Config{App: "shop", Repo: repo, Checks: []Check{{Name: "page", Command: "false"}}}
	g := &Guardian{Cfg: cfg, Dir: t.TempDir(), Root: t.TempDir(), Agent: fakeAgent(&fixes, false)}
	ev, _ := g.Run(ctx)
	id := g.Open(ev)[0]
	g.Work(ctx, id)
	if inc := g.Incidents()[0]; inc.Status != "needs_you" || fixes.Load() != maxRounds {
		t.Fatalf("should give up after %d rounds: %+v (fixes %d)", maxRounds, inc, fixes.Load())
	}

	// Passes in dev, fails in production: rolled back, dev back to
	// production's code, and queued to be done again.
	repo2 := gitRepo(t)
	fixes.Store(0)
	cfg2 := &Config{App: "shop", Repo: repo2, Checks: []Check{{Name: "page", Command: "false"}},
		Prod: Env{Deploy: "true", Rollback: "touch rolled-back"}}
	g2 := &Guardian{Cfg: cfg2, Dir: t.TempDir(), Root: t.TempDir(), Agent: fakeAgent(&fixes, true)}
	ev, _ = g2.Run(ctx)
	id = g2.Open(ev)[0]
	g2.Work(ctx, id)
	g2.Approve(ctx, id)
	if _, err := os.Stat(filepath.Join(repo2, "rolled-back")); err != nil {
		t.Fatal("rollback did not run")
	}
	if read(t, filepath.Join(repo2, "app.txt")) != "broken\n" || read(t, filepath.Join(g2.devDir(), "app.txt")) != "broken\n" {
		t.Fatal("production and dev should both be back to the old code")
	}
	if inc := g2.Incidents()[0]; inc.Status != "queued" {
		t.Fatalf("should be queued again: %+v", inc)
	}
	if err := g2.Work(ctx, id); err != nil || g2.Incidents()[0].Status != "ready" || fixes.Load() != 2 {
		t.Fatalf("second try: %v %+v", err, g2.Incidents()[0])
	}
}

func TestMask(t *testing.T) {
	dump := `CREATE TABLE customers (id integer PRIMARY KEY, full_name text, city text, total numeric);
INSERT INTO customers VALUES(1,'Asha Rao','Pune',4999.50);
INSERT INTO public.orders (id, customer_id, email, phone, note) VALUES (9876543210, 1, 'asha@gmail.com', '+91 9876543210', 'call 9123456789, Aadhaar 1234 5678 9012'), (2, 1, NULL, '98765 43210', 'it''s fine');
`
	out := Mask(dump)
	for _, gone := range []string{"Asha", "Pune", "asha@gmail.com", "9876543210'", "9123456789", "1234 5678 9012"} {
		if strings.Contains(out, gone) {
			t.Fatalf("%q not masked:\n%s", gone, out)
		}
	}
	for _, kept := range []string{"(9876543210, 1,", "4999.50", "NULL", "it''s fine", "Customer"} {
		if !strings.Contains(out, kept) {
			t.Fatalf("%q should stay:\n%s", kept, out)
		}
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
		g.answer(context.Background(), u, func(job func()) { queued++; job() })
	}
	if inc := g.Incidents()[0]; inc.Status != "rejected" || queued != 1 {
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
	env := strings.Join(devEnv(map[string]string{"DATABASE_URL": "postgres://test"}), "\n")
	if strings.Contains(env, "STRIPE_SECRET_KEY") || strings.Contains(env, "postgres://live") || !strings.Contains(env, "HARMLESS=yes") || !strings.Contains(env, "DATABASE_URL=postgres://test") {
		t.Fatalf("dev env: %s", env)
	}
}

func TestNightlyDataCopyIsMasked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	repo := gitRepo(t)
	cfg := &Config{App: "shop", Repo: repo, Data: Data{
		Export: `printf "INSERT INTO users (id, email) VALUES (7, 'asha@gmail.com');\n"`,
		Import: "cat > imported.sql", At: "02:00"}}
	g := &Guardian{Cfg: cfg, Dir: t.TempDir(), Root: t.TempDir()}
	if err := g.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 5, 0, 0, 0, 0, time.Local)
	g.dueData(context.Background(), day.Add(time.Hour)) // before 02:00
	if _, err := os.Stat(filepath.Join(g.devDir(), "imported.sql")); err == nil {
		t.Fatal("copied too early")
	}
	g.dueData(context.Background(), day.Add(3*time.Hour))
	got := read(t, filepath.Join(g.devDir(), "imported.sql"))
	if !strings.Contains(got, "(7, 'user1@example.com')") || strings.Contains(got, "asha") {
		t.Fatalf("dev got %q", got)
	}
}

func TestOwnersWorkspaceShips(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh and make")
	}
	ctx := context.Background()
	repo := gitRepo(t)
	var fixes atomic.Int32
	g := &Guardian{Cfg: &Config{App: "shop", Repo: repo}, Dir: t.TempDir(), Root: t.TempDir(), Agent: fakeAgent(&fixes, true)}
	if _, err := g.Ship(""); err == nil {
		t.Fatal("nothing to ship yet")
	}
	ws, _, err := g.Workspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again, _, _ := g.Workspace(ctx); again != ws {
		t.Fatal("the same workspace until it ships")
	}
	os.WriteFile(filepath.Join(ws, "app.txt"), []byte("fixed by the owner\n"), 0o644)
	id, err := g.Ship("")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Work(ctx, id); err != nil {
		t.Fatal(err)
	}
	inc := g.Incidents()[0]
	if inc.Status != "ready" || fixes.Load() != 0 || !strings.Contains(inc.Problem, "Owner's changes") {
		t.Fatalf("the owner's code goes to QA as written: %+v (Guardian fixes %d)", inc, fixes.Load())
	}
	if read(t, filepath.Join(g.devDir(), "app.txt")) != "fixed by the owner\n" || read(t, filepath.Join(repo, "app.txt")) != "broken\n" {
		t.Fatal("the owner's change should be in dev only")
	}
}

func TestFixNeedsATestThatCatchesTheBug(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh and make")
	}
	repo := gitRepo(t)
	os.WriteFile(filepath.Join(repo, "Makefile"), []byte("test:\n\ttrue\n"), 0o644) // passes with or without the fix
	commit(repo, "weak tests")
	var fixes atomic.Int32
	g := &Guardian{Cfg: &Config{App: "shop", Repo: repo, Checks: []Check{{Name: "page", Command: "false"}}},
		Dir: t.TempDir(), Root: t.TempDir(), Agent: fakeAgent(&fixes, true)}
	ev, _ := g.Run(context.Background())
	id := g.Open(ev)[0]
	g.Work(context.Background(), id)
	if inc := g.Incidents()[0]; inc.Status != "needs_you" || !strings.Contains(inc.QA, "would not catch this bug") {
		t.Fatalf("a test that passes without the fix must not count: %+v", inc)
	}
}

func TestMaskJSONAndCSV(t *testing.T) {
	j := Mask(`[{"id":9876543210,"name":"Asha Rao","contact":{"phone":"+91 9876543210"},"note":"mail asha@gmail.com"}]`)
	c := Mask("id,full_name,city,amount\n9876543210,Asha Rao,Pune,4999\n")
	l := Mask("{\"email\":\"asha@gmail.com\",\"total\":5}\n{\"email\":\"ravi@x.in\",\"total\":7}\n")
	for _, out := range []string{j, c, l} {
		for _, gone := range []string{"Asha", "Pune", "asha@gmail.com", "ravi@x.in", "+91 9876543210"} {
			if strings.Contains(out, gone) {
				t.Fatalf("%q not masked:\n%s", gone, out)
			}
		}
	}
	if !strings.Contains(j, "9876543210") || !strings.Contains(c, "9876543210,") || !strings.Contains(l, `"total":7`) {
		t.Fatalf("ids and amounts must stay:\n%q\n%q\n%q", j, c, l)
	}
}

func TestSiteURL(t *testing.T) {
	vercel := "Inspect: https://vercel.com/me/shop/abc [2s]\nPreview: https://shop-git-dev-me.vercel.app [3s]\n"
	netlify := "Website draft URL: https://dev--shop.netlify.app\nBuild logs: https://app.netlify.com/sites/shop/deploys/1\n"
	if siteURL(vercel) != "https://shop-git-dev-me.vercel.app" || siteURL(netlify) != "https://dev--shop.netlify.app" || siteURL("done") != "" {
		t.Fatal(siteURL(vercel), siteURL(netlify))
	}
}

func TestLeakedSecretNeverReachesDev(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh and make")
	}
	repo := gitRepo(t)
	g := &Guardian{Cfg: &Config{App: "shop", Repo: repo}, Dir: t.TempDir(), Root: t.TempDir(),
		Agent: func(_ context.Context, dir, prompt string) error {
			if strings.Contains(prompt, "You are Guardian") {
				os.WriteFile(filepath.Join(dir, "app.txt"), []byte("fixed\nkey = \"rzp_live_abcdefghijklmnop\"\n"), 0o644)
			}
			return nil
		}}
	id, _ := g.Request("take payments")
	g.Work(context.Background(), id)
	if inc := g.Incidents()[0]; inc.Status != "needs_you" || !strings.Contains(inc.QA, "MUST FIX: live payment key") {
		t.Fatalf("%+v", inc)
	}
	if strings.Contains(read(t, filepath.Join(g.devDir(), "app.txt")), "rzp_live") {
		t.Fatal("the secret reached dev")
	}
}
