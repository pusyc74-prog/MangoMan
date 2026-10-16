package ingress

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/core"
)

// Super agents start from a brief, not from questions in a chat: the page
// asks once, in a form, and MangoMan writes the answers into the task's
// folder, where the agent reads them. The business part is kept in the
// projects folder's Brand folder and offered again to every agent.

// businessFields are what the business form keeps.
var businessFields = map[string]bool{"name": true, "website": true, "sells": true, "customers": true, "where": true,
	"language": true, "currency": true, "timezone": true, "social": true, "competitors": true, "facts": true,
	"tone": true, "colours": true, "avoid": true}

var logoTypes = map[string]bool{".png": true, ".jpg": true, ".jpeg": true}

const maxField = 4000

func (s *Server) briefRoutes(mux *http.ServeMux) {
	mux.Handle("GET /mangoman/business", s.auth(http.HandlerFunc(s.businessGet)))
	mux.Handle("PUT /mangoman/business", s.auth(http.HandlerFunc(s.businessPut)))
	mux.Handle("POST /mangoman/business/logo", s.auth(http.HandlerFunc(s.businessLogo)))
	mux.Handle("POST /mangoman/agents/brief", s.auth(http.HandlerFunc(s.agentBrief)))
}

// brandDir is the folder that keeps the business profile and logo.
func brandDir(w http.ResponseWriter) string {
	dir := projects(w)
	if dir == "" {
		return ""
	}
	d := filepath.Join(dir, "Brand")
	if err := os.MkdirAll(d, 0o755); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "no_folder", err.Error())
		return ""
	}
	return d
}

type business struct {
	Fields map[string]string `json:"fields"`
	Logo   string            `json:"logo,omitempty"` // file name in the Brand folder
}

func readBusiness(dir string) business {
	b := business{Fields: map[string]string{}}
	if raw, err := os.ReadFile(filepath.Join(dir, "business.json")); err == nil {
		_ = json.Unmarshal(raw, &b)
	}
	if b.Fields == nil {
		b.Fields = map[string]string{}
	}
	if b.Logo != "" {
		if _, err := os.Stat(filepath.Join(dir, b.Logo)); err != nil {
			b.Logo = ""
		}
	}
	return b
}

func writeBusiness(dir string, b business) error {
	raw, _ := json.MarshalIndent(b, "", "  ")
	p := filepath.Join(dir, "business.json")
	if err := os.WriteFile(p+".tmp", raw, 0o644); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}

func (s *Server) businessGet(w http.ResponseWriter, _ *http.Request) {
	dir := brandDir(w)
	if dir == "" {
		return
	}
	writeJSON(w, readBusiness(dir))
}

func (s *Server) businessPut(w http.ResponseWriter, r *http.Request) {
	dir := brandDir(w)
	if dir == "" {
		return
	}
	var in struct {
		Fields map[string]string `json:"fields"`
	}
	if err := readJSON(r, &in); err != nil {
		core.WriteError(w, http.StatusBadRequest, "bad_request", "could not read the request")
		return
	}
	b := readBusiness(dir)
	b.Fields = map[string]string{}
	for k, v := range in.Fields {
		v = strings.TrimSpace(v)
		if !businessFields[k] || len(v) > maxField {
			core.WriteError(w, http.StatusBadRequest, "bad_field", fmt.Sprintf("%s: unknown, or longer than %d characters", k, maxField))
			return
		}
		if v != "" {
			b.Fields[k] = v
		}
	}
	if err := writeBusiness(dir, b); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "not_saved", err.Error())
		return
	}
	writeJSON(w, b)
}

// businessLogo keeps the logo as Brand/logo.<ext>, replacing an older one.
func (s *Server) businessLogo(w http.ResponseWriter, r *http.Request) {
	dir := brandDir(w)
	if dir == "" {
		return
	}
	ext := strings.ToLower(filepath.Ext(r.URL.Query().Get("name")))
	if !logoTypes[ext] {
		core.WriteError(w, http.StatusBadRequest, "bad_file", "The logo must be a PNG or JPG picture.")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 5<<20))
	if err != nil || len(raw) == 0 {
		core.WriteError(w, http.StatusBadRequest, "bad_file", "The logo was not saved (at most 5 MB).")
		return
	}
	b := readBusiness(dir)
	name := "logo" + ext
	if err := os.WriteFile(filepath.Join(dir, name), raw, 0o644); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "not_saved", err.Error())
		return
	}
	if b.Logo != "" && b.Logo != name {
		os.Remove(filepath.Join(dir, b.Logo))
	}
	b.Logo = name
	if err := writeBusiness(dir, b); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "not_saved", err.Error())
		return
	}
	writeJSON(w, b)
}

var agentID = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// agentBrief makes the task folder for one run of a super agent: brief.json
// with the business and this job's answers, and a copy of the logo.
func (s *Server) agentBrief(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Agent string         `json:"agent"`
		Title string         `json:"title"`
		Job   map[string]any `json:"job"`
	}
	if err := readJSON(r, &in); err != nil || !agentID.MatchString(in.Agent) || len(in.Agent) > 60 || len(in.Title) > 80 {
		core.WriteError(w, http.StatusBadRequest, "bad_request", "could not read the brief")
		return
	}
	for k, v := range in.Job {
		switch x := v.(type) {
		case string:
			if len(x) <= maxField {
				continue
			}
		case []any:
			if len(x) <= 40 {
				continue
			}
		}
		core.WriteError(w, http.StatusBadRequest, "bad_field", k+": too long")
		return
	}
	brand := brandDir(w)
	if brand == "" {
		return
	}
	biz := readBusiness(brand)
	base := filepath.Dir(brand)
	stem := in.Agent + "-" + time.Now().Format("2006-01-02")
	folder := filepath.Join(base, stem)
	for i := 2; ; i++ { // a second run the same day gets its own folder
		if _, err := os.Stat(folder); os.IsNotExist(err) {
			break
		}
		folder = filepath.Join(base, fmt.Sprintf("%s-%d", stem, i))
	}
	if err := os.MkdirAll(folder, 0o755); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "no_folder", err.Error())
		return
	}
	logo := ""
	if biz.Logo != "" {
		if raw, err := os.ReadFile(filepath.Join(brand, biz.Logo)); err == nil && os.WriteFile(filepath.Join(folder, biz.Logo), raw, 0o644) == nil {
			logo = biz.Logo
		}
	}
	brief := map[string]any{"agent": in.Agent, "title": in.Title, "made": time.Now().Format(time.RFC3339),
		"business": biz.Fields, "logo": logo, "job": in.Job,
		"note": "Written by MangoMan from the owner's answers. Use these facts; do not ask again for what is here."}
	raw, _ := json.MarshalIndent(brief, "", "  ")
	if err := os.WriteFile(filepath.Join(folder, "brief.json"), raw, 0o644); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "not_saved", err.Error())
		return
	}
	writeJSON(w, map[string]string{"folder": folder})
}
