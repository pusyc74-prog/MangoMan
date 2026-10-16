package sheets

import (
	"context"
	"strings"
	"testing"
)

func TestBoardsKeepTheirShape(t *testing.T) {
	st := &Store{Dir: t.TempDir()}
	sh, _ := st.New("")
	if b, err := st.GetBoards(sh.ID); err != nil || b.Boards == nil || b.Metrics == nil {
		t.Fatalf("empty: %+v %v", b, err)
	}
	rev := Ref{DS: "jan + feb", Col: "Revenue (INR)", Agg: "sum"}
	good := Boards{
		Metrics: []Metric{{ID: "roas", Name: "Return on ad spend", Kind: "number", Add: []Ref{rev}, Per: []Ref{{DS: "spend", Col: "Spend (INR)", Agg: "sum"}}, Times: 1}},
		Boards: []Board{{ID: "b1", Name: "Mine", Tiles: []Tile{
			{ID: "t1", Title: "Revenue", Type: "kpi", Values: []Ref{rev}},
			{ID: "t2", Title: "ROAS by week", Type: "line", By: "@week", Values: []Ref{{Metric: "roas"}}, Size: "l"},
		}}},
	}
	if err := st.SaveBoards(sh.ID, good); err != nil {
		t.Fatal(err)
	}
	if b, _ := st.GetBoards(sh.ID); len(b.Boards) != 1 || b.Boards[0].Tiles[1].Values[0].Metric != "roas" {
		t.Fatalf("saved: %+v", b)
	}
	bad := map[string]func(*Boards){
		"kpi with a split":   func(b *Boards) { b.Boards[0].Tiles[0].By = "Product" },
		"line by a column":   func(b *Boards) { b.Boards[0].Tiles[1].By = "Product" },
		"unknown metric":     func(b *Boards) { b.Boards[0].Tiles[1].Values[0].Metric = "nope" },
		"metric in a metric": func(b *Boards) { b.Metrics[0].Per = []Ref{{Metric: "roas"}} },
		"rows summed":        func(b *Boards) { b.Boards[0].Tiles[0].Values[0] = Ref{DS: "spend", Agg: "sum"} },
		"odd type":           func(b *Boards) { b.Boards[0].Tiles[0].Type = "radar" },
		"odd times":          func(b *Boards) { b.Metrics[0].Times = 7 },
	}
	for name, spoil := range bad {
		b := Boards{Metrics: append([]Metric{}, good.Metrics...), Boards: []Board{{ID: "b1", Name: "Mine", Tiles: []Tile{good.Boards[0].Tiles[0], good.Boards[0].Tiles[1]}}}}
		b.Boards[0].Tiles[0].Values = []Ref{rev}
		b.Boards[0].Tiles[1].Values = []Ref{{Metric: "roas"}}
		spoil(&b)
		if err := st.SaveBoards(sh.ID, b); err == nil {
			t.Errorf("%s: saved", name)
		}
	}
}

func TestPlanBoard(t *testing.T) {
	needPython(t, false)
	reply := ""
	var prompts []string
	st := &Store{Dir: t.TempDir(), Model: func(p string, asJSON bool) (string, error) {
		prompts = append(prompts, p)
		if !asJSON {
			t.Error("plans are asked for as JSON")
		}
		if strings.Contains(p, "added this table") {
			return `{}`, nil
		}
		return reply, nil
	}}
	sh, _ := st.New("")
	add(t, st, sh.ID, "jan.csv", "feb.csv", "spend.csv")
	if _, err := st.Load(context.Background(), sh.ID); err != nil {
		t.Fatal(err)
	}
	prof, _ := st.Profile(sh.ID)
	if len(prof.Datasets) != 2 || prof.Datasets[0].Name != "jan + feb" || prof.Datasets[1].Name != "spend" {
		t.Fatalf("tables: %+v", prof.Datasets)
	}
	// Talking first: no board yet; a made-up total is caught and asked again.
	reply = `{"reply": "Your revenue was 99,999 so far. Shall I compare it with ad spend by week?", "board": null}`
	p, err := st.PlanBoard(context.Background(), sh.ID, []PlanTurn{{Role: "user", Text: "I want to see if ads pay off"}})
	if err != nil || p.Board != nil || len(p.Unchecked) != 1 || !strings.Contains(prompts[len(prompts)-1], "cannot know: 99,999") {
		t.Fatalf("talk: %+v %v", p, err)
	}
	reply = `{"reply": "Here it is: revenue, spend and their ratio by week, over 30 days or more.", "board": {"name": "Do ads pay off", "tiles": [
		{"title": "Revenue", "type": "kpi", "values": [{"ds": "jan + feb", "col": "Revenue (INR)", "agg": "sum"}]},
		{"title": "Revenue and spend by week", "type": "column", "by": "@week", "values": [{"ds": "jan + feb", "col": "Revenue (INR)", "agg": "sum"}, {"ds": "spend", "col": "Spend (INR)", "agg": "sum"}]},
		{"title": "ROAS by week", "type": "line", "by": "@week", "values": [{"metric": "m1"}]},
		{"title": "Profit", "type": "kpi", "values": [{"ds": "jan + feb", "col": "Profit", "agg": "sum"}]},
		{"title": "By campaign", "type": "bar", "by": "Campaign", "values": [{"ds": "jan + feb", "col": "Units", "agg": "sum"}]}
	]}, "metrics": [{"id": "m1", "name": "Return on ad spend", "kind": "number", "add": [{"ds": "jan + feb", "col": "Revenue (INR)", "agg": "sum"}], "per": [{"ds": "spend", "col": "Spend (INR)", "agg": "sum"}]}]}`
	p, err = st.PlanBoard(context.Background(), sh.ID, []PlanTurn{{Role: "user", Text: "yes"}})
	if err != nil || p.Board == nil || len(p.Unchecked) != 0 {
		t.Fatalf("plan: %+v %v", p, err)
	}
	if len(p.Board.Tiles) != 3 || len(p.Dropped) != 2 || len(p.Metrics) != 1 || p.Metrics[0].ID == "m1" || p.Board.Tiles[2].Values[0].Metric != p.Metrics[0].ID || p.Metrics[0].Times != 1 {
		t.Fatalf("checked plan: %+v dropped %v metrics %+v", p.Board, p.Dropped, p.Metrics)
	}
	if !strings.Contains(p.Dropped[0], `no column "Profit"`) || !strings.Contains(p.Dropped[1], `no column "Campaign" to split by`) {
		t.Fatalf("dropped: %v", p.Dropped)
	}
	// What it proposes can be kept as it is.
	if err := st.SaveBoards(sh.ID, Boards{Boards: []Board{*p.Board}, Metrics: p.Metrics}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PlanBoard(context.Background(), sh.ID, []PlanTurn{{Role: "ai", Text: "hi"}}); err == nil {
		t.Error("a plan without the user's words")
	}
}
