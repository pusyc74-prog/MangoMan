package ingress

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/opencode"
	"github.com/pusyc74-prog/mangoman/internal/pyenv"
)

// The setup page's Get ready: sizes before anything downloads, progress
// while it runs, one run at a time, a plain error, and a second try.
func TestGetReady(t *testing.T) {
	t.Setenv("MANGOMAN_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // no OpenCode of the user's own
	big := strings.Repeat("x", 61_000_000/1000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "61000000")
		if r.Method == http.MethodGet {
			w.Write([]byte(big))
		}
	}))
	defer srv.Close()
	old := opencode.Releases
	opencode.Releases = srv.URL + "/"
	defer func() { opencode.Releases = old }()

	h, s := dashServer(t, mem{})
	release, fails := make(chan struct{}), true
	s.GetReady = func(home string, step pyenv.Step) error {
		step("Downloading the coding helper", 30, 61)
		<-release
		if fails {
			return errors.New("could not reach the download site; check the internet connection and try again")
		}
		return nil
	}
	get := func() ReadyView {
		t.Helper()
		w := call(h, "GET", "/mangoman/ready", "127.0.0.1:4141", authz, "")
		var v ReadyView
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != 200 {
			t.Fatalf("%d %v %s", w.Code, err, w.Body)
		}
		return v
	}
	v := get()
	if len(v.Parts) != 3 || v.Parts[0].Bytes != 61_000_000 || v.Total <= v.Parts[0].Bytes || v.Ready || v.Keys != 0 || v.Running {
		t.Fatalf("before: %+v", v)
	}
	if w := call(h, "POST", "/mangoman/ready", "127.0.0.1:4141", authz, ""); w.Code != 200 {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for v = get(); v.Of == 0 && time.Now().Before(deadline); v = get() {
		time.Sleep(10 * time.Millisecond)
	}
	if !v.Running || v.Step != "Downloading the coding helper" || v.Done != 30 || v.Of != 61 {
		t.Fatalf("while running: %+v", v)
	}
	if w := call(h, "POST", "/mangoman/ready", "127.0.0.1:4141", authz, ""); w.Code != http.StatusConflict {
		t.Fatalf("a second run at once: %d", w.Code)
	}
	release <- struct{}{}
	for v = get(); v.Running && time.Now().Before(deadline); v = get() {
		time.Sleep(10 * time.Millisecond)
	}
	if v.Running || !strings.Contains(v.Error, "internet connection") || len(v.Parts) != 3 {
		t.Fatalf("after a failure: %+v", v)
	}
	fails = false
	call(h, "POST", "/mangoman/ready", "127.0.0.1:4141", authz, "")
	release <- struct{}{}
	for v = get(); v.Running && time.Now().Before(deadline); v = get() {
		time.Sleep(10 * time.Millisecond)
	}
	if v.Running || v.Error != "" {
		t.Fatalf("a second try: %+v", v)
	}
	for _, path := range []string{"/mangoman/ready", "/mangoman/code/open"} {
		if w := call(h, "POST", path, "127.0.0.1:4141", nil, ""); w.Code != 401 {
			t.Errorf("%s without the token: %d", path, w.Code)
		}
	}
}
