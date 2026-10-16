package sheets

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Custom dashboards: the user's own boards of tiles and own metrics. The
// page counts every tile from the data; this file keeps them, checks their
// shape, and lets a model draft one while talking it through with the user.

// Ref is one value a tile or metric shows: a column added up (or averaged,
// counted...) in one table, the table's rows, or an own metric.
type Ref struct {
	DS     string `json:"ds,omitempty"`
	Col    string `json:"col,omitempty"` // "" = rows
	Agg    string `json:"agg,omitempty"`
	Metric string `json:"metric,omitempty"`
}

// Filter keeps the rows whose column is one of the values.
type Filter struct {
	Col    string   `json:"col"`
	Values []string `json:"values"`
}

// Tile is one chart or number on a board.
type Tile struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Type    string   `json:"type"`
	Values  []Ref    `json:"values"`
	By      string   `json:"by,omitempty"` // "", @day ... @year, or a column name
	Filters []Filter `json:"filters,omitempty"`
	Top     int      `json:"top,omitempty"`
	Size    string   `json:"size,omitempty"`
}

// Metric is the user's own measure: (sum of Add - sum of Minus) / sum of Per x Times.
type Metric struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Kind  string  `json:"kind"`
	Add   []Ref   `json:"add"`
	Minus []Ref   `json:"minus,omitempty"`
	Per   []Ref   `json:"per,omitempty"`
	Times float64 `json:"times,omitempty"`
}

// Board is one custom dashboard.
type Board struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Tiles []Tile `json:"tiles"`
}

// Boards is everything a sheet keeps in boards.json.
type Boards struct {
	Boards  []Board  `json:"boards"`
	Metrics []Metric `json:"metrics"`
}

var (
	tileTypes  = map[string]bool{"kpi": true, "line": true, "column": true, "bar": true, "pie": true, "table": true}
	aggs       = map[string]bool{"sum": true, "avg": true, "min": true, "max": true, "count": true, "distinct": true}
	grains     = map[string]bool{"@day": true, "@week": true, "@month": true, "@quarter": true, "@year": true}
	sizes      = map[string]bool{"": true, "s": true, "m": true, "l": true}
	metricKind = map[string]bool{"money": true, "number": true, "percent": true}
	boardID    = regexp.MustCompile(`^[a-z0-9]{1,16}$`)
)

func newID() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func short(s string, n int) bool { return strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= n }

// checkRef checks a value's shape and, with a profile, that its table and
// column are there and can be counted that way.
func checkRef(r Ref, metrics map[string]Metric, prof *Profile, inMetric bool) error {
	if r.Metric != "" {
		if inMetric {
			return errors.New("a metric cannot use another metric")
		}
		if _, ok := metrics[r.Metric]; !ok {
			return fmt.Errorf("no metric %q", r.Metric)
		}
		return nil
	}
	if !aggs[r.Agg] || r.DS == "" || len(r.DS) > 200 || len(r.Col) > 200 {
		return errors.New("a value needs a table, a column and how to count it")
	}
	if r.Col == "" && r.Agg != "count" {
		return errors.New("rows can only be counted")
	}
	if prof == nil {
		return nil
	}
	ds, ok := datasetNamed(*prof, r.DS)
	if !ok {
		return fmt.Errorf("no table %q", r.DS)
	}
	if r.Col == "" {
		return nil
	}
	c, ok := column(ds, r.Col)
	if !ok {
		return fmt.Errorf("no column %q in %s", r.Col, r.DS)
	}
	if !numericKind[c.Kind] && r.Agg != "count" && r.Agg != "distinct" {
		return fmt.Errorf("%s holds words: it can be counted, not added up", r.Col)
	}
	return nil
}

func datasetNamed(p Profile, name string) (Dataset, bool) {
	for _, ds := range p.Datasets {
		if ds.Name == name {
			return ds, true
		}
	}
	return Dataset{}, false
}

