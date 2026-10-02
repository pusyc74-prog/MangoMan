package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Assist holds what the assist tools need to reach the local router.
type Assist struct {
	BaseURL string // e.g. http://127.0.0.1:4141
	Token   string
	Root    string // project directory; files outside it are refused
	HTTP    *http.Client
}

const (
	maxFileBytes  = 200 << 10
	maxTotalBytes = 600 << 10
)

// secretish are files never sent to a free provider.
var secretish = []string{".env", ".pem", ".key", ".p12", ".pfx", "id_rsa", "id_ed25519", ".npmrc", ".netrc",
	"credentials", ".kube/config", ".aws/", ".ssh/", "secrets."}

// Instructions tell the main model when to use assist mode.
const Instructions = `MangoMan routes work to free AI models (Groq, Cerebras, NVIDIA, OpenRouter and others) running through the user's own free accounts.
Use free_ask for self-contained routine work that does not need your full judgment: drafting unit tests, docstrings and docs, summarising long files or logs, boilerplate, simple refactors, translations, explaining code. Review what comes back before using it.
The free model cannot see this conversation: put everything it needs in the prompt, or pass file paths in files.
Do not send secrets. Files that look like credentials are refused.
Use free_review for a second opinion on the current git changes. Use free_status to see what free capacity is left.`

// Tools returns the assist tools.
func (a *Assist) Tools() []Tool {
	return []Tool{
		{
			Name:  "free_ask",
			Title: "Ask a free model",
			Description: "Send a self-contained task to a free model through MangoMan and get its answer. " +
				"Good for routine work (tests, docs, summaries, boilerplate, explanations) to save paid usage. " +
				"The free model sees only the prompt and the listed files, not this conversation.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{"type": "string", "description": "The complete task, with all needed context."},
					"files": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
						"description": "Paths of project files to include (relative to the project root, 200 KB each at most)."},
					"task": map[string]any{"type": "string", "enum": []string{"auto", "coder", "writer", "fast", "long"},
						"description": "Kind of work, which picks the free model group. Default auto."},
					"model": map[string]any{"type": "string",
						"description": "Optional exact model: a catalogue name, strict/<model> or group/<name>."},
					"max_tokens": map[string]any{"type": "integer", "minimum": 1, "maximum": 32000},
				},
				"required": []string{"prompt"},
			},
			Call: a.freeAsk,
		},
		{
			Name:  "free_review",
			Title: "Review git changes with a free model",
			Description: "Ask a free model to review the current uncommitted git changes (or the changes since a ref) " +
				"for bugs, missed edge cases and leaked secrets. Returns findings to check, not edits.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"ref":   map[string]any{"type": "string", "description": "Compare against this ref (e.g. main). Default: uncommitted changes."},
					"focus": map[string]any{"type": "string", "description": "Optional: what to pay attention to."},
				},
			},
			Call: a.freeReview,
		},
		{
			Name:        "free_status",
			Title:       "Free capacity status",
			Description: "Show which free providers are connected and how many free models are ready right now.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Call:        a.freeStatus,
		},
	}
}

func (a *Assist) client() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return &http.Client{Timeout: 4 * time.Minute}
}

// readProjectFile reads a file under Root, refusing escapes and secrets.
func (a *Assist) readProjectFile(p string) (string, string, error) {
	root, err := filepath.EvalSymlinks(a.Root)
	if err != nil {
		return "", "", fmt.Errorf("project root unreadable: %w", err)
	}
	full := p
	if !filepath.IsAbs(full) {
		full = filepath.Join(root, p)
	}
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", "", fmt.Errorf("%s: not found", p)
	}
	rel, err := filepath.Rel(root, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("%s: outside the project, not sent", p)
	}
	low := strings.ToLower(filepath.ToSlash(rel))
	for _, s := range secretish {
		if strings.Contains(low, s) {
			return "", "", fmt.Errorf("%s: looks like a secrets file, not sent to a free provider", p)
		}
	}
	st, err := os.Stat(real)
	if err != nil || st.IsDir() {
		return "", "", fmt.Errorf("%s: not a file", p)
	}
	if st.Size() > maxFileBytes {
		return "", "", fmt.Errorf("%s: %d KB is over the 200 KB limit; send the relevant part in the prompt", p, st.Size()>>10)
	}
	data, err := os.ReadFile(real)
	if err != nil {
		return "", "", err
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return "", "", fmt.Errorf("%s: binary file, not sent", p)
	}
	return filepath.ToSlash(rel), string(data), nil
}

