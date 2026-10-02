// Package router picks the best free candidate for each request, sends it
// directly to the provider, checks the answer, and fails over on errors,
// rate limits and bad answers.
package router

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/breaker"
	"github.com/pusyc74-prog/mangoman/internal/catalogue"
	"github.com/pusyc74-prog/mangoman/internal/classify"
	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/core"
	"github.com/pusyc74-prog/mangoman/internal/guard"
	"github.com/pusyc74-prog/mangoman/internal/keys"
	"github.com/pusyc74-prog/mangoman/internal/providers"
	"github.com/pusyc74-prog/mangoman/internal/quota"
	"github.com/pusyc74-prog/mangoman/internal/store"
)

// Router wires the routing pieces together.
type Router struct {
	Cat      *catalogue.Catalogue
	Keys     *keys.Resolver
	Quota    *quota.Tracker
	Breakers *breaker.Set
	Client   *providers.Client
	Cfg      *config.Config
	Log      *store.Log
	Health   *Health
	Logf     func(format string, args ...any)

	// StreamIdle aborts a stream that sends nothing for this long.
	StreamIdle time.Duration
	// NonStreamTimeout caps one non-streaming attempt.
	NonStreamTimeout time.Duration
}

// New returns a router with default breaker and timeout settings.
func New(cat *catalogue.Catalogue, kr *keys.Resolver, cfg *config.Config) *Router {
	return &Router{
		Cat: cat, Keys: kr, Cfg: cfg,
		Quota:            quota.New(),
		Breakers:         breaker.New(3, 30*time.Second, 5*time.Minute),
		Client:           providers.NewClient(),
		Logf:             func(string, ...any) {},
		Health:           NewHealth(),
		StreamIdle:       60 * time.Second,
		NonStreamTimeout: 180 * time.Second,
	}
}

const maxBody = 64 << 20

// attemptResult says what happened on one candidate.
type attemptResult struct {
	done     bool          // response written to the client
	firstOut time.Duration // streams: time to first output; 0 = use total time
	outcome  string
	status   int
	errMsg   string
	badBody  []byte // a complete answer that failed the guard, kept as last resort
	badWhy   string
	clientIn bool // upstream rejected the request itself (4xx client error)
	tokens   int  // tokens counted against quota
}

// Handle serves one Chat Completions request end to end.
func (rt *Router) Handle(w http.ResponseWriter, r *http.Request, req *core.Request) {
	id := newID()
	if msg := rt.scopeProblem(req.Model); msg != "" {
		core.WriteError(w, http.StatusNotFound, "model_not_found", msg)
		return
	}
	class := classify.Classify(req)
	cands, info := rt.plan(req, class)
	if len(cands) == 0 {
		rt.writeNoCandidate(w, info)
		return
	}

	var (
		lastStatus   int
		lastMsg      string
		fallbackBody []byte
		fallbackWhy  string
		fallbackCand Candidate
		clientErrors int
		sawRateLimit bool
		attempts     int
		unreachable  = map[string]bool{} // providers whose host failed this request
	)
	for _, c := range cands {
		if r.Context().Err() != nil {
			return // client went away
		}
		if unreachable[c.Provider.ID] {
			continue
		}
		if !rt.Breakers.Allow(c.Target()) {
			continue
		}
		if ok, _ := rt.allow(c, req.EstTokens); !ok {
			continue
		}
		attempts++
		start := time.Now()
		res := rt.attempt(w, r, req, c, class, id, attempts)
		if res.outcome != "client_gone" {
			lat := res.firstOut
			if lat == 0 {
				lat = time.Since(start)
			}
			good := strings.HasPrefix(res.outcome, "ok")
			if !good {
				lat = 0
			}
			rt.Health.Observe(c, res.outcome, good, lat)
		}
		rt.Log.Add(store.Event{
			Time: start, RequestID: id, Provider: c.Provider.ID, Model: c.Model.Canonical, Class: class,
			Outcome: res.outcome, Status: res.status, LatencyMS: time.Since(start).Milliseconds(),
			Attempt: attempts, Stream: req.Stream, Tokens: res.tokens,
		})
		rt.Logf("req=%s attempt=%d %s -> %s (%d) %s", id, attempts, c.Target(), res.outcome, res.status, res.errMsg)
		if res.done {
			return
		}
		if res.outcome == "rate_limited" {
			sawRateLimit = true
		}
		if res.outcome == "network_error" || res.outcome == "key_rejected" {
			// The provider itself is down or our key is bad: its other models
			// would fail the same way, so skip them for this request.
			unreachable[c.Provider.ID] = true
		}
		if res.badBody != nil && fallbackBody == nil {
			fallbackBody, fallbackWhy, fallbackCand = res.badBody, res.badWhy, c
		}
		if res.status != 0 {
			lastStatus, lastMsg = res.status, res.errMsg
		} else if res.errMsg != "" {
			lastMsg = res.errMsg
		}
		if res.clientIn {
			// The request itself may be at fault. Try one other provider, then
			// return the provider's error rather than burning more quota.
			clientErrors++
			if clientErrors >= 2 {
				core.WriteError(w, lastStatus, "upstream_rejected_request", lastMsg)
				return
			}
		}
	}

	if fallbackBody != nil {
		// Every candidate failed somehow, but one gave a complete answer that
		// only failed the guard. Better to return it, clearly labelled.
		rt.setHeaders(w, fallbackCand, class, attempts)
		w.Header().Set("X-MangoMan-Guard", fallbackWhy)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(fallbackBody)
		return
	}
	if sawRateLimit || attempts == 0 {
		_, info := rt.plan(req, class)
		reset := info.EarliestReset
		if reset.IsZero() {
			reset = time.Now().Add(time.Minute)
		}
		core.WriteExhausted(w, reset, "all free candidates are rate limited; retry after the reset or connect another provider")
		return
	}
	if lastStatus == 0 {
		lastStatus = http.StatusBadGateway
	}
	if lastStatus < 500 {
		lastStatus = http.StatusBadGateway
	}
	core.WriteError(w, lastStatus, "all_candidates_failed", fmt.Sprintf("tried %d free candidates; last error: %s", attempts, lastMsg))
}

