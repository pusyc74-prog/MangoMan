package ingress

import (
	"encoding/csv"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/pusyc74-prog/mangoman/internal/buffer"
	"github.com/pusyc74-prog/mangoman/internal/core"
	"github.com/pusyc74-prog/mangoman/internal/keys"
)

// Buffer (optional, off until the owner connects it): a task's posting
// calendar, written by the social posts skill as calendar.csv, goes to the
// owner's Buffer as drafts after the owner approves them here; the owner
// adds each picture and time in Buffer. The key lives in the key store,
// never in a file or the page.

const bufferSecret = "connector-buffer"

// The social posts skill's platform names, and Buffer's service names.
var bufferService = map[string]string{"instagram": "instagram", "facebook": "facebook", "linkedin": "linkedin", "x": "twitter",
	"threads": "threads", "pinterest": "pinterest", "youtube": "youtube", "tiktok": "tiktok", "bluesky": "bluesky", "mastodon": "mastodon"}

func (s *Server) bufferRoutes(mux *http.ServeMux) {
	mux.Handle("GET /mangoman/buffer", s.auth(http.HandlerFunc(s.bufferStatus)))
	mux.Handle("PUT /mangoman/buffer", s.auth(http.HandlerFunc(s.bufferConnect)))
	mux.Handle("DELETE /mangoman/buffer", s.auth(http.HandlerFunc(s.bufferDisconnect)))
	mux.Handle("GET /mangoman/buffer/plan", s.auth(http.HandlerFunc(s.bufferPlan)))
	mux.Handle("POST /mangoman/buffer/drafts", s.auth(http.HandlerFunc(s.bufferDrafts)))
}

func (s *Server) bufferKey() string {
	if s.Secrets == nil {
		return ""
	}
	k, err := s.Secrets.Get(bufferSecret)
	if err != nil {
		return ""
	}
	return k
}

// bufferStatus says whether Buffer is connected and lists its channels.
func (s *Server) bufferStatus(w http.ResponseWriter, r *http.Request) {
	key := s.bufferKey()
	if key == "" {
		writeJSON(w, map[string]any{"connected": false})
		return
	}
	ch, err := buffer.Channels(r.Context(), key)
	if err != nil {
		// refused: the saved key no longer works and a new one is needed;
		// otherwise Buffer is busy or out of reach, and the key may be fine.
		writeJSON(w, map[string]any{"connected": true, "error": err.Error(), "refused": errors.Is(err, buffer.ErrKey)})
		return
	}
	writeJSON(w, map[string]any{"connected": true, "channels": ch})
}