func checkMetric(m Metric, prof *Profile) error {
	if !boardID.MatchString(m.ID) || !short(m.Name, 60) || !metricKind[m.Kind] || len(m.Add) == 0 ||
		len(m.Add) > 6 || len(m.Minus) > 6 || len(m.Per) > 6 || (m.Times != 0 && m.Times != 1 && m.Times != 100) {
		return errors.New("a metric needs a name, a kind and up to 6 values to add up")
	}
	for _, part := range [][]Ref{m.Add, m.Minus, m.Per} {
		for _, r := range part {
			if err := checkRef(r, nil, prof, true); err != nil {
				return fmt.Errorf("%s: %v", m.Name, err)
			}
		}
	}
	return nil
}

// checkTile checks a tile; with a profile also that what it splits by is there.
func checkTile(t Tile, metrics map[string]Metric, prof *Profile) error {
	if !boardID.MatchString(t.ID) || !short(t.Title, 80) || !tileTypes[t.Type] || len(t.Values) == 0 || len(t.Values) > 4 ||
		!sizes[t.Size] || t.Top < 0 || t.Top > 50 || len(t.Filters) > 5 || len(t.By) > 200 {
		return errors.New("a chart needs a title, a type and one to four values")
	}
	for _, r := range t.Values {
		if err := checkRef(r, metrics, prof, false); err != nil {
			return fmt.Errorf("%s: %v", t.Title, err)
		}
	}
	for _, f := range t.Filters {
		if f.Col == "" || len(f.Values) == 0 || len(f.Values) > 200 {
			return fmt.Errorf("%s: a filter needs a column and its values", t.Title)
		}
	}
	if (t.Type == "kpi") != (t.By == "") || t.Type == "line" && !grains[t.By] {
		return fmt.Errorf("%s: a number tile has no split; a line splits by time; other charts by time or a column", t.Title)
	}
	if prof == nil {
		return nil
	}
	var all []Dataset
	for _, r := range t.Values {
		all = append(all, refTables(r, metrics, *prof)...)
	}
	for _, f := range t.Filters {
		found := false
		for _, ds := range all {
			_, ok := column(ds, f.Col)
			found = found || ok
		}
		if !found {
			return fmt.Errorf("%s: no column %q to filter by", t.Title, f.Col)
		}
	}
	if t.By == "" {
		return nil
	}
	for _, r := range t.Values {
		for _, ds := range refTables(r, metrics, *prof) {
			if grains[t.By] && !hasKind(ds, "date") {
				return fmt.Errorf("%s: %s has no dates to split by", t.Title, ds.Name)
			}
			if _, ok := column(ds, t.By); !grains[t.By] && !ok {
				return fmt.Errorf("%s: %s has no column %q to split by", t.Title, ds.Name, t.By)
			}
		}
	}
	return nil
}

// refTables are the tables a value counts in (a metric's, for a metric).
func refTables(r Ref, metrics map[string]Metric, p Profile) []Dataset {
	var out []Dataset
	refs := []Ref{r}
	if m, ok := metrics[r.Metric]; ok {
		refs = append(append(append([]Ref{}, m.Add...), m.Minus...), m.Per...)
	}
	for _, x := range refs {
		if ds, ok := datasetNamed(p, x.DS); ok {
			out = append(out, ds)
		}
	}
	return out
}

func hasKind(ds Dataset, kind string) bool {
	for _, c := range ds.Columns {
		if c.Kind == kind {
			return true
		}
	}
	return false
}