func (rt *Router) writeNoCandidate(w http.ResponseWriter, info planInfo) {
	switch {
	case info.QuotaBlocked > 0 && info.EarliestReset.After(time.Now()):
		core.WriteExhausted(w, info.EarliestReset, "free capacity is used up on every connected provider; it returns at the next reset")
	case info.Considered > 0 && info.NoKey == info.Considered:
		core.WriteError(w, http.StatusServiceUnavailable, "no_providers_connected", "no provider keys found: run `mangoman keys add groq` (or another provider)")
	case info.DoesNotFit > 0 && info.BreakerOpen == 0 && info.QuotaBlocked == 0:
		core.WriteError(w, http.StatusBadRequest, "no_model_fits", "no connected free model supports this request (context size, tools, JSON mode or images)")
	default:
		core.WriteError(w, http.StatusServiceUnavailable, "no_candidates", "no free candidate is available right now; providers may be down, see `mangoman status`")
	}
}

func (rt *Router) setHeaders(w http.ResponseWriter, c Candidate, class string, attempts int) {
	h := w.Header()
	h.Set("X-MangoMan-Provider", c.Provider.ID)
	h.Set("X-MangoMan-Model", c.Model.Canonical)
	h.Set("X-MangoMan-Upstream-Model", c.Model.Upstream)
	h.Set("X-MangoMan-Class", class)
	h.Set("X-MangoMan-Attempts", strconv.Itoa(attempts))
	h.Set("X-MangoMan-Data-Policy", rt.Cat.PolicyFor(c.Model).Label())
}

// attempt sends the request to one candidate.
func (rt *Router) attempt(w http.ResponseWriter, r *http.Request, req *core.Request, c Candidate, class, id string, n int) attemptResult {
	q := c.Provider.Quirks
	body, addedUsage, err := req.BodyWith(c.Model.Upstream, core.Upstream{
		Drop: q.DropParams, MaxTokensField: q.MaxTokensField, StreamUsage: q.StreamUsage,
	})
	if err != nil {
		return attemptResult{outcome: "internal", errMsg: err.Error()}
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if !req.Stream {
		var tcancel context.CancelFunc
		ctx, tcancel = context.WithTimeout(ctx, rt.NonStreamTimeout)
		defer tcancel()
	}

	resp, err := rt.Client.Chat(ctx, c.Provider, c.Key, body, req.Stream)
	if err != nil {
		if r.Context().Err() != nil {
			return attemptResult{done: true, outcome: "client_gone"}
		}
		rt.Breakers.Failure(c.Target())
		out := "network_error"
		var ne net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) && ne.Timeout() {
			out = "timeout"
		}
		return attemptResult{outcome: out, errMsg: err.Error()}
	}
	defer resp.Body.Close()
	rt.Quota.FromHeaders(c.QKey, resp.Header, c.Provider.RateHeaders)

	if resp.StatusCode != http.StatusOK {
		return rt.upstreamError(resp, c)
	}
	if req.Stream {
		return rt.stream(ctx, cancel, w, req, c, class, n, resp, addedUsage)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		rt.Breakers.Failure(c.Target())
		return attemptResult{outcome: "read_error", errMsg: err.Error()}
	}
	ans, ok := guard.ParseChatResponse(data)
	if !ok {
		// Some providers (OpenRouter) report errors inside a 200 body.
		if code, msg, isErr := errorIn200(data); isErr {
			if code == http.StatusTooManyRequests {
				rt.Quota.Block(c.QKey, time.Now().Add(time.Minute))
				return attemptResult{outcome: "rate_limited", status: code, errMsg: msg}
			}
			rt.Breakers.Failure(c.Target())
			return attemptResult{outcome: "error_in_200", status: code, errMsg: msg}
		}
		rt.Breakers.Failure(c.Target())
		return attemptResult{outcome: "quality:" + guard.Unparseable, status: resp.StatusCode, errMsg: "unparseable response"}
	}
	rt.Breakers.Success(c.Target())
	tokens := usageTokens(data, req.EstTokens, len(ans.Content))
	rt.record(c, tokens)
	if why := guard.Check(req, ans); why != "" {
		return attemptResult{outcome: "quality:" + why, status: resp.StatusCode, errMsg: "answer failed guard: " + why, badBody: data, badWhy: why, tokens: tokens}
	}
	rt.setHeaders(w, c, class, n)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
	return attemptResult{done: true, outcome: "ok", status: 200, tokens: tokens}
}

