package main

import (
	"encoding/json"
	"testing"

	"github.com/pusyc74-prog/mangoman/internal/config"
)

func TestOpenCodeConfig(t *testing.T) {
	cfg := &config.Config{Port: 4141}
	var c map[string]any
	// Interactive: every tool stays, and the skill rule is added.
	_ = json.Unmarshal([]byte(openCodeConfig(cfg, "free/coder", "/x/mangoman-rules.md", modeTUI)), &c)
	if c["tools"] != nil {
		t.Fatalf("an interactive session keeps every tool: %v", c["tools"])
	}
	if in, _ := c["instructions"].([]any); len(in) != 1 || in[0] != "/x/mangoman-rules.md" {
		t.Fatalf("skill rule not added: %v", c["instructions"])
	}
	// Unattended: unused tools off; no packs, no rule.
	c = nil
	_ = json.Unmarshal([]byte(openCodeConfig(cfg, "free/coder", "", modeRun)), &c)
	tools, _ := c["tools"].(map[string]any)
	for _, name := range []string{"question", "task", "todowrite", "webfetch"} {
		if tools[name] != false {
			t.Fatalf("%s should be off in an unattended run: %v", name, tools)
		}
	}
	if c["instructions"] != nil {
		t.Fatal("no rule without packs")
	}
}

func TestOpenCodeConfigUIAsksFirst(t *testing.T) {
	var c struct {
		Permission struct {
			Bash map[string]string `json:"bash"`
		} `json:"permission"`
		Tools map[string]bool `json:"tools"`
	}
	_ = json.Unmarshal([]byte(openCodeConfig(&config.Config{Port: 4141}, "free/coder", "", modeUI)), &c)
	if c.Permission.Bash["*"] != "ask" || c.Permission.Bash["git status*"] != "allow" {
		t.Fatalf("the coding screen should ask before commands, except reading ones: %v", c.Permission.Bash)
	}
	if c.Tools != nil {
		t.Fatal("the coding screen keeps every tool, including questions")
	}
}
