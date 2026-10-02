package ingress

import (
	"errors"
	"io"
	"net/http"

	"github.com/pusyc74-prog/mangoman/internal/adapt"
	"github.com/pusyc74-prog/mangoman/internal/core"
)

func (s *Server) formatRoutes(mux *http.ServeMux) {
	mux.Handle("POST /v1/messages", s.auth(http.HandlerFunc(s.messages)))
	mux.Handle("POST /v1/messages/count_tokens", s.auth(http.HandlerFunc(s.countTokens)))
	mux.Handle("POST /v1/responses", s.auth(http.HandlerFunc(s.responses)))
}

// readBody reads a request body; ok is false after an error was written.
func readBody(w http.ResponseWriter, r *http.Request, fail func(status int, msg string)) ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBody))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			fail(http.StatusRequestEntityTooLarge, "request body over 32 MB")
		} else {
			fail(http.StatusBadRequest, "could not read body")
		}
		return nil, false
	}
	return body, true
}

func writeRaw(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// messages serves the Anthropic Messages API (Claude Code, Anthropic SDKs).
func (s *Server) messages(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, msg string) { writeRaw(w, status, adapt.AnthropicError(status, msg)) }
	body, ok := readBody(w, r, fail)
	if !ok {
		return
	}
	chat, info, err := adapt.AnthropicToChat(body)
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	req, err := core.ParseChat(chat)
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	req.KeepUsage = true
	tc := adapt.NewTranscoder(w, adapt.NewAnthropicCodec(info, req.EstTokens))
	s.Router.Handle(tc, r, req)
	tc.Finish()
}

// countTokens estimates input tokens; Claude Code calls it to size context.
func (s *Server) countTokens(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, msg string) { writeRaw(w, status, adapt.AnthropicError(status, msg)) }
	body, ok := readBody(w, r, fail)
	if !ok {
		return
	}
	chat, _, err := adapt.AnthropicToChat(body)
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	req, err := core.ParseChat(chat)
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]int{"input_tokens": req.EstTokens})
}

// responses serves the OpenAI Responses API (Codex CLI, newer OpenAI SDKs).
func (s *Server) responses(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, msg string) {
		writeRaw(w, status, adapt.ResponsesError(status, "invalid_request_error", msg))
	}
	body, ok := readBody(w, r, fail)
	if !ok {
		return
	}
	chat, info, err := adapt.ResponsesToChat(body)
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	req, err := core.ParseChat(chat)
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	req.KeepUsage = true
	tc := adapt.NewTranscoder(w, adapt.NewResponsesCodec(info, req.EstTokens))
	s.Router.Handle(tc, r, req)
	tc.Finish()
}
