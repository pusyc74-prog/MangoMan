package ingress

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/pusyc74-prog/mangoman/internal/agents"
	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/core"
	"github.com/pusyc74-prog/mangoman/internal/skills"
)

// The skills library: MangoMan's own skills, shown and copied but never
// changed, and the user's own, kept next to them in the coding screen's
// skills folder, where agents find them. An own skill has no marker file,
// so updates to MangoMan's packs leave it alone.

type librarySkill struct {
	skills.Pack
	Own   bool   `json:"own"`
	Error string `json:"error,omitempty"` // an own skill whose SKILL.md is not valid
}

const maxSkill = 64 << 10

func (s *Server) libraryRoutes(mux *http.ServeMux) {
	mux.Handle("GET /mangoman/skills", s.auth(http.HandlerFunc(s.skillsList)))
	mux.Handle("GET /mangoman/skills/{name}", s.auth(http.HandlerFunc(s.skillGet)))
	mux.Handle("PUT /mangoman/skills/{name}", s.auth(http.HandlerFunc(s.skillPut)))
	mux.Handle("DELETE /mangoman/skills/{name}", s.auth(http.HandlerFunc(s.skillDelete)))
	mux.Handle("POST /mangoman/skills/{name}/copy", s.auth(http.HandlerFunc(s.skillCopy)))
	mux.Handle("GET /mangoman/my-agents", s.auth(http.HandlerFunc(s.myAgentsGet)))
	mux.Handle("PUT /mangoman/my-agents", s.auth(http.HandlerFunc(s.myAgentsPut)))
}

// starterIDs are the ids of the pre-made agents (starters.json).
func starterIDs() map[string]bool {
	var d struct {
		Agents []struct{ ID string } `json:"agents"`
	}
	raw, _ := fs.ReadFile(uiFiles, "ui/starters.json")
	_ = json.Unmarshal(raw, &d)
	ids := map[string]bool{}
	for _, a := range d.Agents {
		ids[a.ID] = true
	}
	return ids
}

func (s *Server) myAgentsGet(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string][]string{"agents": nonNil(s.Cfg.GetMyAgents())})
}

// myAgentsPut replaces the pre-made agents in the user's team.
func (s *Server) myAgentsPut(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Agents []string `json:"agents"`
	}
	if err := readJSON(r, &in); err != nil {
		core.WriteError(w, http.StatusBadRequest, "bad_request", "send {\"agents\": [\"social-media-manager\", ...]}")
		return
	}
	known := starterIDs()
	for _, id := range in.Agents {
		if !known[id] {
			core.WriteError(w, http.StatusUnprocessableEntity, "unknown_agent", "no pre-made agent called "+id)
			return
		}
	}
	s.Cfg.SetMyAgents(in.Agents)
	if err := s.save(); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "config_not_saved", err.Error())
		return
	}
	s.myAgentsGet(w, r)
}

func ownDir() (string, error) {
	d, err := config.Dir()
	return filepath.Join(d, "opencode", "skills"), err
}

// fromAgent is true for a folder MangoMan copies in from an installed
// advanced agent, replaced each time the coding screen starts.
func fromAgent(skillsDir, name string) bool {
	dir, err := config.Path("agents")
	if err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		return false
	}
	return agents.Exposed(filepath.Join(dir, name), filepath.Join(skillsDir, name))
}

// ownPath is the folder of the user's skill name: an error when the name
// is not allowed or the folder is one of MangoMan's.
func ownPath(name string) (string, error) {
	if !slug.MatchString(name) || len(name) > 64 || strings.HasPrefix(name, "mangoman-") {
		return "", errors.New("Use a name of small letters, digits and dashes, not starting with mangoman-.")
	}
	dir, err := ownDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, name)
	if _, err := os.Stat(filepath.Join(p, skills.Marker)); err == nil || fromAgent(dir, name) {
		return "", errors.New(name + " is one of MangoMan's skills or agents.")
	}
	return p, nil
}

func (s *Server) skillsList(w http.ResponseWriter, _ *http.Request) {
	packs, err := skills.List()
	dir, derr := ownDir()
	if err != nil || derr != nil {
		core.WriteError(w, http.StatusInternalServerError, "no_skills", errors.Join(err, derr).Error())
		return
	}
	out := []librarySkill{}
	for _, p := range packs {
		out = append(out, librarySkill{Pack: p})
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), "mangoman-") {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), skills.Marker)); err == nil || fromAgent(dir, e.Name()) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name(), "SKILL.md"))
		if err != nil {
			continue
		}
		ls := librarySkill{Own: true}
		if ls.Pack, err = skills.ParseSkill(raw); err != nil {
			ls.Name, ls.Error = e.Name(), err.Error()
		} else if ls.Name != e.Name() {
			ls.Name, ls.Error = e.Name(), "the name in SKILL.md must match its folder"
		}
		out = append(out, ls)
	}
	writeJSON(w, map[string]any{"dir": dir, "skills": out})
}

