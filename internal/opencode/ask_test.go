package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// The test program is also a stand-in OpenCode: run with
// MANGOMAN_FAKE_OPENCODE set, it answers as OpenCode's run --format json
// does (ok: one answer from the prompt; limit: OpenCode's log line at the
// free limit, then nothing; fail: an error event; quiet: nothing at all).
func TestMain(m *testing.M) {
	if mode := os.Getenv("MANGOMAN_FAKE_OPENCODE"); mode != "" {
		fakeOpenCode(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeOpenCode(mode string) {
	in, _ := io.ReadAll(os.Stdin)
	var cfg struct {
		EnabledProviders []string `json:"enabled_providers"`
		Agent            map[string]struct {
			Prompt     string            `json:"prompt"`
			Permission map[string]string `json:"permission"`
			Disable    bool              `json:"disable"`
		} `json:"agent"`
	}
	_ = json.Unmarshal([]byte(os.Getenv("OPENCODE_CONFIG_CONTENT")), &cfg)
	a := cfg.Agent[askAgent]
	ok := strings.Join(os.Args[1:], " ") == "run --format json --agent "+askAgent && len(cfg.EnabledProviders) == 1 &&
		cfg.EnabledProviders[0] == "opencode" && a.Permission["*"] == "deny" && a.Permission["read"] == "allow" && a.Permission["bash"] == "" && a.Permission["edit"] == "" && cfg.Agent["title"].Disable
	switch {
	case !ok:
		fmt.Println(`{"type":"error","error":{"name":"BadCall","data":{"message":"called wrongly: ` + strings.Join(os.Args[1:], " ") + `"}}}`)
	case mode == "ok":
		fmt.Println(`{"type":"step_start"}`)
		b, _ := json.Marshal(map[string]any{"type": "text", "part": map[string]string{"text": "answer to: " + string(in) + " (as " + a.Prompt + ")"}})
		fmt.Println(string(b))
	case mode == "limit":
		fmt.Fprintln(os.Stderr, `timestamp=2026-10-27T18:27:36.068Z level=ERROR run=1776899a message="stream error" providerID=opencode modelID=big-pickle error.error="AI_APICallError: Free usage limit reached. Free models work in OpenCode (https://opencode.ai)."`)
		time.Sleep(time.Minute) // as OpenCode: it waits for the limit to end
	case mode == "slow":
		fmt.Println(`{"type":"text","part":{"text":"half an ans"}}`)
		time.Sleep(time.Minute)
	case mode == "fail":
		fmt.Println(`{"type":"error","error":{"name":"APIError","data":{"message":"Upstream request failed: Model is unavailable."}}}`)
	}
}

func TestAsk(t *testing.T) {
	bin, _ := os.Executable()
	ctx := context.Background()
	t.Setenv("MANGOMAN_FAKE_OPENCODE", "ok")
	long := strings.Repeat("x", 40000) // longer than a Windows command line
	got, err := Ask(ctx, bin, "Answer in JSON.", "describe the sheet "+long)
	if err != nil || !strings.HasPrefix(got, "answer to: describe the sheet x") || !strings.HasSuffix(got, "(as Answer in JSON.)") || !strings.Contains(got, long) {
		t.Fatalf("answer: %.120q %v", got, err)
	}
	t.Setenv("MANGOMAN_FAKE_OPENCODE", "limit")
	start := time.Now()
	if _, err := Ask(ctx, bin, "x", "y"); !errors.Is(err, ErrFreeLimit) || !strings.Contains(err.Error(), "come back at") {
		t.Fatalf("the free limit: %v", err)
	}
	if took := time.Since(start); took > 15*time.Second {
		t.Fatalf("at the free limit OpenCode keeps waiting; Ask must stop it at once, took %s", took)
	}
	t.Setenv("MANGOMAN_FAKE_OPENCODE", "fail")
	if _, err := Ask(ctx, bin, "x", "y"); err == nil || !strings.Contains(err.Error(), "Model is unavailable") || errors.Is(err, ErrFreeLimit) || !Says(err) {
		t.Fatalf("an error from OpenCode, worded for the user: %v", err)
	}
	t.Setenv("MANGOMAN_FAKE_OPENCODE", "quiet")
	if _, err := Ask(ctx, bin, "x", "y"); err == nil || !strings.Contains(err.Error(), "no answer") {
		t.Fatalf("no answer: %v", err)
	}
	if _, err := Ask(ctx, "/no/such/opencode", "x", "y"); err == nil || !strings.Contains(err.Error(), "Get ready") || !Says(err) {
		t.Fatalf("no OpenCode: %v", err)
	}
	// Cut off by the caller's time limit: no half answer, and OpenCode is stopped.
	t.Setenv("MANGOMAN_FAKE_OPENCODE", "slow")
	short, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	start = time.Now()
	if got, err := Ask(short, bin, "x", "y"); got != "" || err == nil || !strings.Contains(err.Error(), "in time") || time.Since(start) > 15*time.Second {
		t.Fatalf("a cut-off run: %q %v after %s", got, err, time.Since(start))
	}
}

// The free limit is caught from OpenCode's own log line only, never from
// words the model writes, and says when it comes back.
func TestFreeLimit(t *testing.T) {
	for line, want := range map[string]bool{
		// From OpenCode's real service (engine-check, 27 Oct 18:27 UTC).
		`timestamp=2026-10-27T18:27:36.068Z level=ERROR run=1776899a message="stream error" providerID=opencode modelID=big-pickle session.id=ses_e8161e0cfffeT4YbAa6oDeV552 small=false agent=build mode=primary error.error="AI_APICallError: Free usage limit reached. Free models work in OpenCode (https://opencode.ai)."`: true,
		// As OpenCode 1.18.34 logs it (seen with a stand-in answering as OpenCode's service does).
		`timestamp=2026-10-27T17:56:53.511Z level=ERROR run=211be3c9 message="stream error" providerID=opencode modelID=big-pickle session.id=ses_e8 small=false agent=build mode=primary error.error="AI_APICallError: Free usage limit reached."`:    true,
		`timestamp=2026-10-27T17:56:53.511Z level=ERROR run=211be3c9 message="stream error" providerID=opencode modelID=big-pickle error.error="AI_APICallError: Rate limit exceeded. Please try again later."`:                                        true,
		`timestamp=2026-10-27T17:56:58.968Z level=ERROR run=211be3c9 message="stream error" providerID=opencode modelID=big-pickle small=true agent=title error.error="AI_RetryError: Failed after 3 attempts. Last error: Free usage limit reached."`: true,
		// A provider's own limit that OpenCode's service passes on, and another
		// provider's limit, are not OpenCode's free limit.
		`timestamp=2026-10-27T17:56:53.511Z level=ERROR run=211be3c9 message="stream error" providerID=opencode modelID=mimo error.error="AI_APICallError: Error from provider (Xiaomi): Rate limit exceeded. Please try again later."`: false,
		`timestamp=2026-10-27T17:56:53.511Z level=ERROR run=211be3c9 message="stream error" providerID=groq modelID=x error.error="AI_APICallError: Rate limit exceeded."`:                                                              false,
		"Our plans: the free usage limit is 3 projects; pick Pro for more.": false,
		`The error said: "FreeUsageLimitError"`:                             false,
		`timestamp=2026-10-28T00:15:02.1Z level=ERROR run=ab12 message="stream error" error="Upstream request failed: Model is unavailable."`: false,
	} {
		if got := FreeLimitLine.MatchString("Writing the page...\n" + line + "\n"); got != want {
			t.Errorf("%q: %v, want %v", line, got, want)
		}
	}
	// 9:00 PM UTC: midnight UTC is 3 hours away.
	now := time.Date(2026, 10, 27, 21, 0, 0, 0, time.UTC)
	if err := FreeLimit(now); !strings.Contains(err.Error(), now.Add(3*time.Hour).Local().Format("3:04 PM")) {
		t.Fatalf("reset time: %v", err)
	}
}
