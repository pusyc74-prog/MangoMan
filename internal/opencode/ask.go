package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// OpenCode's own free models count messages per internet connection and
// day, from midnight UTC, and at the limit OpenCode waits for hours and
// offers its own paid plan. MangoMan stops at once and says when they
// come back.

// ErrFreeLimit is OpenCode's free daily limit reached.
var ErrFreeLimit = errors.New("today's free AI messages are used up on this internet connection")

// Said is an error whose message is already worded for the person using
// MangoMan; callers show it as it is.
type Said struct{ Err error }

func (e Said) Error() string { return e.Err.Error() }
func (e Said) Unwrap() error { return e.Err }

// Says reports whether err should be shown as it is: a Said, or the free limit.
func Says(err error) bool {
	var s Said
	return errors.As(err, &s) || errors.Is(err, ErrFreeLimit)
}

// FreeLimit is ErrFreeLimit with when the messages come back, in local time.
func FreeLimit(now time.Time) error {
	reset := now.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	return fmt.Errorf("%w; they come back at %s", ErrFreeLimit, reset.Local().Format("3:04 PM"))
}

// FreeLimitLine is OpenCode's own log line ("timestamp=... level=...") for
// an error from its own models' service at the free daily limit. The line
// carries the error's message only, right after the error's name (also
// when OpenCode gave up after retries): "Rate limit exceeded. Please try
// again later." (what the service sends a logged-out user at that limit,
// read in its code) or "Free usage limit reached" / "Free usage exceeded"
// (what the real service sent, 27 Oct). A provider's own limit passed on
// by the service starts "Error from provider" and is not matched; the
// model's words never start a line so.
var FreeLimitLine = regexp.MustCompile(`(?im)^timestamp=\S+ level=\S+ .*providerID=opencode\b.*\berror(?:\.error)?="AI_\w+: (?:Failed after \d+ attempts\. Last error: )?(?:Rate limit exceeded\. Please try again later|Free usage (?:limit|exceeded))`)

// limitWatch keeps OpenCode's log lines and calls stop as soon as one is
// the free limit's.
type limitWatch struct {
	mu   sync.Mutex
	b    []byte
	seen bool
	stop func()
}

func (w *limitWatch) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.b = append(w.b, p...)
	if !w.seen && FreeLimitLine.Match(w.b) {
		w.seen = true
		w.stop()
	}
	return len(p), nil
}

func (w *limitWatch) hit() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.seen
}

// askAgent is the agent an Ask runs as: no tools, the caller's instructions.
const askAgent = "mangoman-ask"

// AskConfig is OpenCode's inline config for one question: OpenCode's own
// models only, no chat title (a second request), and an agent with the
// given instructions that may use no tool.
func AskConfig(system string) string {
	b, _ := json.Marshal(map[string]any{
		"enabled_providers": []string{"opencode"},
		"share":             "disabled",
		"autoupdate":        false,
		"agent": map[string]any{
			"title": map[string]any{"disable": true},
			askAgent: map[string]any{
				"mode":        "primary",
				"description": "Answers one request for MangoMan",
				"prompt":      system,
				"permission":  map[string]string{"*": "deny"},
			},
		},
	})
	return string(b)
}

// Ask puts one question to OpenCode's own free models through the OpenCode
// program bin and returns the answer: a run in an empty folder, the prompt
// on standard input (a long one would not fit on a Windows command line).
// It fails with ErrFreeLimit (wrapped by FreeLimit) at the free daily limit.
func Ask(ctx context.Context, bin, system, prompt string) (string, error) {
	dir, err := os.MkdirTemp("", "mangoman-ask-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	// At the limit OpenCode keeps waiting for hours: its log line ends the run.
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	cmd := exec.CommandContext(ctx, bin, "run", "--format", "json", "--agent", askAgent)
	Group(cmd)
	cmd.Cancel = func() error { return KillTree(cmd) }
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(prompt)
	var out bytes.Buffer
	logs := &limitWatch{stop: stop}
	cmd.Stdout, cmd.Stderr = &out, logs
	cmd.Env = append(os.Environ(), "OPENCODE_CONFIG_CONTENT="+AskConfig(system),
		"OPENCODE_PRINT_LOGS=1", "OPENCODE_LOG_LEVEL=ERROR", "OPENCODE_DISABLE_AUTOUPDATE=1")
	cmd.WaitDelay = 5 * time.Second
	runErr := cmd.Run()
	if logs.hit() {
		return "", FreeLimit(time.Now())
	}
	var text, failed strings.Builder
	for _, line := range strings.Split(out.String(), "\n") {
		var ev struct {
			Type string `json:"type"`
			Part struct {
				Text string `json:"text"`
			} `json:"part"`
			Error struct {
				Name string `json:"name"`
				Data struct {
					Message string `json:"message"`
				} `json:"data"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "text":
			text.WriteString(ev.Part.Text)
		case "error":
			if failed.Len() == 0 {
				failed.WriteString(strings.TrimSpace(ev.Error.Data.Message + " " + ev.Error.Name))
			}
		}
	}
	switch s := strings.TrimSpace(text.String()); {
	case ctx.Err() != nil: // a cut-off answer is not an answer
		return "", Said{errors.New("the free AI model did not answer in time: try again in a few minutes")}
	case s != "":
		return s, nil
	case failed.Len() > 0:
		return "", Said{fmt.Errorf("the free AI model failed (%s): try again in a few minutes", failed.String())}
	case runErr != nil:
		return "", Said{fmt.Errorf("the free AI could not start (%v): open MangoMan's setup page and click Get ready", runErr)}
	}
	return "", Said{errors.New("the free AI model gave no answer: try again in a few minutes")}
}
