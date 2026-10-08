package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/pusyc74-prog/mangoman/internal/config"
)

func TestOpenCodeConfig(t *testing.T) {
	// OpenCode keeps its own tools and instructions (measured better on 8 Oct).
	for _, mode := range []int{modeTUI, modeRun} {
		var c map[string]any
		_ = json.Unmarshal([]byte(openCodeConfig(&config.Config{Port: 4141}, "free/coder", mode)), &c)
		if c["tools"] != nil || c["instructions"] != nil || c["model"] != "mangoman/free/coder" {
			t.Fatalf("mode %d: %v", mode, c)
		}
	}
}

func TestOpenCodeConfigUIAsksFirst(t *testing.T) {
	var c struct {
		Permission struct {
			Bash map[string]string `json:"bash"`
		} `json:"permission"`
		Tools map[string]bool `json:"tools"`
	}
	_ = json.Unmarshal([]byte(openCodeConfig(&config.Config{Port: 4141}, "free/coder", modeUI)), &c)
	if c.Permission.Bash["*"] != "ask" || c.Permission.Bash["git status*"] != "allow" {
		t.Fatalf("the coding screen should ask before commands, except reading ones: %v", c.Permission.Bash)
	}
	if c.Tools != nil {
		t.Fatal("the coding screen keeps every tool, including questions")
	}
}

func TestResumeArgs(t *testing.T) {
	got := resumeArgs([]string{"run", "--auto", "--dir", "/w", "-m", "mangoman/free/coder", "write the ads"})
	want := []string{"run", "--continue", "--auto", "--dir", "/w", "-m", "mangoman/free/coder", resumeNote}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
}

func TestTailWriterKeepsTheEnd(t *testing.T) {
	var tw tailWriter
	_, _ = tw.Write([]byte(strings.Repeat("x", 9000)))
	_, _ = tw.Write([]byte("upstream_stream_error"))
	if s := tw.String(); len(s) != 8<<10 || !strings.HasSuffix(s, "upstream_stream_error") {
		t.Fatalf("kept %d bytes", len(s))
	}
}

func TestCommandRunning(t *testing.T) {
	if _, err := exec.LookPath("pgrep"); err != nil {
		t.Skip("no pgrep")
	}
	if commandRunning(os.Getpid()) {
		t.Fatal("no command yet")
	}
	c := exec.Command("sleep", "5")
	if err := c.Start(); err != nil {
		t.Skip(err)
	}
	defer func() { _ = c.Process.Kill(); _ = c.Wait() }()
	if !commandRunning(os.Getpid()) {
		t.Fatal("a running command was not seen")
	}
	if !languageServer("node /usr/lib/node_modules/typescript-language-server/lib/cli.mjs --stdio") ||
		!languageServer("node /x/vscode-json-languageserver --stdio") || !languageServer("node /x/pyright-langserver --stdio") ||
		languageServer("npm install") || languageServer("python3 scripts/build_ads.py ads.json") {
		t.Fatal("language server names")
	}
}
