package guardian

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
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