// bufferConnect keeps a key that Buffer accepts.
func (s *Server) bufferConnect(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key string `json:"key"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Key) == "" || len(in.Key) > 512 {
		core.WriteError(w, http.StatusBadRequest, "bad_request", "paste the API key from publish.buffer.com/settings/api")
		return
	}
	if s.Secrets == nil {
		core.WriteError(w, http.StatusServiceUnavailable, "no_keystore", "this MangoMan has no key store to keep the key in")
		return
	}
	key := strings.TrimSpace(in.Key)
	ch, err := buffer.Channels(r.Context(), key)
	if err != nil {
		core.WriteError(w, http.StatusBadGateway, "buffer_refused", err.Error())
		return
	}
	if err := s.Secrets.Set(bufferSecret, key); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "not_saved", err.Error())
		return
	}
	writeJSON(w, map[string]any{"connected": true, "channels": ch})
}

func (s *Server) bufferDisconnect(w http.ResponseWriter, _ *http.Request) {
	if s.Secrets != nil {
		if err := s.Secrets.Delete(bufferSecret); err != nil && !errors.Is(err, keys.ErrNotFound) {
			core.WriteError(w, http.StatusInternalServerError, "not_removed", err.Error())
			return
		}
	}
	writeJSON(w, map[string]bool{"connected": false})
}

// PlanRow is one post on one platform from a task's calendar.csv.
type PlanRow struct {
	Date     string `json:"date"`
	Time     string `json:"time"`
	Platform string `json:"platform"`
	Service  string `json:"service"` // Buffer's name for the platform ("" when Buffer has none)
	Post     string `json:"post"`
	Images   string `json:"images"`
	Caption  string `json:"caption"`
}

// calendarIn finds the calendar.csv the social posts skill wrote in a task
// folder (at most two folders down), as a path relative to the folder.
func calendarIn(dir string) string {
	found := ""
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() && strings.Count(rel, string(filepath.Separator)) >= 2 {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == "calendar.csv" && d.Type().IsRegular() {
			found = rel
			return fs.SkipAll
		}
		return nil
	})
	return found
}

// bufferPlan reads a task's calendar for the Send to Buffer screen: every
// post and platform, with its planned date and time as the calendar has them.
func (s *Server) bufferPlan(w http.ResponseWriter, r *http.Request) {
	dir := projects(w)
	if dir == "" {
		return
	}
	task := entry(dir, r.URL.Query().Get("folder"))
	fi, err := os.Stat(task)
	if task == "" || err != nil || !fi.IsDir() {
		core.WriteError(w, http.StatusNotFound, "not_found", "no task folder by that name in MangoMan Projects")
		return
	}
	rel := calendarIn(task)
	if rel == "" {
		core.WriteError(w, http.StatusNotFound, "no_calendar", "this task has no posting calendar (calendar.csv); the social posts skill writes one when posts have dates")
		return
	}
	f, err := os.Open(filepath.Join(task, rel))
	if err != nil {
		core.WriteError(w, http.StatusInternalServerError, "not_read", err.Error())
		return
	}
	defer f.Close()
	cr := csv.NewReader(f)
	cr.FieldsPerRecord = -1 // a short row is skipped below, not the whole calendar
	rows, err := cr.ReadAll()
	if err != nil || len(rows) < 2 {
		core.WriteError(w, http.StatusUnprocessableEntity, "bad_calendar", "calendar.csv could not be read")
		return
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[strings.TrimPrefix(strings.TrimSpace(h), "\uFEFF")] = i
	}
	for _, need := range []string{"Date", "Time", "Platform", "Post", "Images", "Caption"} {
		if _, ok := col[need]; !ok {
			core.WriteError(w, http.StatusUnprocessableEntity, "bad_calendar", "calendar.csv has no "+need+" column")
			return
		}
	}
	out := []PlanRow{}
	for _, row := range rows[1:] {
		if len(row) < len(rows[0]) {
			continue
		}
		get := func(k string) string { return strings.TrimSpace(row[col[k]]) }
		p := PlanRow{Date: get("Date"), Time: get("Time"), Platform: get("Platform"), Post: get("Post"), Images: get("Images"), Caption: get("Caption")}
		p.Service = bufferService[strings.ToLower(p.Platform)]
		out = append(out, p)
	}
	writeJSON(w, map[string]any{"calendar": filepath.ToSlash(rel), "rows": out})
}

// bufferDrafts saves the posts the owner approved as Buffer drafts, one
// request each, and stops (saying so, so the page sends no more) at a
// refused key, a used-up limit or a post Buffer did not keep as a draft.
func (s *Server) bufferDrafts(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Drafts []struct {
			Channel string `json:"channel"`
			Text    string `json:"text"`
		} `json:"drafts"`
	}
	if err := readJSONUpTo(r, &in, 1<<20); err != nil || len(in.Drafts) == 0 || len(in.Drafts) > 60 {
		core.WriteError(w, http.StatusBadRequest, "bad_request", "send 1 to 60 drafts")
		return
	}
	key := s.bufferKey()
	if key == "" {
		core.WriteError(w, http.StatusConflict, "not_connected", "connect Buffer first")
		return
	}
	type result struct {
		OK    bool   `json:"ok"`
		ID    string `json:"id,omitempty"`
		Error string `json:"error,omitempty"`
	}
	out := []result{}
	stop := ""
	for _, d := range in.Drafts {
		switch {
		case stop != "":
			out = append(out, result{Error: stop})
			continue
		case d.Channel == "" || strings.TrimSpace(d.Text) == "":
			out = append(out, result{Error: "no channel or no text"})
			continue
		}
		id, err := buffer.Draft(r.Context(), key, d.Channel, d.Text)
		if errors.Is(err, buffer.ErrKey) || errors.Is(err, buffer.ErrLimit) || errors.Is(err, buffer.ErrNotDraft) {
			stop = "not sent: " + err.Error()
		}
		if err != nil {
			out = append(out, result{ID: id, Error: err.Error()})
			continue
		}
		out = append(out, result{OK: true, ID: id})
	}
	writeJSON(w, map[string]any{"results": out, "stopped": stop != ""})
}
