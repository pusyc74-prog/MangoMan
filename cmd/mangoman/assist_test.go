package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/config"
)

func TestOpenCodeConfig(t *testing.T) {
	// OpenCode keeps its own tools (measured on 8 Oct); instructions only with a rules file.
	for _, mode := range []int{modeTUI, modeRun} {
		var c map[string]any
		_ = json.Unmarshal([]byte(openCodeConfig(&config.Config{Port: 4141}, "free/coder", mode, "", false)), &c)
		if c["tools"] != nil || c["instructions"] != nil || c["model"] != "mangoman/free/coder" {
			t.Fatalf("mode %d: %v", mode, c)
		}
		if p, _ := c["compaction"].(map[string]any); p["prune"] != true {
			t.Fatalf("mode %d: old tool output is not pruned: %v", mode, c["compaction"])
		}
	}
}

func TestCodeRules(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	projects := filepath.Join(home, "MangoMan Projects")
	if err := os.Mkdir(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	if !inProjects(projects) || inProjects(home) {
		t.Fatal("MangoMan Projects not told apart from another folder")
	}
	base := codeRules(false, false, modeUI)
	if !strings.Contains(base, "skill's folder") || !strings.Contains(base, "When you write or change code") || strings.Contains(base, "MangoMan Projects") ||
		strings.Contains(base, "PowerShell") || strings.Contains(base, "Ask before deleting") {
		t.Fatalf("a project folder on Mac or Linux in the coding screen gets the skill folder and code rules only:\n%s", base)
	}
	// Nobody can answer in an unattended run, and only OpenCode's own
	// terminal screen runs commands without asking first.
	if run := codeRules(false, false, modeRun); strings.Contains(run, "ask") || strings.Contains(run, "Ask") {
		t.Fatalf("an unattended run is told to ask:\n%s", run)
	}
	if !strings.Contains(codeRules(false, false, modeTUI), "Ask before deleting") {
		t.Fatal("the terminal screen, which asks before nothing, lacks the ask-before rule")
	}
	all := codeRules(true, true, modeUI)
	for _, want := range []string{"skill's folder", "folder named after it", "agent or bot", `"2>$null"`} {
		if !strings.Contains(all, want) {
			t.Errorf("rules in MangoMan Projects on Windows lack %q:\n%s", want, all)
		}
	}
	for _, c := range []struct {
		goos, shell string
		ps          bool
	}{{"windows", "", true}, {"windows", "powershell", true}, {"windows", `C:\Program Files\PowerShell\7\pwsh.exe`, true},
		{"windows", `C:\Program Files\Git\bin\bash.exe`, false}, {"darwin", "", false}, {"linux", "/bin/bash", false}} {
		if psShell(c.goos, c.shell) != c.ps {
			t.Errorf("psShell(%q, %q) should be %v", c.goos, c.shell, c.ps)
		}
	}
	path, err := writeCodeRules(home, true, modeUI)
	if b, _ := os.ReadFile(path); err != nil || !strings.Contains(string(b), "folder named after it") {
		t.Fatalf("rules file %s: %v", path, err)
	}
	var c struct{ Instructions []string }
	_ = json.Unmarshal([]byte(openCodeConfig(&config.Config{Port: 4141}, "free/coder", modeUI, path, true)), &c)
	if len(c.Instructions) != 1 || c.Instructions[0] != path {
		t.Fatalf("instructions: %v", c.Instructions)
	}
}

func TestOpenCodeConfigUIAsksFirst(t *testing.T) {
	var c struct {
		Permission struct {
			Bash map[string]string `json:"bash"`
		} `json:"permission"`
		Tools map[string]bool `json:"tools"`
	}
	_ = json.Unmarshal([]byte(openCodeConfig(&config.Config{Port: 4141}, "free/coder", modeUI, "", false)), &c)
	if c.Permission.Bash["*"] != "ask" || c.Permission.Bash["git status*"] != "allow" {
		t.Fatalf("the coding screen should ask before commands, except reading ones: %v", c.Permission.Bash)
	}
	// In MangoMan Projects only what deletes, moves, downloads, installs or
	// changes the computer asks.
	c.Permission.Bash = nil
	_ = json.Unmarshal([]byte(openCodeConfig(&config.Config{Port: 4141}, "free/coder", modeUI, "", true)), &c)
	if c.Permission.Bash["*"] != "allow" || c.Permission.Bash["Remove-Item*"] != "ask" || c.Permission.Bash["rm*"] != "ask" || c.Permission.Bash["pip *"] != "ask" {
		t.Fatalf("projects: %v", c.Permission.Bash)
	}
	if c.Tools != nil {
		t.Fatal("the coding screen keeps every tool, including questions")
	}
	// Community skills' scripts ask on a screen and never run unattended;
	// the rule names the real skills folder, so a user's own skill or a
	// project folder named like one is left alone.
	t.Setenv("MANGOMAN_HOME", t.TempDir())
	cd, _ := config.Dir()
	key := func(id string) string { return "**" + filepath.Join(cd, "opencode", "skills", id+"-") + "*" }
	c.Permission.Bash = nil
	_ = json.Unmarshal([]byte(openCodeConfig(&config.Config{Port: 4141}, "free/coder", modeUI, "", true)), &c)
	if c.Permission.Bash[key("ecc")] != "ask" {
		t.Fatalf("community scripts on the coding screen: %v", c.Permission.Bash)
	}
	c.Permission.Bash = nil
	_ = json.Unmarshal([]byte(openCodeConfig(&config.Config{Port: 4141}, "free/coder", modeRun, "", true)), &c)
	if c.Permission.Bash[key("rezvani")] != "deny" || c.Permission.Bash["*"] != "allow" {
		t.Fatalf("community scripts in an unattended run: %v", c.Permission.Bash)
	}
	// OpenCode takes the last matching rule in key order: "*" first, then
	// these, then the agent sandbox's deny rules.
	keys := []string{}
	for k := range c.Permission.Bash {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if keys[0] != "*" || !strings.HasPrefix(keys[1], "**") {
		t.Fatalf("rule order: %v", keys)
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

// On OpenCode's own models: no MangoMan provider, only OpenCode's models,
// and OpenCode picks the model unless one of its own is asked for. The
// permissions are the same as on the switch.
func TestOpenCodeConfigOpenCodeEngine(t *testing.T) {
	t.Setenv("MANGOMAN_HOME", t.TempDir())
	cfg := &config.Config{Port: 4141, Engine: config.EngineOpenCode}
	var c map[string]any
	_ = json.Unmarshal([]byte(openCodeConfig(cfg, "free/coder", modeRun, "", true)), &c)
	if c["provider"] != nil || c["model"] != nil || c["small_model"] != nil || c["permission"] == nil {
		t.Fatalf("OpenCode engine: %v", c)
	}
	if p, _ := c["enabled_providers"].([]any); len(p) != 1 || p[0] != "opencode" {
		t.Fatalf("only OpenCode's own models: %v", c["enabled_providers"])
	}
	_ = json.Unmarshal([]byte(openCodeConfig(cfg, "opencode/big-pickle", modeRun, "", true)), &c)
	if c["model"] != "opencode/big-pickle" {
		t.Fatalf("an OpenCode model asked for: %v", c["model"])
	}
	// A setting MangoMan does not know is the switch, today's behaviour.
	_ = json.Unmarshal([]byte(openCodeConfig(&config.Config{Port: 4141, Engine: "opencod"}, "free/coder", modeRun, "", true)), &c)
	if c["model"] != "mangoman/free/coder" || c["provider"] == nil {
		t.Fatalf("an unknown engine must fall back to the switch: %v", c)
	}
}

// The free limit is caught from OpenCode's own log line only, never from
// words the model writes, and says when it comes back.
func TestFreeLimit(t *testing.T) {
	for line, want := range map[string]bool{
		// As OpenCode 1.18.34 logs it (seen with a stand-in answering as OpenCode's service does).
		`timestamp=2026-10-27T17:56:53.511Z level=ERROR run=211be3c9 message="stream error" providerID=opencode modelID=big-pickle session.id=ses_e8 small=false agent=build mode=primary error.error="AI_APICallError: Free usage limit reached."`:    true,
		`timestamp=2026-10-27T17:56:53.511Z level=ERROR run=211be3c9 message="stream error" providerID=opencode modelID=big-pickle error.error="AI_APICallError: Rate limit exceeded. Please try again later."`:                                        true,
		`timestamp=2026-10-27T17:56:58.968Z level=ERROR run=211be3c9 message="stream error" providerID=opencode modelID=big-pickle small=true agent=title error.error="AI_RetryError: Failed after 3 attempts. Last error: Free usage limit reached."`: true,
		// Another provider's rate limit is not OpenCode's free limit.
		`timestamp=2026-10-27T17:56:53.511Z level=ERROR run=211be3c9 message="stream error" providerID=groq modelID=x error.error="AI_APICallError: Rate limit exceeded."`: false,
		"Our plans: the free usage limit is 3 projects; pick Pro for more.":                                                                                                false,
		`The error said: "FreeUsageLimitError"`: false,
		`timestamp=2026-10-28T00:15:02.1Z level=ERROR run=ab12 message="stream error" error="Upstream request failed: Model is unavailable."`: false,
	} {
		if got := freeLimit.MatchString("Writing the page...\n" + line + "\n"); got != want {
			t.Errorf("%q: %v, want %v", line, got, want)
		}
	}
	// 9:00 PM UTC: midnight UTC is 3 hours away.
	now := time.Date(2026, 10, 27, 21, 0, 0, 0, time.UTC)
	if err := freeLimitError(now); !strings.Contains(err.Error(), now.Add(3*time.Hour).Local().Format("3:04 PM")) {
		t.Fatalf("reset time: %v", err)
	}
}

// watchOpenCode stops a run as soon as OpenCode logs the free limit, and
// leaves a run alone that only prints the words.
func TestWatchOpenCode(t *testing.T) {
	for name, out := range map[string]string{
		"limit": `timestamp=2026-10-27T17:56:53.511Z level=ERROR run=211be3c9 message="stream error" providerID=opencode modelID=big-pickle error.error="AI_APICallError: Rate limit exceeded. Please try again later."` + "\n",
		"words": "The free usage limit on our plan is 3 projects.\n",
	} {
		c := exec.Command("sleep", "30")
		if err := c.Start(); err != nil {
			t.Skip(err)
		}
		var tw tailWriter
		var stalled, limited atomic.Bool
		done := make(chan struct{})
		ended := make(chan struct{})
		go func() { _ = c.Wait(); close(ended) }()
		go watchOpenCode(&tw, c.Process, done, &stalled, &limited)
		_, _ = tw.Write([]byte(out))
		select {
		case <-ended:
		case <-time.After(5 * time.Second):
		}
		close(done)
		_ = c.Process.Kill()
		<-ended
		if limited.Load() != (name == "limit") || stalled.Load() {
			t.Fatalf("%s: limited %v, stalled %v", name, limited.Load(), stalled.Load())
		}
	}
}
