package ingress

import (
	"net/http"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/arch"
	"github.com/pusyc74-prog/mangoman/internal/core"
)

// ArchView is Mango Arch on the Settings page.
type ArchView struct {
	Live    bool          `json:"live"`  // this build talks to the Mango Arch server
	Share   bool          `json:"share"` // the owner agreed to send counts
	Synced  time.Time     `json:"synced,omitzero"`
	Lessons []arch.Lesson `json:"lessons"`
}

func (s *Server) archView() ArchView {
	v := ArchView{Live: s.Arch != nil, Share: s.Cfg.SharingLessons(), Lessons: []arch.Lesson{}}
	if s.Arch != nil {
		var in []arch.Lesson
		v.Synced, in = s.Arch.Status()
		v.Lessons = append(v.Lessons, in...)
	}
	return v
}

func (s *Server) archGet(w http.ResponseWriter, _ *http.Request) { writeJSON(w, s.archView()) }

// archSet turns sharing on or off.
func (s *Server) archSet(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Share bool `json:"share"`
	}
	if err := readJSON(r, &in); err != nil {
		core.WriteError(w, http.StatusBadRequest, "bad_request", `send {"share": true} or false`)
		return
	}
	s.Cfg.SetShareLessons(in.Share)
	if err := s.save(); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "config_unsaved", err.Error())
		return
	}
	writeJSON(w, s.archView())
}
