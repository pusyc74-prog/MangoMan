package ingress

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/core"
)

// The coding workspace (mangoman code --ui): OpenCode runs as a local
// server, started and owned by the mangoman code process, which attaches it
// here. The dashboard's coding screen talks to it only through this proxy,
// so the browser never holds OpenCode's password and every call passes the
// router's own token, host and origin checks.

// codeBackend is the attached OpenCode server.
type codeBackend struct {
	id       string // given at attach; only the same id can detach it
	url      *url.URL
	password string
	dir      string // the folder OpenCode works in
	shipDir  string // the Guardian project to ship from ("" when there is none)
	proxy    *httputil.ReverseProxy
}

type codeState struct {
	mu sync.Mutex
	b  *codeBackend
}

// CodeInfo is what the coding screen needs to know about the workspace.
type CodeInfo struct {
	Attached bool   `json:"attached"`
	Dir      string `json:"dir,omitempty"`
	CanShip  bool   `json:"can_ship,omitempty"`
	LANIP    string `json:"lan_ip,omitempty"` // for opening a preview on a phone on the same Wi-Fi
}

func (s *Server) codeRoutes(mux *http.ServeMux) {
	mux.Handle("POST /mangoman/code/attach", s.auth(http.HandlerFunc(s.codeAttach)))
	mux.Handle("DELETE /mangoman/code/attach", s.auth(http.HandlerFunc(s.codeDetach)))
	mux.Handle("GET /mangoman/code/info", s.auth(http.HandlerFunc(s.codeInfo)))
	mux.Handle("POST /mangoman/code/ship", s.auth(http.HandlerFunc(s.codeShip)))
	mux.Handle("/mangoman/code/oc/", s.auth(http.HandlerFunc(s.codeProxy)))
}

func (s *Server) codeAttach(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL      string `json:"url"`
		Password string `json:"password"`
		Dir      string `json:"dir"`
		ShipDir  string `json:"ship_dir,omitempty"`
	}
	if err := readJSON(r, &in); err != nil {
		core.WriteError(w, http.StatusBadRequest, "bad_request", "send {\"url\", \"password\", \"dir\"}")
		return
	}
	u, err := url.Parse(in.URL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" {
		core.WriteError(w, http.StatusBadRequest, "bad_url", "the OpenCode server must be on http://127.0.0.1:<port>")
		return
	}
	b := &codeBackend{id: config.NewToken(), url: u, password: in.Password, dir: in.Dir, shipDir: in.ShipDir}
	b.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(u)
			pr.Out.URL.Path = "/" + strings.TrimPrefix(pr.In.URL.Path, "/mangoman/code/oc/")
			pr.Out.URL.RawPath = ""
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Origin")
			pr.Out.SetBasicAuth("opencode", b.password)
		},
		FlushInterval: -1, // the event stream must reach the page as it happens
	}
	s.code.mu.Lock()
	s.code.b = b
	s.code.mu.Unlock()
	writeJSON(w, map[string]string{"id": b.id})
}

// codeDetach closes the workspace only if it is still the one that id
// attached: a second mangoman code --ui replaces the first, and the first
// closing must not take the second down with it.
func (s *Server) codeDetach(w http.ResponseWriter, r *http.Request) {
	s.code.mu.Lock()
	if s.code.b != nil && s.code.b.id == r.URL.Query().Get("id") {
		s.code.b = nil
	}
	s.code.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) backend() *codeBackend {
	s.code.mu.Lock()
	defer s.code.mu.Unlock()
	return s.code.b
}

func (s *Server) codeInfo(w http.ResponseWriter, _ *http.Request) {
	b := s.backend()
	if b == nil {
		writeJSON(w, CodeInfo{})
		return
	}
	writeJSON(w, CodeInfo{Attached: true, Dir: b.dir, CanShip: b.shipDir != "", LANIP: lanIP()})
}

func (s *Server) codeProxy(w http.ResponseWriter, r *http.Request) {
	b := s.backend()
	if b == nil {
		core.WriteError(w, http.StatusServiceUnavailable, "no_workspace", "no coding workspace is open: run mangoman code --ui in your project")
		return
	}
	b.proxy.ServeHTTP(w, r)
}

// codeShip queues the workspace's work for production through Guardian:
// QA tests it in dev, then the owner approves.
func (s *Server) codeShip(w http.ResponseWriter, r *http.Request) {
	b := s.backend()
	if b == nil || b.shipDir == "" {
		core.WriteError(w, http.StatusConflict, "cannot_ship", "this folder is not a Guardian project; ship it your usual way")
		return
	}
	self, err := os.Executable()
	if err != nil {
		core.WriteError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, self, "guardian", "ship")
	cmd.Dir = b.shipDir
	out, err := cmd.CombinedOutput()
	res := map[string]any{"ok": err == nil, "output": tail(string(out), 4000)}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

// lanIP is this computer's address on the local network, for opening a
// preview on a phone on the same Wi-Fi ("" when there is none).
func lanIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil && n.IP.IsPrivate() {
			return n.IP.String()
		}
	}
	return ""
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
