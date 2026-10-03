package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// session runs a server and returns a function that sends one message and,
// for requests, reads the response.
func session(t *testing.T, s *Server) func(msg string) map[string]any {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Serve(ctx, inR, outW); outW.Close(); close(done) }()
	t.Cleanup(func() { inW.Close(); cancel(); <-done })
	rd := bufio.NewReader(outR)
	return func(msg string) map[string]any {
		if _, err := io.WriteString(inW, msg+"\n"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(msg, `"id"`) {
			return nil
		}
		line, err := rd.ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("bad response %q", line)
		}
		return m
	}
}

// fakeRouter records the last chat request and answers it.
func fakeRouter(t *testing.T, status int) (*httptest.Server, *map[string]any) {
	var last map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/mangoman/overview" {
			io.WriteString(w, `{"providers":[{"name":"Groq","status":"connected"},{"name":"NVIDIA","status":"not_connected"}],"models":[{"state":"ready"},{"state":"ready"},{"state":"rate_limited"}]}`)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&last)
		if status != 200 {
			w.Header().Set("Retry-After", "42")
			w.WriteHeader(status)
			io.WriteString(w, `{"error":{"message":"all free candidates are rate limited"}}`)
			return
		}
		w.Header().Set("X-MangoMan-Provider", "groq")
		w.Header().Set("X-MangoMan-Model", "gpt-oss-120b")
		io.WriteString(w, `{"choices":[{"message":{"content":"Here are the tests."},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &last
}

func project(t *testing.T) string {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc Add(a, b int) int { return a + b }\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=1"), 0o644)
	os.WriteFile(filepath.Join(dir, "big.txt"), []byte(strings.Repeat("x", maxFileBytes+1)), 0o644)
	os.WriteFile(filepath.Join(dir, "bin.dat"), []byte{0, 1, 2, 3}, 0o644)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "private.txt"), []byte("private"), 0o644)
	_ = os.Symlink(filepath.Join(outside, "private.txt"), filepath.Join(dir, "link.txt"))
	return dir
}

func TestProtocol(t *testing.T) {
	srv, _ := fakeRouter(t, 200)
	a := &Assist{BaseURL: srv.URL, Token: "tok", Root: project(t)}
	send := session(t, &Server{Name: "mangoman", Version: "t", Instructions: Instructions, Tools: a.Tools()})

	init := send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	res := init["result"].(map[string]any)
	if res["protocolVersion"] != "2025-06-18" || res["serverInfo"].(map[string]any)["name"] != "mangoman" ||
		res["capabilities"].(map[string]any)["tools"] == nil || !strings.Contains(res["instructions"].(string), "free_ask") {
		t.Fatalf("initialize %v", init)
	}
	if r := send(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`); r["result"].(map[string]any)["protocolVersion"] != supported[0] {
		t.Fatalf("unknown version should get our latest: %v", r)
	}
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)

	list := send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	tools := list["result"].(map[string]any)["tools"].([]any)
	names := []string{}
	for _, x := range tools {
		tl := x.(map[string]any)
		names = append(names, tl["name"].(string))
		if tl["inputSchema"].(map[string]any)["type"] != "object" {
			t.Fatalf("schema %v", tl)
		}
	}
	if strings.Join(names, ",") != "free_ask,free_review,free_status" {
		t.Fatalf("tools %v", names)
	}

	if r := send(`{"jsonrpc":"2.0","id":3,"method":"nope"}`); r["error"].(map[string]any)["code"].(float64) != -32601 {
		t.Fatalf("unknown method %v", r)
	}
	if r := send(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nope","arguments":{}}}`); r["error"] == nil {
		t.Fatalf("unknown tool %v", r)
	}
	if r := send(`{"jsonrpc":"2.0","id":"p","method":"ping"}`); r["id"] != "p" || r["result"] == nil {
		t.Fatalf("ping %v", r)
	}
	st := send(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"free_status","arguments":{}}}`)
	text := st["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "Connected: Groq.") || !strings.Contains(text, "2 free models ready, 1 temporarily") {
		t.Fatalf("status %q", text)
	}
}

func callText(t *testing.T, send func(string) map[string]any, id int, name, args string) (string, bool) {
	t.Helper()
	r := send(`{"jsonrpc":"2.0","id":` + strings.Repeat("9", id) + `,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`)
	res := r["result"].(map[string]any)
	return res["content"].([]any)[0].(map[string]any)["text"].(string), res["isError"].(bool)
}

func TestFreeAskFilesAndGuards(t *testing.T) {
	srv, last := fakeRouter(t, 200)
	a := &Assist{BaseURL: srv.URL, Token: "tok", Root: project(t)}
	send := session(t, &Server{Name: "m", Tools: a.Tools()})

	text, isErr := callText(t, send, 1, "free_ask", `{"prompt":"Write tests for Add.","files":["main.go",".env","../etc/passwd","link.txt","big.txt","bin.dat"],"task":"coder"}`)
	if isErr || !strings.Contains(text, "Here are the tests.") || !strings.Contains(text, "answered by groq/gpt-oss-120b") {
		t.Fatalf("answer %q", text)
	}
	for _, want := range []string{".env: looks like a secrets file", "outside the project", "link.txt: outside the project", "over the 200 KB limit", "binary file"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing skip note %q in %q", want, text)
		}
	}
	got := *last
	if got["model"] != "free/coder" {
		t.Fatalf("model %v", got["model"])
	}
	user := got["messages"].([]any)[0].(map[string]any)["content"].(string)
	if !strings.Contains(user, "--- file: main.go ---") || strings.Contains(user, "SECRET") || strings.Contains(user, "private") {
		t.Fatalf("sent content %q", user)
	}

	if text, isErr := callText(t, send, 2, "free_ask", `{"prompt":""}`); !isErr || !strings.Contains(text, "prompt is required") {
		t.Fatalf("empty prompt %q", text)
	}
	if text, isErr := callText(t, send, 3, "free_ask", `{"prompt":"x","files":[".env"]}`); !isErr || !strings.Contains(text, "no files could be sent") {
		t.Fatalf("only secret files %q", text)
	}
	callText(t, send, 4, "free_ask", `{"prompt":"x","model":"group/coding"}`)
	if (*last)["model"] != "group/coding" {
		t.Fatalf("explicit model %v", (*last)["model"])
	}
}

func TestFreeAskRouterDownAndExhausted(t *testing.T) {
	a := &Assist{BaseURL: "http://127.0.0.1:1", Token: "tok", Root: t.TempDir()}
	send := session(t, &Server{Name: "m", Tools: a.Tools()})
	if text, isErr := callText(t, send, 1, "free_ask", `{"prompt":"hi"}`); !isErr || !strings.Contains(text, "mangoman serve") {
		t.Fatalf("router down %q", text)
	}
	srv, _ := fakeRouter(t, 429)
	a2 := &Assist{BaseURL: srv.URL, Token: "tok", Root: t.TempDir()}
	send2 := session(t, &Server{Name: "m", Tools: a2.Tools()})
	if text, isErr := callText(t, send2, 1, "free_ask", `{"prompt":"hi"}`); !isErr || !strings.Contains(text, "about 42 s") {
		t.Fatalf("exhausted %q", text)
	}
}

func TestFreeReview(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("A=1\n"), 0o644)
	run("add", ".")
	run("commit", "-qm", "init")
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\nvar Key = \"sk-live\"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("A=SUPERSECRET\n"), 0o644)

	srv, last := fakeRouter(t, 200)
	a := &Assist{BaseURL: srv.URL, Token: "tok", Root: dir}
	send := session(t, &Server{Name: "m", Tools: a.Tools()})
	text, isErr := callText(t, send, 1, "free_review", `{"focus":"secrets"}`)
	if isErr || !strings.Contains(text, "reviewed by groq/gpt-oss-120b") {
		t.Fatalf("review %q", text)
	}
	user := (*last)["messages"].([]any)[1].(map[string]any)["content"].(string)
	if !strings.Contains(user, `var Key = "sk-live"`) || strings.Contains(user, "SUPERSECRET") || !strings.Contains(user, "secrets") {
		t.Fatalf("diff sent %q", user)
	}
	if text, isErr := callText(t, send, 2, "free_review", `{"ref":"--output=/tmp/x"}`); !isErr || !strings.Contains(text, "ref must be") {
		t.Fatalf("option injection %q", text)
	}
}

func TestFreeStatusWrongTokenIsAnError(t *testing.T) {
	srv, _ := fakeRouter(t, 200)
	a := &Assist{BaseURL: srv.URL, Token: "wrong"}
	if _, err := a.freeStatus(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("got %v", err)
	}
}
