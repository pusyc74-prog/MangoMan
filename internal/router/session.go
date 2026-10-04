package router

import (
	"crypto/sha256"
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
		rt.sess.last = Used{Provider: c.Provider.ID, Model: c.Model.Canonical, Class: class, Weak: c.Weak, Time: time.Now()}
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
	o.StrongBusy = len(cands) > 0
	for i, c := range cands {
		if i < 3 {
			o.Next = append(o.Next, c.Target())
		}
		if !c.Weak {
			o.StrongBusy = false
		}
	}
	if o.StrongBusy {
		o.BackAt = info.EarliestReset
	}
	return o
}
