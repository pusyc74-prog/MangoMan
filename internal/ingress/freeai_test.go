package ingress

import (
	"testing"

	"github.com/pusyc74-prog/mangoman/internal/catalogue"
)

func TestFreeAI(t *testing.T) {
	cat, err := catalogue.Seed()
	if err != nil {
		t.Fatal(err)
	}
	provs := []DashProvider{
		{ID: "groq", Status: PSConnected, KeySource: "store",
			TeamKeys: []DashTeamKey{{Name: "ravi", Status: TKWorking}, {Name: "asha", Status: TKRejected}}},
		{ID: "openrouter", Status: PSConnected, KeySource: "env"},
		{ID: "nvidia", Status: PSConnected, KeySource: "team", TeamKeys: []DashTeamKey{{Name: "ravi", Status: TKResting}}},
		{ID: "cerebras", Status: PSNotConnected},
	}
	models := []DashModel{
		{Provider: "groq", Model: "small", Limits: catalogue.Limits{RPD: 1000, TPD: 200_000}},
		{Provider: "groq", Model: "roomy", Limits: catalogue.Limits{RPD: 1000}},
		{Provider: "groq", Model: "tight", Limits: catalogue.Limits{RPD: 5000, TPM: 6000}},
		{Provider: "openrouter", Model: "or", Limits: catalogue.Limits{RPM: 20}},
		{Provider: "nvidia", Model: "nv", Limits: catalogue.Limits{RPM: 40}},
		{Provider: "cerebras", Model: "cb", Limits: catalogue.Limits{RPD: 14400}},
	}
	got := map[string]FreeRow{}
	for _, r := range freeAI(provs, models, cat) {
		got[r.Provider] = r
	}
	if len(got) != 3 {
		t.Fatalf("only connected providers: %+v", got)
	}
	// Two working keys (the rejected one does not count); the model without
	// a token limit has the most room.
	g := got["groq"]
	if g.Keys != 2 || g.Model != "roomy" || g.RPD != 2000 || g.BigTasks != 2000/taskCost.BigRequests || g.SmallTasks != 2000/taskCost.SmallRequests {
		t.Fatalf("groq: %+v", g)
	}
	// OpenRouter's account limit applies to every model.
	if o := got["openrouter"]; o.RPD != 50 || o.BigTasks != 50/taskCost.BigRequests {
		t.Fatalf("openrouter: %+v", o)
	}
	// No daily limit published: unlimited, on the teammate's key alone.
	if n := got["nvidia"]; n.Keys != 1 || n.BigTasks != -1 || n.RPM != 40 {
		t.Fatalf("nvidia: %+v", n)
	}
	// A request bigger than the per-minute token limit never fits.
	if n := tasks(catalogue.Limits{RPD: 5000, TPM: 6000}, 3, 20, 290_000); n != 0 {
		t.Fatalf("per-minute limit too small: %d", n)
	}
	// The token limit decides when it is the tighter one.
	if n := tasks(catalogue.Limits{RPD: 1000, TPD: 1_000_000}, 1, 20, 290_000); n != 3 {
		t.Fatalf("token-limited tasks: %d", n)
	}
}
