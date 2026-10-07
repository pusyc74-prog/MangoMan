package ingress

import (
	"errors"
	"net/http"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/catalogue"
	"github.com/pusyc74-prog/mangoman/internal/config"
	"github.com/pusyc74-prog/mangoman/internal/core"
	"github.com/pusyc74-prog/mangoman/internal/keys"
)

// Team key statuses shown on the dashboard.
const (
	TKWorking  = "working"
	TKResting  = "resting" // used up for now; back at RestsUntil
	TKRejected = "rejected"
	TKMissing  = "missing" // named in the config, but the store has no key
)

// DashTeamKey is one teammate's key on a provider card.
type DashTeamKey struct {
	Name          string    `json:"name"`
	Status        string    `json:"status"`
	RequestsToday int       `json:"requests_today"`
	RestsUntil    time.Time `json:"rests_until,omitzero"`
}

// teamKeys describes a provider's team keys for its dashboard card.
func (s *Server) teamKeys(p catalogue.Provider) []DashTeamKey {
	names := s.Cfg.GetTeamKeys(p.ID)
	if len(names) == 0 {
		return nil
	}
	snap := s.Router.Quota.Snapshot()
	now := time.Now()
	out := make([]DashTeamKey, 0, len(names))
	for _, n := range names {
		d := DashTeamKey{Name: n, Status: TKWorking}
		for _, q := range snap {
			if q.Key.Provider != p.ID || q.Key.Account != n {
				continue
			}
			d.RequestsToday += q.ReqToday
			if q.BlockedUntil.After(now) && q.BlockedUntil.After(d.RestsUntil) {
				d.RestsUntil = q.BlockedUntil
			}
		}
		name := keys.Name(p.ID, n)
		switch k, _ := s.Router.Keys.Get(name); {
		case s.Router.Keys.Rejected(name):
			d.Status, d.RestsUntil = TKRejected, time.Time{}
		case k == "":
			d.Status, d.RestsUntil = TKMissing, time.Time{}
		case !d.RestsUntil.IsZero():
			d.Status = TKResting
		}
		out = append(out, d)
	}
	return out
}

func (s *Server) removeTeamKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("provider")
	p, ok := s.Router.Cat.Provider(id)
	if !ok {
		core.WriteError(w, http.StatusNotFound, "unknown_provider", "unknown provider "+id)
		return
	}
	name, err := keys.TeamName(r.PathValue("name"))
	if err != nil {
		core.WriteError(w, http.StatusBadRequest, "bad_team_name", err.Error())
		return
	}
	if st := s.Router.Keys.Store(); st != nil {
		if err := st.Delete(keys.Name(id, name)); err != nil && !errors.Is(err, keys.ErrNotFound) {
			core.WriteError(w, http.StatusInternalServerError, "key_not_removed", err.Error())
			return
		}
	}
	s.Router.Keys.Forget(keys.Name(id, name))
	s.Cfg.SetTeamKey(id, name, false)
	if err := s.save(); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "config_not_saved", err.Error())
		return
	}
	writeJSON(w, s.providerStatus(p, s.modelCount(id)))
}

// registerTeamKey records a team key the command line already stored, so a
// running router uses it at once and never saves its config without it.
func (s *Server) registerTeamKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("provider")
	if _, ok := s.Router.Cat.Provider(id); !ok {
		core.WriteError(w, http.StatusNotFound, "unknown_provider", "unknown provider "+id)
		return
	}
	name, err := keys.TeamName(r.PathValue("name"))
	if err != nil {
		core.WriteError(w, http.StatusBadRequest, "bad_team_name", err.Error())
		return
	}
	s.Router.Keys.Forget(keys.Name(id, name))
	if k, _ := s.Router.Keys.Get(keys.Name(id, name)); k == "" {
		core.WriteError(w, http.StatusNotFound, "no_team_key", "no stored key for "+name)
		return
	}
	s.Cfg.SetTeamKey(id, name, true)
	if err := s.save(); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "config_not_saved", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// People: one local token per person on a shared machine (mangoman people).
// Changed through the running router when it is up, so it never saves an
// old copy of the list over a newer one.

func (s *Server) addPerson(w http.ResponseWriter, r *http.Request) {
	name, err := keys.TeamName(r.PathValue("name"))
	if err != nil {
		core.WriteError(w, http.StatusBadRequest, "bad_name", err.Error())
		return
	}
	tok := config.NewToken()
	s.Cfg.SetPerson(name, tok)
	if err := s.save(); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "config_not_saved", err.Error())
		return
	}
	writeJSON(w, map[string]string{"name": name, "token": tok})
}

func (s *Server) removePerson(w http.ResponseWriter, r *http.Request) {
	name, err := keys.TeamName(r.PathValue("name"))
	if err != nil {
		core.WriteError(w, http.StatusBadRequest, "bad_name", err.Error())
		return
	}
	s.Cfg.SetPerson(name, "")
	if err := s.save(); err != nil {
		core.WriteError(w, http.StatusInternalServerError, "config_not_saved", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
