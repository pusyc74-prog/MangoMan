package router

import (
	"crypto/sha256"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/core"
)

// session holds what the router remembers between requests: the model each
// chat is on, whether weak models are allowed for now, and the last model
// used (for the dashboard's live panel).
type session struct {
	mu          sync.Mutex
	chats       map[[16]byte]chat
	weakerUntil time.Time
	last        Used
	turns       map[string]int       // per model: whose key goes first next (team keys)
	busy        map[string]time.Time // per model: overloaded, tried last until then
}

// busyFor is how long a model that said it is overloaded is tried last.
const busyFor = 2 * time.Minute

// markBusy notes that a model said it is overloaded.
func (rt *Router) markBusy(target string) {
	rt.sess.mu.Lock()
	defer rt.sess.mu.Unlock()
	if rt.sess.busy == nil {
		rt.sess.busy = map[string]time.Time{}
	}
	rt.sess.busy[target] = time.Now().Add(busyFor)
}

// busyLast moves models that said they are overloaded in the last two minutes
// behind the others. They stay in the list: with one model to choose from
// (strict mode), it is still asked. Measured on NVIDIA: about one request in
// five came back "Service temporarily overloaded", too few in a row to trip
// the breaker, so every request lost time on the same busy model first.
func (rt *Router) busyLast(cs []Candidate) []Candidate {
	rt.sess.mu.Lock()
	now := time.Now()
	busy := map[string]bool{}
	for t, until := range rt.sess.busy {
		if now.Before(until) {
			busy[t] = true
		} else {
			delete(rt.sess.busy, t)
		}
	}
	rt.sess.mu.Unlock()
	if len(busy) == 0 {
		return cs
	}
	out := slices.Clone(cs)
	sort.SliceStable(out, func(i, j int) bool { return !busy[out[i].Target()] && busy[out[j].Target()] })
	return out
}

// turn returns how far to rotate a model's keys for this request, and moves
// the turn on for the next one.
func (rt *Router) turn(target string) int {
	rt.sess.mu.Lock()
	defer rt.sess.mu.Unlock()
	if rt.sess.turns == nil {
		rt.sess.turns = map[string]int{}
	}
	n := rt.sess.turns[target]
	rt.sess.turns[target] = n + 1
	return n
}

type chat struct {
	model string
	seen  time.Time
}

// Used is the model that answered the latest request.
type Used struct {
	Provider string    `json:"provider"`
	Model    string    `json:"model"`
	Class    string    `json:"class"`
	Weak     bool      `json:"weak"`
	Key      string    `json:"key,omitempty"` // the teammate whose key answered; empty for your own
	Time     time.Time `json:"time"`
}

// chatIdle is how long a chat keeps its model after its last request.
const chatIdle = 2 * time.Hour

// chatKey identifies a chat by its first user message: every request of an
// ongoing chat repeats it. Requests with and without tools are kept apart,
// so a coding tool's side requests (titles) do not move the main chat.
func chatKey(req *core.Request) ([16]byte, bool) {
	first := req.FirstUserText()
	if first == "" || req.Internal {
		return [16]byte{}, false
	}
	tools := "0"
	if req.HasTools() {
		tools = "1"
	}
	sum := sha256.Sum256([]byte(tools + first))
	var k [16]byte
	copy(k[:], sum[:])
	return k, true
}

// stuck returns the model this request's chat was last answered by, or "".
func (rt *Router) stuck(req *core.Request) string {
	k, ok := chatKey(req)
	if !ok {
		return ""
	}
	rt.sess.mu.Lock()
	defer rt.sess.mu.Unlock()
	if c, ok := rt.sess.chats[k]; ok && time.Since(c.seen) < chatIdle {
		return c.model
	}
	return ""
}

// answered records the model that answered a request.
func (rt *Router) answered(req *core.Request, c Candidate, class string) {
	rt.sess.mu.Lock()
	defer rt.sess.mu.Unlock()
	if !req.Internal {
		rt.sess.last = Used{Provider: c.Provider.ID, Model: c.Model.Canonical, Class: class, Weak: c.Weak, Key: c.teamKey(), Time: time.Now()}
	}
	k, ok := chatKey(req)
	if !ok {
		return
	}
	if rt.sess.chats == nil || len(rt.sess.chats) > 5000 {
		// Rarely reached; dropping old chats only costs them their preference.
		rt.sess.chats = map[[16]byte]chat{}
	}
	rt.sess.chats[k] = chat{model: c.Model.Canonical, seen: time.Now()}
}

// AllowWeakerFor lets every request use weak models for d (0 turns it off).
func (rt *Router) AllowWeakerFor(d time.Duration) {
	rt.sess.mu.Lock()
	defer rt.sess.mu.Unlock()
	rt.sess.weakerUntil = time.Now().Add(d)
}

func (rt *Router) allowWeaker() bool {
	if rt.Cfg.GetAllowWeaker() {
		return true
	}
	rt.sess.mu.Lock()
	defer rt.sess.mu.Unlock()
	return time.Now().Before(rt.sess.weakerUntil)
}

// Outlook is what the dashboard's live panel shows.
type Outlook struct {
	Last        *Used     `json:"last,omitempty"`
	Next        []string  `json:"next"`                   // the models the next request would try, in order
	StrongBusy  bool      `json:"strong_busy"`            // only weak models are free right now
	BackAt      time.Time `json:"back_at,omitzero"`       // when a strong model frees up, if known
	WeakerUntil time.Time `json:"weaker_until,omitzero"`  // weak models allowed until then
	WeakerOn    bool      `json:"weaker_always,omitzero"` // weak models always allowed
	HasList     bool      `json:"has_list,omitzero"`      // My list is set: other models need permission
}

// Outlook previews the next request: a typical chat of the same kind as
// the last one.
func (rt *Router) Outlook() Outlook {
	rt.sess.mu.Lock()
	o := Outlook{WeakerOn: rt.Cfg.GetAllowWeaker()}
	if rt.sess.last.Model != "" {
		l := rt.sess.last
		o.Last = &l
	}
	if time.Now().Before(rt.sess.weakerUntil) {
		o.WeakerUntil = rt.sess.weakerUntil
	}
	rt.sess.mu.Unlock()
	class := ""
	if o.Last != nil {
		class = o.Last.Class
	}
	cands, info := rt.plan(&core.Request{EstTokens: 8000, AllowWeaker: true}, class)
	fav, _ := rt.splitFavorites(cands)
	o.HasList = len(rt.Cfg.GetFavorites()) > 0
	o.StrongBusy = len(cands) > 0
	for i, c := range cands {
		if i < 3 {
			o.Next = append(o.Next, c.Target())
		}
		if !c.Weak && !o.HasList {
			o.StrongBusy = false
		}
	}
	if len(fav) > 0 {
		o.StrongBusy = false
	}
	if o.StrongBusy {
		o.BackAt = info.EarliestReset
	}
	return o
}