// check checks the shape of everything a sheet keeps; columns are not
// checked, so a board survives a column leaving the data (the page says so).
func (b Boards) check() error {
	if len(b.Boards) > 20 || len(b.Metrics) > 30 {
		return errors.New("up to 20 dashboards and 30 metrics")
	}
	metrics := map[string]Metric{}
	for _, m := range b.Metrics {
		if err := checkMetric(m, nil); err != nil {
			return err
		}
		if _, ok := metrics[m.ID]; ok {
			return errors.New("two metrics share an id")
		}
		metrics[m.ID] = m
	}
	ids := map[string]bool{}
	for _, bd := range b.Boards {
		if !boardID.MatchString(bd.ID) || !short(bd.Name, 60) || len(bd.Tiles) > 24 || ids[bd.ID] {
			return errors.New("a dashboard needs a name and holds up to 24 charts")
		}
		ids[bd.ID] = true
		for _, t := range bd.Tiles {
			if err := checkTile(t, metrics, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// GetBoards reads a sheet's dashboards.
func (st *Store) GetBoards(id string) (Boards, error) {
	b := Boards{Boards: []Board{}, Metrics: []Metric{}}
	d, err := st.dir(id)
	if err != nil {
		return b, err
	}
	raw, err := os.ReadFile(filepath.Join(d, "boards.json"))
	if err == nil && json.Unmarshal(raw, &b) != nil {
		return b, errors.New("the saved dashboards could not be read")
	}
	return b, nil
}

// SaveBoards checks and keeps a sheet's dashboards.
func (st *Store) SaveBoards(id string, b Boards) error {
	d, err := st.dir(id)
	if err != nil {
		return err
	}
	if err := b.check(); err != nil {
		return err
	}
	l := st.lock(id + " boards") // not the sheet's lock: a refresh may hold that for minutes
	l.Lock()
	defer l.Unlock()
	raw, _ := json.Marshal(b)
	if len(raw) > 512<<10 {
		return errors.New("these dashboards are too big to keep")
	}
	p := filepath.Join(d, "boards.json")
	if err := os.WriteFile(p+".tmp", raw, 0o600); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}

// ---------- planning a dashboard with a model ----------

// PlanTurn is one message of the planning chat.
type PlanTurn struct {
	Role string `json:"role"` // user or ai
	Text string `json:"text"`
}

// Plan is the model's reply, with the dashboard it proposes once checked.
type Plan struct {
	Reply     string   `json:"reply"`
	Board     *Board   `json:"board,omitempty"`
	Metrics   []Metric `json:"metrics,omitempty"`
	Dropped   []string `json:"dropped,omitempty"` // parts left out, and why
	Unchecked []string `json:"unchecked,omitempty"`
}

// PlanBoard talks a dashboard through with the user and proposes one built
// only from the columns that are there.
func (st *Store) PlanBoard(ctx context.Context, id string, chat []PlanTurn) (Plan, error) {
	if len(chat) == 0 || chat[len(chat)-1].Role != "user" || strings.TrimSpace(chat[len(chat)-1].Text) == "" {
		return Plan{}, errors.New("type what you want to see")
	}
	if st.Model == nil {
		return Plan{}, errors.New("no free model is connected: add a key in Connections")
	}
	prof, err := st.Profile(id)
	if err != nil {
		return Plan{}, err
	}
	saved, _ := st.GetBoards(id)
	var tables Profile
	for _, ds := range prof.Datasets {
		if ds.JoinedInto == "" {
			tables.Datasets = append(tables.Datasets, ds)
		}
	}
	fix := ""
	for try := 0; try < 2; try++ {
		if ctx.Err() != nil {
			return Plan{}, ctx.Err()
		}
		text, err := st.Model(planPrompt(tables, saved.Metrics, chat, fix), true)
		if err != nil {
			return Plan{}, modelError(err)
		}
		var r struct {
			Reply   string   `json:"reply"`
			Board   *Board   `json:"board"`
			Metrics []Metric `json:"metrics"`
		}
		if parseJSON(text, &r) != nil || strings.TrimSpace(r.Reply) == "" {
			fix = "Your last reply was not the JSON asked for. Reply with one JSON object only."
			continue
		}
		p := Plan{Reply: strings.TrimSpace(r.Reply)}
		words := p.Reply
		if r.Board != nil {
			p.Board, p.Metrics, p.Dropped = cleanPlan(*r.Board, r.Metrics, saved.Metrics, prof)
		}
		if p.Board != nil {
			words += "\n" + p.Board.Name
			for _, t := range p.Board.Tiles {
				words += "\n" + t.Title
			}
			for _, m := range p.Metrics {
				words += "\n" + m.Name
			}
		}
		// It sees the column summary only; any number it writes must be from
		// there, the user's words, or the usual periods.
		groups := [][]any{{schema(tables)}, {7.0, 14.0, 28.0, 30.0, 60.0, 90.0, 100.0, 180.0, 365.0}}
		for _, t := range chat {
			if t.Role == "user" {
				groups = append(groups, []any{t.Text})
			}
		}
		p.Unchecked = Unchecked(words, groups)
		if len(p.Unchecked) > 0 && try == 0 {
			fix = "Your reply had numbers about the data that you cannot know: " + strings.Join(p.Unchecked, ", ") + ". Write it again without them."
			continue
		}
		return p, nil
	}
	return Plan{}, errors.New("the free model's answer could not be read; say it again in other words")
}

// cleanPlan keeps the parts of a proposed dashboard that fit the data, with
// fresh ids, and says what was left out.
func cleanPlan(b Board, proposed, saved []Metric, prof Profile) (*Board, []Metric, []string) {
	var dropped []string
	known := map[string]Metric{}
	for _, m := range saved {
		known[m.ID] = m
	}
	rename := map[string]string{}
	var metrics []Metric
	for _, m := range proposed {
		old := m.ID
		m.ID = "m" + newID()
		if m.Times == 0 {
			m.Times = 1
		}
		if err := checkMetric(m, &prof); err != nil {
			dropped = append(dropped, err.Error())
			continue
		}
		rename[old] = m.ID
		known[m.ID] = m
		metrics = append(metrics, m)
	}
	out := Board{ID: newID(), Name: strings.TrimSpace(b.Name), Tiles: []Tile{}}
	if !short(out.Name, 60) {
		out.Name = "My dashboard"
	}
	for _, t := range b.Tiles {
		if len(out.Tiles) == 12 {
			break
		}
		t.ID = newID()
		for i, r := range t.Values {
			if n, ok := rename[r.Metric]; ok {
				t.Values[i].Metric = n
			}
		}
		if t.Size == "" && t.Type != "kpi" {
			t.Size = "m"
		}
		if err := checkTile(t, known, &prof); err != nil {
			dropped = append(dropped, err.Error())
			continue
		}
		out.Tiles = append(out.Tiles, t)
	}
	if len(out.Tiles) == 0 {
		return nil, nil, dropped
	}
	return &out, metrics, dropped
}

func planPrompt(p Profile, metrics []Metric, chat []PlanTurn, fix string) string {
	var b strings.Builder
	b.WriteString("You help a busy, non-technical business owner plan a custom dashboard in Chargesheets. Talk it through like an analyst: " +
		"say what is worth watching for this business and why, ask one short question when their wish is unclear, " +
		"and propose a dashboard once you know enough (or when they ask for one). You see the columns only, never the rows: " +
		"never state totals, counts or other numbers about their data.\n\nTheir data (each TABLE is named for the user in quotes):\n")
	b.WriteString(schema(p))
	if len(metrics) > 0 {
		b.WriteString("\nTheir own metrics, usable by id:\n")
		for _, m := range metrics {
			fmt.Fprintf(&b, "  %s: %s (%s)\n", m.ID, m.Name, m.Kind)
		}
	}
	b.WriteString(`
A dashboard: {"name": "<short>", "tiles": [<2 to 10 tiles>]}
A tile: {"title": "<short>", "type": "kpi|line|column|bar|pie|table", "values": [<1 to 3 values>], "by": "<split>", "top": <n for bar, pie, table>, "size": "s|m|l"}
A value: {"ds": "<table name for the user>", "col": "<column>", "agg": "sum|avg|min|max|count|distinct"}, or rows: {"ds": "<table>", "agg": "count"}, or {"metric": "<metric id>"}
"by": "" for kpi (one number); "@day", "@week", "@month", "@quarter" or "@year" for line and column; a column name for bar, pie and table.
Values from different tables can share a tile when split by time, or by a column with the same name in each table.
An own metric: {"id": "m1", "name": "<name>", "kind": "money|number|percent", "add": [<values>], "minus": [<values>], "per": [<values>], "times": 1 or 100}
It means (sum of add - sum of minus) / sum of per, times "times". Return on ad spend = add [sales] per [ad spend columns]; margin % = add [sales] minus [costs] per [sales] times 100.
Use table and column names exactly as written. Add up money and counts; average prices and rates.

The chat so far:
`)
	for _, t := range chat[max(0, len(chat)-10):] {
		who := "User"
		if t.Role != "user" {
			who = "You"
		}
		fmt.Fprintf(&b, "%s: %s\n", who, clip(t.Text, 1500))
	}
	b.WriteString(`
Reply with JSON only: {"reply": "<your answer in plain short sentences>", "board": <a dashboard, or null while still talking>, "metrics": [<own metrics the board uses>]}`)
	if fix != "" {
		b.WriteString("\n\n" + fix)
	}
	return b.String()
}
