package router

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/pusyc74-prog/mangoman/internal/brain"
	"github.com/pusyc74-prog/mangoman/internal/classify"
	"github.com/pusyc74-prog/mangoman/internal/core"
	"github.com/pusyc74-prog/mangoman/internal/guard"
)

// NonAnswer is the guard outcome for a refusal or non-answer the brain caught.
const NonAnswer = "non_answer"

const taskQuestion = `Which kind of task is this request to an AI assistant?
code: writing, fixing, reviewing or explaining code, or working with software tools
reasoning: maths, logic, planning, step-by-step analysis or comparing options
writing: prose, emails, posts, summaries, translation or general chat
extraction: pulling structured data or fields out of text
fast: a trivial reply that needs no thought (a greeting, yes or no, a single fact)

Request:
`

// brainClass asks the brain for the task class, once per conversation (the
// first user message is the key, since agents resend the history). The rule
// result stands when the brain has no confident answer.
func (rt *Router) brainClass(ctx context.Context, req *core.Request, rule string) string {
	first := clip(req.FirstUserText(), 2000)
	if strings.TrimSpace(first) == "" {
		return rule
	}
	last := clip(req.LastUserText(), 1500)
	q := taskQuestion + first
	if last != first {
		q += "\n\nLatest message:\n" + last
	}
	if c, _, ok := rt.Brain.Choose(ctx, "task", brain.Key("task", first), q, classify.BrainClasses); ok {
		return c
	}
	return rule
}

// refusalHints are phrases that make a short answer worth a second look.
var refusalHints = regexp.MustCompile(`(?i)\b(i can(?:'|no)t|i cannot|i'?m (?:not able|unable)|i am (?:not able|unable)|as an ai\b|i'?m sorry|i apologi[sz]e|unable to (?:help|assist|comply)|can(?:'|no)t (?:help|assist) with|not able to (?:help|assist|provide))`)

// suspicious picks the answers worth checking: short, no tool calls, and
// sounding like a refusal. Everything else skips the brain entirely.
func suspicious(a guard.Answer) bool {
	if len(a.ToolCalls) > 0 {
		return false
	}
	t := strings.TrimSpace(a.Content)
	return utf8.RuneCountInString(t) <= 600 && refusalHints.MatchString(t)
}

// nonAnswer asks whether a suspicious answer fails to answer the request.
// Only a confident yes fails over, so a fair refusal of a harmful request,
// or an honest "I don't know", from every model still reaches the user.
func (rt *Router) nonAnswer(ctx context.Context, req *core.Request, a guard.Answer) bool {
	q := "Does this reply refuse or fail to answer a reasonable request, so another assistant should try instead? " +
		"Answer no if the refusal is justified (the request is harmful or impossible) or if the reply answers it.\n\nRequest:\n" +
		clip(req.LastUserText(), 1500) + "\n\nReply:\n" + clip(a.Content, 600)
	yes, conf, ok := rt.Brain.YesNo(ctx, "non_answer", "", q)
	return ok && yes && conf >= 0.75
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + " [...]"
}

// recorder is a minimal ResponseWriter for internal calls.
type recorder struct {
	h      http.Header
	status int
	body   bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.h }
func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
}
func (r *recorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = 200
	}
	return r.body.Write(p)
}

// InternalCall routes one of MangoMan's own requests (the brain's) through
// the normal pipeline: free-first, failover, quota counted. It never calls
// the brain itself.
func (rt *Router) InternalCall(ctx context.Context, body []byte) (int, []byte, error) {
	req, err := core.ParseChat(body)
	if err != nil {
		return 0, nil, err
	}
	req.Internal = true
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://mangoman.internal/v1/chat/completions", nil)
	if err != nil {
		return 0, nil, err
	}
	w := &recorder{h: http.Header{}}
	rt.Handle(w, hr, req)
	if w.status == 0 {
		return 0, nil, errors.New("no response")
	}
	return w.status, w.body.Bytes(), nil
}
