package ingress

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/core"
	"github.com/pusyc74-prog/mangoman/internal/sheets"
)

// Chargesheets: the user's spreadsheets as dashboards (internal/sheets).

func (s *Server) sheetRoutes(mux *http.ServeMux) {
	h := func(f http.HandlerFunc) http.Handler { return s.auth(s.needSheets(f)) }
	mux.Handle("GET /mangoman/sheets", h(s.sheetList))
	mux.Handle("POST /mangoman/sheets", h(s.sheetNew))
	mux.Handle("GET /mangoman/sheets/{id}", h(s.sheetGet))
	mux.Handle("PATCH /mangoman/sheets/{id}", h(s.sheetRename))
	mux.Handle("DELETE /mangoman/sheets/{id}", h(s.sheetDelete))
	mux.Handle("POST /mangoman/sheets/{id}/files", h(s.sheetFile))
	mux.Handle("DELETE /mangoman/sheets/{id}/files/{file}", h(s.sheetRemoveFile))
	mux.Handle("POST /mangoman/sheets/{id}/google", h(s.sheetGoogle))
	mux.Handle("POST /mangoman/sheets/{id}/load", h(s.sheetLoad))
	mux.Handle("POST /mangoman/sheets/{id}/choices", h(s.sheetChoices))
	mux.Handle("POST /mangoman/sheets/{id}/refresh", h(s.sheetRefresh))
	mux.Handle("GET /mangoman/sheets/{id}/data", h(s.sheetData))
	mux.Handle("POST /mangoman/sheets/{id}/ask", h(s.sheetAsk))
	mux.Handle("GET /mangoman/sheets/{id}/ask/{job}", h(s.sheetAskJob))
	mux.Handle("POST /mangoman/sheets/{id}/readout", h(s.sheetReadout))
	mux.Handle("POST /mangoman/sheets/{id}/connect", h(s.sheetConnect))
	mux.Handle("GET /mangoman/sheets/{id}/boards", h(s.sheetBoards))
	mux.Handle("PUT /mangoman/sheets/{id}/boards", h(s.sheetSaveBoards))
	mux.Handle("POST /mangoman/sheets/{id}/plan", h(s.sheetPlan))
}

func (s *Server) needSheets(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Sheets == nil {
			core.WriteError(w, http.StatusServiceUnavailable, "no_sheets", "Chargesheets is not set up in this MangoMan")
			return
		}
		next(w, r)
	}
}

func sheetErr(w http.ResponseWriter, err error) {
	if err.Error() == "no such sheet" {
		core.WriteError(w, http.StatusNotFound, "not_found", "This Chargesheet was deleted.")
		return
	}
	core.WriteError(w, http.StatusBadRequest, "sheet", upper(err.Error()))
}

// upper starts a message with a capital letter, the way the page shows it.
func upper(s string) string {
	if s != "" && s[0] >= 'a' && s[0] <= 'z' {
		return string(s[0]-32) + s[1:]
	}
	return s
}

func sheetBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := readJSON(r, v); err != nil {
		core.WriteError(w, http.StatusBadRequest, "bad_request", "could not read the request")
		return false
	}
	return true
}

func (s *Server) sheetList(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{"sheets": s.Sheets.List()})
}

func (s *Server) sheetNew(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title string `json:"title"`
	}
	if !sheetBody(w, r, &in) {
		return
	}
	sh, err := s.Sheets.New(in.Title)
	if err != nil {
		sheetErr(w, err)
		return
	}
	writeJSON(w, sh)
}

// sheetView is a sheet with what its check screen shows.
type sheetView struct {
	Sheet   sheets.Sheet    `json:"sheet"`
	Profile *sheets.Profile `json:"profile,omitempty"`
	Choices sheets.Choices  `json:"choices"`
	Live    bool            `json:"live"`
}

func (s *Server) view(w http.ResponseWriter, sh sheets.Sheet) {
	v := sheetView{Sheet: sh, Choices: s.Sheets.GetChoices(sh.ID), Live: sh.Live()}
	if p, err := s.Sheets.Profile(sh.ID); err == nil {
		v.Profile = &p
	}
	writeJSON(w, v)
}

func (s *Server) sheetGet(w http.ResponseWriter, r *http.Request) {
	sh, err := s.Sheets.Get(r.PathValue("id"))
	if err != nil {
		sheetErr(w, err)
		return
	}
	s.view(w, sh)
}

func (s *Server) sheetRename(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title string `json:"title"`
	}
	if !sheetBody(w, r, &in) {
		return
	}
	sh, err := s.Sheets.Rename(r.PathValue("id"), in.Title)
	if err != nil {
		sheetErr(w, err)
		return
	}
	writeJSON(w, sh)
}

