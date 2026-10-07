package ingress

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCodeWorkspaceProxy(t *testing.T) {
	var gotPath, gotAuth string
	oc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.RequestURI(), r.Header.Get("Authorization")
		u, p, ok := r.BasicAuth()
		if !ok || u != "opencode" || p != "secret" {
			http.Error(w, "no", 401)
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer oc.Close()
	h, _ := dashServer(t, mem{})

	// Nothing attached yet: a clear message, not a crash.
	if w := call(h, "GET", "/mangoman/code/oc/session", "127.0.0.1:4141", authz, ""); w.Code != 503 {
		t.Fatalf("no workspace: %d %s", w.Code, w.Body)
	}
	// Only a server on this computer may be attached.
	if w := call(h, "POST", "/mangoman/code/attach", "127.0.0.1:4141", authz, `{"url":"http://example.com:80","password":"x","dir":"/p"}`); w.Code != 400 {
		t.Fatalf("a remote address must be refused: %d", w.Code)
	}
	if w := call(h, "POST", "/mangoman/code/attach", "127.0.0.1:4141", authz, `{"url":"`+oc.URL+`","password":"secret","dir":"/p"}`); w.Code != 204 {
		t.Fatalf("attach: %d %s", w.Code, w.Body)
	}
	// Calls pass through with OpenCode's password, never the router token.
	w := call(h, "GET", "/mangoman/code/oc/vcs/diff?mode=git", "127.0.0.1:4141", authz, "")
	if w.Code != 200 || gotPath != "/vcs/diff?mode=git" || strings.Contains(gotAuth, "tok") {
		t.Fatalf("proxy: %d path %q auth %q", w.Code, gotPath, gotAuth)
	}
	// Without the router token, nothing reaches OpenCode.
	gotPath = ""
	if w := call(h, "GET", "/mangoman/code/oc/session", "127.0.0.1:4141", nil, ""); w.Code != 401 || gotPath != "" {
		t.Fatalf("no token: %d, reached %q", w.Code, gotPath)
	}
	if w := call(h, "GET", "/mangoman/code/info", "127.0.0.1:4141", authz, ""); !strings.Contains(w.Body.String(), `"attached": true`) && !strings.Contains(w.Body.String(), `"attached":true`) {
		t.Fatalf("info: %s", w.Body)
	}
	// Not a Guardian project: no ship.
	if w := call(h, "POST", "/mangoman/code/ship", "127.0.0.1:4141", authz, ""); w.Code != 409 {
		t.Fatalf("ship without Guardian: %d", w.Code)
	}
	call(h, "DELETE", "/mangoman/code/attach", "127.0.0.1:4141", authz, "")
	if w := call(h, "GET", "/mangoman/code/oc/session", "127.0.0.1:4141", authz, ""); w.Code != 503 {
		t.Fatalf("after detach: %d", w.Code)
	}
}