// upstreamError classifies a non-200 reply.
func (rt *Router) upstreamError(resp *http.Response, c Candidate) attemptResult {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	msg := upstreamMessage(data)
	res := attemptResult{status: resp.StatusCode, errMsg: msg}
	switch s := resp.StatusCode; {
	case s == http.StatusTooManyRequests:
		rt.Quota.Block(c.QKey, time.Now().Add(quota.RetryAfter(resp.Header)))
		res.outcome = "rate_limited"
	case s == http.StatusUnauthorized:
		// Key invalid or revoked: stop using it until the user fixes it.
		rt.Keys.Disable(c.Provider.ID)
		res.outcome = "key_rejected"
	case s == http.StatusForbidden:
		// A 403 is usually about this one model (OpenRouter: "only available
		// on agentic harnesses", region or tier locks), not the key. Avoid
		// the model; the provider's other models stay in use.
		rt.Breakers.Failure(c.Target())
		rt.Breakers.Failure(c.Target())
		rt.Breakers.Failure(c.Target())
		res.outcome = "model_forbidden"
	case s == http.StatusNotFound || s == http.StatusGone:
		// Model removed, renamed or retired (NVIDIA answers 410): avoid it.
		rt.Breakers.Failure(c.Target())
		rt.Breakers.Failure(c.Target())
		rt.Breakers.Failure(c.Target())
		res.outcome = "model_not_found"
	case s == http.StatusRequestTimeout || s == http.StatusConflict || s == http.StatusRequestEntityTooLarge:
		res.outcome = "retryable_" + strconv.Itoa(s)
	case s >= 500:
		rt.Breakers.Failure(c.Target())
		res.outcome = "server_error"
	default: // 400, 422 and other client errors
		res.outcome = "client_error"
		res.clientIn = true
	}
	return res
}

func upstreamMessage(data []byte) string {
	var e struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(data, &e) == nil && e.Error != nil {
		switch v := e.Error.(type) {
		case string:
			return v
		case map[string]any:
			if m, ok := v["message"].(string); ok {
				return m
			}
		}
	}
	if len(data) > 300 {
		data = data[:300]
	}
	return string(data)
}

// errorIn200 detects {"error":{...}} bodies sent with HTTP 200.
func errorIn200(data []byte) (code int, msg string, ok bool) {
	var e struct {
		Error *struct {
			Code    any    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &e) != nil || e.Error == nil {
		return 0, "", false
	}
	switch v := e.Error.Code.(type) {
	case float64:
		code = int(v)
	case string:
		code, _ = strconv.Atoi(v)
	}
	if code < 400 || code > 599 {
		code = http.StatusBadGateway
	}
	return code, e.Error.Message, true
}

// usageTokens reads usage.total_tokens, else estimates.
func usageTokens(body []byte, est, outChars int) int {
	var u struct {
		Usage struct {
			Total int `json:"total_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &u) == nil && u.Usage.Total > 0 {
		return u.Usage.Total
	}
	return est + outChars/4
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// scopeProblem explains a strict or group request that can never match.
func (rt *Router) scopeProblem(model string) string {
	requested := strings.ToLower(strings.TrimSpace(model))
	scope := rt.scope(requested)
	if scope == nil {
		return ""
	}
	if len(scope) == 0 {
		return "no group called " + strings.TrimPrefix(requested, GroupPrefix) + ": create it with `mangoman group set <name> <model>...`"
	}
	for _, m := range rt.Cat.AllModels() {
		if scopeIndex(scope, m) >= 0 {
			return ""
		}
	}
	return "none of " + strings.Join(scope, ", ") + " is in the catalogue: see `mangoman models`"
}