// chat sends one request to the router and returns the answer and who gave it.
func (a *Assist) chat(ctx context.Context, model, system, user string, maxTokens int) (string, string, error) {
	msgs := []map[string]string{}
	if system != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": system})
	}
	msgs = append(msgs, map[string]string{"role": "user", "content": user})
	body := map[string]any{"model": model, "messages": msgs}
	if maxTokens > 0 {
		body["max_tokens"] = maxTokens
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.BaseURL, "/")+"/v1/chat/completions", bytes.NewReader(b))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client().Do(req)
	if err != nil {
		return "", "", errors.New("MangoMan is not running on this computer: ask the user to run `mangoman serve`")
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		msg := e.Error.Message
		if msg == "" {
			msg = strings.TrimSpace(string(data))
		}
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			msg += fmt.Sprintf(" (free capacity returns in about %s s)", ra)
		}
		return "", "", fmt.Errorf("free models unavailable (HTTP %d): %s", resp.StatusCode, msg)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &out); err != nil || len(out.Choices) == 0 {
		return "", "", errors.New("the free model sent an unreadable answer")
	}
	who := resp.Header.Get("X-MangoMan-Provider") + "/" + resp.Header.Get("X-MangoMan-Model")
	ans := out.Choices[0].Message.Content
	if out.Choices[0].FinishReason == "length" {
		ans += "\n\n[cut off at the length limit]"
	}
	if g := resp.Header.Get("X-MangoMan-Guard"); g != "" {
		who += ", flagged: " + g
	}
	return ans, who, nil
}

func (a *Assist) freeAsk(ctx context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Prompt    string   `json:"prompt"`
		Files     []string `json:"files"`
		Task      string   `json:"task"`
		Model     string   `json:"model"`
		MaxTokens int      `json:"max_tokens"`
	}
	if err := json.Unmarshal(raw, &in); err != nil || strings.TrimSpace(in.Prompt) == "" {
		return "", errors.New("prompt is required")
	}
	var b strings.Builder
	b.WriteString(in.Prompt)
	total := 0
	var skipped []string
	for _, f := range in.Files {
		rel, text, err := a.readProjectFile(f)
		if err != nil {
			skipped = append(skipped, err.Error())
			continue
		}
		if total+len(text) > maxTotalBytes {
			skipped = append(skipped, rel+": skipped, over the 600 KB total")
			continue
		}
		total += len(text)
		fmt.Fprintf(&b, "\n\n--- file: %s ---\n%s", rel, text)
	}
	if len(in.Files) > 0 && len(skipped) == len(in.Files) {
		return "", errors.New("no files could be sent: " + strings.Join(skipped, "; "))
	}
	model := in.Model
	if model == "" {
		switch in.Task {
		case "coder", "writer", "fast", "long":
			model = "free/" + in.Task
		default:
			model = "free/auto"
		}
	}
	ans, who, err := a.chat(ctx, model, "", b.String(), in.MaxTokens)
	if err != nil {
		return "", err
	}
	note := ""
	if len(skipped) > 0 {
		note = "\nNot sent: " + strings.Join(skipped, "; ")
	}
	return fmt.Sprintf("%s\n\n[answered by %s (free) via MangoMan]%s", ans, who, note), nil
}

func (a *Assist) freeReview(ctx context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Ref   string `json:"ref"`
		Focus string `json:"focus"`
	}
	_ = json.Unmarshal(raw, &in)
	if strings.HasPrefix(in.Ref, "-") {
		return "", errors.New("ref must be a branch, tag or commit")
	}
	args := []string{"diff", "--no-color", "--no-ext-diff"}
	if in.Ref != "" {
		args = append(args, in.Ref)
	} else {
		args = append(args, "HEAD")
	}
	args = append(args, "--", ".")
	for _, s := range []string{".env", "*.pem", "*.key", "*secret*", "*credentials*"} {
		args = append(args, ":(exclude,glob)**/"+s)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = a.Root
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git diff failed (is this a git repository?): %v", err)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return "No changes to review.", nil
	}
	if len(out) > maxTotalBytes {
		return "", fmt.Errorf("the diff is %d KB, over the 600 KB limit; review a smaller set of changes", len(out)>>10)
	}
	prompt := "Review this git diff. List concrete problems only: bugs, missed edge cases, security issues, " +
		"leaked secrets or keys, and risky changes. For each, give the file, the line and a one-line fix. " +
		"If it looks fine, say so in one line.\n"
	if in.Focus != "" {
		prompt += "Pay special attention to: " + in.Focus + "\n"
	}
	ans, who, err := a.chat(ctx, "free/coder", "You are a careful senior code reviewer.", prompt+"\n"+string(out), 4000)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s\n\n[reviewed by %s (free) via MangoMan; check each finding before acting]", ans, who), nil
}

func (a *Assist) freeStatus(ctx context.Context, _ json.RawMessage) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(a.BaseURL, "/")+"/mangoman/overview", nil)
	req.Header.Set("Authorization", "Bearer "+a.Token)
	resp, err := a.client().Do(req)
	if err != nil {
		return "", errors.New("MangoMan is not running on this computer: ask the user to run `mangoman serve`")
	}
	defer resp.Body.Close()
	var ov struct {
		Providers []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"providers"`
		Models []struct {
			State string `json:"state"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ov); err != nil {
		return "", errors.New("could not read MangoMan's status")
	}
	var on []string
	for _, p := range ov.Providers {
		if p.Status == "connected" || p.Status == "running" {
			on = append(on, p.Name)
		}
	}
	ready, limited := 0, 0
	for _, m := range ov.Models {
		switch m.State {
		case "ready":
			ready++
		case "rate_limited", "cooling_down":
			limited++
		}
	}
	if len(on) == 0 {
		return "No free providers are connected. Ask the user to run `mangoman setup`.", nil
	}
	return fmt.Sprintf("Connected: %s. %d free models ready, %d temporarily used up or cooling down.",
		strings.Join(on, ", "), ready, limited), nil
}