func (s *Server) sheetDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Sheets.Delete(r.PathValue("id")); err != nil {
		sheetErr(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// sheetFile takes one file: its name as ?name=, its bytes as the body.
func (s *Server) sheetFile(w http.ResponseWriter, r *http.Request) {
	sh, err := s.Sheets.AddFile(r.PathValue("id"), r.URL.Query().Get("name"), http.MaxBytesReader(w, r.Body, sheets.MaxFile+1))
	if err != nil {
		sheetErr(w, err)
		return
	}
	writeJSON(w, sh)
}

func (s *Server) sheetRemoveFile(w http.ResponseWriter, r *http.Request) {
	sh, err := s.Sheets.RemoveSource(r.PathValue("id"), r.PathValue("file"))
	if err != nil {
		sheetErr(w, err)
		return
	}
	writeJSON(w, sh)
}

func (s *Server) sheetGoogle(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL string `json:"url"`
	}
	if !sheetBody(w, r, &in) {
		return
	}
	sh, err := s.Sheets.AddGoogle(r.Context(), r.PathValue("id"), in.URL)
	if err != nil {
		sheetErr(w, err)
		return
	}
	writeJSON(w, sh)
}

func (s *Server) sheetLoad(w http.ResponseWriter, r *http.Request) {
	sh, err := s.Sheets.Load(r.Context(), r.PathValue("id"))
	if err != nil {
		sheetErr(w, err)
		return
	}
	s.view(w, sh)
}

func (s *Server) sheetChoices(w http.ResponseWriter, r *http.Request) {
	var in sheets.Choices
	if !sheetBody(w, r, &in) {
		return
	}
	sh, err := s.Sheets.SetChoices(r.Context(), r.PathValue("id"), in)
	if err != nil {
		sheetErr(w, err)
		return
	}
	s.view(w, sh)
}

func (s *Server) sheetRefresh(w http.ResponseWriter, r *http.Request) {
	sh, changed, err := s.Sheets.Refresh(r.Context(), r.PathValue("id"), r.URL.Query().Get("force") == "1")
	if err != nil {
		sheetErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"sheet": sh, "changed": changed})
}

func (s *Server) sheetData(w http.ResponseWriter, r *http.Request) {
	path, err := s.Sheets.DataPath(r.PathValue("id"))
	if err != nil {
		sheetErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, path)
}

// Questions run in the background; the page asks for their steps as they come.
type askJob struct {
	mu     sync.Mutex
	Steps  []sheets.Step  `json:"steps"`
	Done   bool           `json:"done"`
	Answer *sheets.Answer `json:"answer,omitempty"`
	Error  string         `json:"error,omitempty"`
	ended  time.Time
}

var askJobs = struct {
	sync.Mutex
	m map[string]*askJob
}{m: map[string]*askJob{}}

func (s *Server) sheetAsk(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Question string        `json:"question"`
		History  []sheets.Turn `json:"history"`
	}
	if !sheetBody(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if _, err := s.Sheets.Get(id); err != nil {
		sheetErr(w, err)
		return
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	key := hex.EncodeToString(b)
	job := &askJob{Steps: []sheets.Step{}}
	askJobs.Lock()
	for k, j := range askJobs.m { // forget answers fetched long ago
		j.mu.Lock()
		old := j.Done && time.Since(j.ended) > 30*time.Minute
		j.mu.Unlock()
		if old {
			delete(askJobs.m, k)
		}
	}
	askJobs.m[key] = job
	askJobs.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		a, err := s.Sheets.Ask(ctx, id, in.Question, in.History, func(st sheets.Step) {
			job.mu.Lock()
			job.Steps = append(job.Steps, st)
			job.mu.Unlock()
		})
		job.mu.Lock()
		defer job.mu.Unlock()
		job.Done, job.ended = true, time.Now()
		if err != nil {
			job.Error = upper(err.Error())
			return
		}
		job.Answer = &a
	}()
	writeJSON(w, map[string]string{"job": key})
}

func (s *Server) sheetAskJob(w http.ResponseWriter, r *http.Request) {
	askJobs.Lock()
	job := askJobs.m[r.PathValue("job")]
	askJobs.Unlock()
	if job == nil {
		core.WriteError(w, http.StatusNotFound, "not_found", "This question is no longer here; ask again.")
		return
	}
	job.mu.Lock()
	defer job.mu.Unlock()
	writeJSON(w, job)
}

func (s *Server) sheetReadout(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Facts []string `json:"facts"`
		Fresh bool     `json:"fresh"`
	}
	if !sheetBody(w, r, &in) {
		return
	}
	out, err := s.Sheets.WriteReadout(r.Context(), r.PathValue("id"), in.Facts, in.Fresh)
	if err != nil {
		sheetErr(w, err)
		return
	}
	writeJSON(w, out)
}

// sheetConnect adds a Shopify store or a Meta ad account. The secret goes
// to the key store and is never sent back.
func (s *Server) sheetConnect(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind    string `json:"kind"`
		Account string `json:"account"`
		Secret  string `json:"secret"`
	}
	if !sheetBody(w, r, &in) {
		return
	}
	sh, err := s.Sheets.Connect(r.Context(), r.PathValue("id"), in.Kind, in.Account, in.Secret)
	if err != nil {
		sheetErr(w, err)
		return
	}
	writeJSON(w, sh)
}

func (s *Server) sheetBoards(w http.ResponseWriter, r *http.Request) {
	b, err := s.Sheets.GetBoards(r.PathValue("id"))
	if err != nil {
		sheetErr(w, err)
		return
	}
	writeJSON(w, b)
}

func (s *Server) sheetSaveBoards(w http.ResponseWriter, r *http.Request) {
	var in sheets.Boards // bigger than other requests: up to 20 dashboards
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 600<<10))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil {
		core.WriteError(w, http.StatusBadRequest, "bad_request", "could not read the request")
		return
	}
	if err := s.Sheets.SaveBoards(r.PathValue("id"), in); err != nil {
		sheetErr(w, err)
		return
	}
	s.sheetBoards(w, r)
}

func (s *Server) sheetPlan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Chat []sheets.PlanTurn `json:"chat"`
	}
	if !sheetBody(w, r, &in) {
		return
	}
	p, err := s.Sheets.PlanBoard(r.Context(), r.PathValue("id"), in.Chat)
	if err != nil {
		sheetErr(w, err)
		return
	}
	writeJSON(w, p)
}