func (s *Server) skillGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if raw, err := skills.Text(name); err == nil {
		writeJSON(w, map[string]any{"name": name, "own": false, "text": string(raw)})
		return
	}
	p, err := ownPath(name)
	raw, rerr := os.ReadFile(filepath.Join(p, "SKILL.md"))
	if err != nil || rerr != nil {
		core.WriteError(w, http.StatusNotFound, "not_found", "no skill named "+name)
		return
	}
	writeJSON(w, map[string]any{"name": name, "own": true, "path": p, "text": string(raw)})
}

// skillPut saves the user's own skill, which must be a valid SKILL.md
// carrying the same name.
func (s *Server) skillPut(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p, err := ownPath(name)
	if err != nil {
		core.WriteError(w, http.StatusBadRequest, "bad_name", err.Error())
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if err := readJSONUpTo(r, &in, 1<<20); err != nil || len(in.Text) > maxSkill {
		core.WriteError(w, http.StatusBadRequest, "bad_request", "could not read the skill (at most 64 KB)")
		return
	}
	in.Text = strings.ReplaceAll(in.Text, "\r\n", "\n")
	pk, err := skills.ParseSkill([]byte(in.Text))
	if err == nil && pk.Name != name {
		err = errors.New("the name in SKILL.md must be " + name)
	}
	if err != nil {
		core.WriteError(w, http.StatusBadRequest, "bad_skill", err.Error())
		return
	}
	if err := os.MkdirAll(p, 0o755); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "not_saved", err.Error())
		return
	}
	f := filepath.Join(p, "SKILL.md")
	if err := os.WriteFile(f+".tmp", []byte(in.Text), 0o644); err != nil || os.Rename(f+".tmp", f) != nil {
		core.WriteError(w, http.StatusInternalServerError, "not_saved", "the skill was not saved")
		return
	}
	writeJSON(w, librarySkill{Pack: pk, Own: true})
}

func (s *Server) skillDelete(w http.ResponseWriter, r *http.Request) {
	p, err := ownPath(r.PathValue("name"))
	if err == nil {
		_, err = os.Stat(filepath.Join(p, "SKILL.md"))
	}
	if err != nil {
		core.WriteError(w, http.StatusNotFound, "not_found", "no skill of yours by that name")
		return
	}
	if err := os.RemoveAll(p); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "not_removed", err.Error())
		return
	}
	writeJSON(w, map[string]bool{"removed": true})
}

// skillCopy makes one of MangoMan's skills the user's own, under a new
// name, to change as they like. BasedOn names the pre-made agent a copied
// agent playbook belongs to, so the copy starts from that agent's brief.
func (s *Server) skillCopy(w http.ResponseWriter, r *http.Request) {
	var in struct {
		As      string `json:"as"`
		BasedOn string `json:"based_on"`
	}
	if err := readJSON(r, &in); err != nil || (in.BasedOn != "" && !starterIDs()[in.BasedOn]) {
		core.WriteError(w, http.StatusBadRequest, "bad_request", "could not read the request")
		return
	}
	p, err := ownPath(in.As)
	if err != nil {
		core.WriteError(w, http.StatusBadRequest, "bad_name", err.Error())
		return
	}
	if _, err := os.Stat(p); err == nil {
		core.WriteError(w, http.StatusConflict, "exists", "You already have a skill named "+in.As+".")
		return
	}
	if err := skills.Copy(r.PathValue("name"), in.As, p); err != nil {
		os.RemoveAll(p)
		core.WriteError(w, http.StatusNotFound, "not_copied", err.Error())
		return
	}
	if in.BasedOn != "" {
		f := filepath.Join(p, "SKILL.md")
		raw, err := os.ReadFile(f)
		if err == nil && strings.Contains(string(raw), "\nmetadata:\n") {
			raw = []byte(strings.Replace(string(raw), "\nmetadata:\n", "\nmetadata:\n  based_on: "+in.BasedOn+"\n", 1))
			err = os.WriteFile(f, raw, 0o644)
		}
		if err != nil {
			os.RemoveAll(p)
			core.WriteError(w, http.StatusInternalServerError, "not_copied", "the copy could not be marked as based on "+in.BasedOn)
			return
		}
	}
	writeJSON(w, map[string]string{"name": in.As, "path": p})
}
