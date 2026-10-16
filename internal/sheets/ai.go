package sheets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ---------- what the data is (after each load) ----------

var (
	entityWords = regexp.MustCompile(`(?i)customer|client|buyer|product|item|title|campaign|name|sku|store|seller|lead|account|brand`)
	// Numbers that name things (a category number, a code, a year) are never added up.
	codeLike    = regexp.MustCompile(`(?i)categor|(^|[^a-z])(code|id|zip|pin|pincode|phone|year)s?([^a-z]|$)`)
	numericKind = map[string]bool{"number": true, "money": true, "percent": true}
)

// main is the dataset the dashboard opens on: the biggest that is not only
// a lookup already joined into another.
func mainDataset(p Profile) (Dataset, bool) {
	var best Dataset
	found := false
	for _, ds := range p.Datasets {
		if ds.JoinedInto != "" {
			continue
		}
		if !found || ds.Rows > best.Rows {
			best, found = ds, true
		}
	}
	return best, found
}

// guessRoles is code's own reading of the roles, used when no model answers.
func guessRoles(ds Dataset) Roles {
	r := Roles{Table: ds.Name}
	for _, kind := range []string{"money", "number"} {
		for _, c := range ds.Columns {
			if c.Kind == kind && len(r.Measures) < 4 && !codeLike.MatchString(c.Name) {
				r.Measures = append(r.Measures, c.Name)
			}
		}
	}
	for _, c := range ds.Columns {
		switch {
		case c.Kind == "date" && (r.Date == "" || c.Blanks < blanksOf(ds, r.Date)):
			r.Date = c.Name
		case (c.Kind == "category" || c.Kind == "yes_no") && c.Distinct >= 2 && c.Distinct <= 60:
			r.Dims = append(r.Dims, c.Name)
		case r.Entity == "" && (c.Kind == "text" || c.Kind == "category" || c.Kind == "id") && c.Distinct > 5 && entityWords.MatchString(c.Name):
			r.Entity = c.Name
		}
	}
	// Well-filled columns first: a column blank on most rows splits little.
	sort.SliceStable(r.Dims, func(i, j int) bool { return blanksOf(ds, r.Dims[i]) < blanksOf(ds, r.Dims[j]) })
	if len(r.Dims) > 5 {
		r.Dims = r.Dims[:5]
	}
	return r
}

func blanksOf(ds Dataset, name string) int {
	for _, c := range ds.Columns {
		if c.Name == name {
			return c.Blanks
		}
	}
	return 1 << 30
}

func column(ds Dataset, name string) (Column, bool) {
	for _, c := range ds.Columns {
		if c.Name == name {
			return c, true
		}
	}
	return Column{}, false
}

// describe asks a model what the data is about, which columns matter and
// what the user may want to ask. Its answer is checked against the columns.
func (st *Store) describe(p Profile) (Roles, string, []string) {
	ds, ok := mainDataset(p)
	if !ok {
		return Roles{}, "", nil
	}
	roles := guessRoles(ds)
	if st.Model == nil {
		return roles, "", nil
	}
	prompt := "A user added this table to Chargesheets, a dashboard tool for non-technical people.\n\n" + schema(Profile{Datasets: []Dataset{ds}}) +
		"\nReply with JSON only, no other text:\n" +
		`{"about": <one plain sentence: what this data is>, "measures": [<up to 4 columns worth adding up, most important first>], ` +
		`"date": <the main date column or null>, "dims": [<up to 5 columns worth splitting by>], "entity": <the column naming the main thing: customer, product, campaign; or null>, ` +
		`"questions": [<5 short questions this user would ask about this data, in plain words>]}` +
		"\nUse column names exactly as written. Never pick an id, phone or code column as a measure."
	text, err := st.Model(prompt, true)
	if err != nil {
		return roles, "", nil
	}
	var a struct {
		About     string   `json:"about"`
		Measures  []string `json:"measures"`
		Date      string   `json:"date"`
		Dims      []string `json:"dims"`
		Entity    string   `json:"entity"`
		Questions []string `json:"questions"`
	}
	if parseJSON(text, &a) != nil {
		return roles, "", nil
	}
	var ms []string
	for _, m := range a.Measures {
		if c, ok := column(ds, m); ok && numericKind[c.Kind] && !codeLike.MatchString(c.Name) && len(ms) < 4 && !contains(ms, m) {
			ms = append(ms, m)
		}
	}
	if len(ms) > 0 {
		roles.Measures = ms
	}
	if c, ok := column(ds, a.Date); ok && c.Kind == "date" {
		roles.Date = a.Date
	}
	var dims []string
	for _, m := range a.Dims {
		if c, ok := column(ds, m); ok && (c.Kind == "category" || c.Kind == "yes_no") && c.Distinct >= 2 && len(dims) < 5 && !contains(dims, m) {
			dims = append(dims, m)
		}
	}
	if len(dims) > 0 {
		roles.Dims = dims
	}
	if c, ok := column(ds, a.Entity); ok && !numericKind[c.Kind] && c.Kind != "date" {
		roles.Entity = a.Entity
	}
	var qs []string
	for _, q := range a.Questions {
		if q = strings.TrimSpace(q); q != "" && len(q) <= 140 && len(qs) < 5 {
			qs = append(qs, q)
		}
	}
	about := strings.TrimSpace(a.About)
	if len(about) > 240 {
		about = ""
	}
	return roles, about, qs
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// schema is the tables as a model reads them: names, kinds, ranges and the
// most common words. No rows.
func schema(p Profile) string {
	var b strings.Builder
	for _, ds := range p.Datasets {
		fmt.Fprintf(&b, "TABLE %s (%d rows) named \"%s\" for the user\n", ds.Table, ds.Rows, ds.Name)
		for _, c := range ds.Columns {
			typ := "TEXT"
			if numericKind[c.Kind] {
				typ = "REAL"
			}
			fmt.Fprintf(&b, "  %q %s %s", c.Name, typ, strings.ReplaceAll(c.Kind, "_", "/"))
			if c.Unit != "" {
				fmt.Fprintf(&b, " (%s)", c.Unit)
			}
			switch {
			case c.Kind == "date":
				fmt.Fprintf(&b, ", YYYY-MM-DD, %v to %v", c.Min, c.Max)
			case numericKind[c.Kind]:
				fmt.Fprintf(&b, ", %s to %s", num(c.Min), num(c.Max))
			case len(c.Top) > 0:
				var parts []string
				for _, t := range c.Top {
					if len(t) == 2 {
						parts = append(parts, fmt.Sprintf("%v (%s)", t[0], num(t[1])))
					}
				}
				fmt.Fprintf(&b, ", %d distinct: %s", c.Distinct, strings.Join(parts, ", "))
			case len(c.Examples) > 0:
				fmt.Fprintf(&b, ", %d distinct, e.g. %s", c.Distinct, strings.Join(c.Examples, "; "))
			}
			if c.Blanks > 0 {
				fmt.Fprintf(&b, ", %d blank", c.Blanks)
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

func num(v any) string {
	if f, ok := v.(float64); ok {
		return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.4f", f), "0"), ".")
	}
	return fmt.Sprint(v)
}

var fence = regexp.MustCompile("(?s)^.*?</think>")

// parseJSON reads the first JSON object in a model's answer.
func parseJSON(text string, v any) error {
	text = fence.ReplaceAllString(text, "")
	start := strings.IndexByte(text, '{')
	if start < 0 {
		return errors.New("no JSON")
	}
	dec := json.NewDecoder(strings.NewReader(text[start:]))
	return dec.Decode(v)
}

// modelError says in plain words why no model answered.
func modelError(err error) error {
	switch msg := err.Error(); {
	case strings.Contains(msg, "no_providers_connected"):
		return errors.New("no free AI is connected yet: add a key in Connections")
	case strings.Contains(msg, "connection refused"):
		return errors.New("MangoMan's router is not running: restart MangoMan")
	default:
		return errors.New("the free AI models are busy right now: try again in a minute")
	}
}

// ---------- Ask ----------

// Turn is one earlier question and answer in the chat.
type Turn struct {
	Q string `json:"q"`
	A string `json:"a"`
}

// Step is one query the model asked for.
type Step struct {
	Why    string  `json:"why"`
	SQL    string  `json:"sql"`
	Result *Result `json:"result,omitempty"`
	Error  string  `json:"error,omitempty"`
}

// Chart is how to draw one step's rows.
type Chart struct {
	Step int      `json:"step"` // 1-based
	Type string   `json:"type"` // bar, line, pie, table
	X    string   `json:"x"`
	Y    []string `json:"y"`
}

// Answer is what the user sees.
type Answer struct {
	Text      string   `json:"text"`
	Chart     *Chart   `json:"chart,omitempty"`
	Steps     []Step   `json:"steps"`
	Unchecked []string `json:"unchecked,omitempty"` // numbers that did not trace to a result
}

const maxQueries = 5

// Ask answers a question about a sheet: the model writes queries, code runs
// them, and the model explains the results. progress hears each step.
func (st *Store) Ask(ctx context.Context, id, question string, history []Turn, progress func(Step)) (Answer, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return Answer{}, errors.New("type a question")
	}
	if st.Model == nil {
		return Answer{}, errors.New("no free model is connected: add a key in Connections")
	}
	prof, err := st.Profile(id)
	if err != nil {
		return Answer{}, err
	}
	steps := []Step{}
	fix, retried := "", false
	for calls := 0; calls < maxQueries+3; calls++ {
		if ctx.Err() != nil {
			return Answer{}, ctx.Err()
		}
		text, err := st.Model(askPrompt(prof, question, history, steps, fix, len(steps) >= maxQueries), true)
		if err != nil {
			return Answer{}, modelError(err)
		}
		var r struct {
			Query  string `json:"query"`
			Why    string `json:"why"`
			Answer string `json:"answer"`
			Chart  *Chart `json:"chart"`
		}
		if parseJSON(text, &r) != nil {
			if fix == "" {
				fix = "Your last reply was not valid JSON. Reply with one JSON object only."
				continue
			}
			return Answer{}, errors.New("the free model's answer could not be read; ask again")
		}
		fix = ""
		if r.Query != "" && len(steps) < maxQueries {
			s := Step{Why: strings.TrimSpace(r.Why), SQL: strings.TrimSpace(r.Query)}
			res, err := st.Query(ctx, id, s.SQL)
			if err != nil {
				s.Error = err.Error()
			} else {
				s.Result = &res
			}
			steps = append(steps, s)
			if progress != nil {
				progress(s)
			}
			continue
		}
		if strings.TrimSpace(r.Answer) == "" {
			fix = "Reply with {\"answer\": ...} now."
			continue
		}
		a := Answer{Text: strings.TrimSpace(r.Answer), Steps: steps, Chart: validChart(r.Chart, steps)}
		// The column summary the model saw is code's counting too.
		a.Unchecked = Unchecked(a.Text, append(resultGroups(steps), []any{question}, []any{schema(prof)}))
		if len(a.Unchecked) > 0 && !retried {
			retried = true
			fix = "These numbers in your answer are not in the query results: " + strings.Join(a.Unchecked, ", ") +
				". Write the answer again using only numbers from the results, or run a query that computes them."
			continue
		}
		return a, nil
	}
	return Answer{Steps: steps}, errors.New("no answer after several tries; ask in other words")
}

// resultGroups are the values that belong together in the results: each
// column, and each row.
func resultGroups(steps []Step) [][]any {
	var groups [][]any
	for _, s := range steps {
		if s.Result == nil {
			continue
		}
		for j := range s.Result.Columns {
			col := make([]any, 0, len(s.Result.Rows))
			for _, row := range s.Result.Rows {
				if j < len(row) {
					col = append(col, row[j])
				}
			}
			groups = append(groups, col)
		}
		for _, row := range s.Result.Rows {
			groups = append(groups, row)
		}
	}
	return groups
}

func validChart(c *Chart, steps []Step) *Chart {
	if c == nil || c.Step < 1 || c.Step > len(steps) || steps[c.Step-1].Result == nil {
		return nil
	}
	cols := steps[c.Step-1].Result.Columns
	ok := map[string]bool{"bar": true, "line": true, "pie": true, "table": true}
	if !ok[c.Type] || !contains(cols, c.X) {
		return nil
	}
	var y []string
	for _, v := range c.Y {
		if contains(cols, v) && v != c.X {
			y = append(y, v)
		}
	}
	if len(y) == 0 && c.Type != "table" {
		return nil
	}
	c.Y = y
	return c
}

func askPrompt(p Profile, question string, history []Turn, steps []Step, fix string, last bool) string {
	var b strings.Builder
	b.WriteString("You are the data analyst inside Chargesheets. Answer the user's question about their data by querying it. " +
		"Never guess a number: every number in your answer must come from your query results.\n\n")
	b.WriteString("The data, in SQLite:\n")
	b.WriteString(schema(p))
	b.WriteString("\nSQL rules: SQLite only; one SELECT per query; quote column names with double quotes; dates are text YYYY-MM-DD, so use " +
		"strftime('%Y-%m', \"col\") for months and strftime('%Y', \"col\") for years; median(x) exists; add LIMIT 50 to long lists; " +
		"round money to 2 decimals and shares to 1. Compare words case-insensitively (LOWER) when matching what the user typed.\n")
	if len(history) > 0 {
		b.WriteString("\nThe chat so far (for follow-up questions):\n")
		for _, t := range history[max(0, len(history)-6):] {
			fmt.Fprintf(&b, "User: %s\nYou: %s\n", t.Q, clip(t.A, 600))
		}
	}
	fmt.Fprintf(&b, "\nThe question now: %s\n", question)
	if len(steps) > 0 {
		b.WriteString("\nYour queries so far and their results:\n")
		for i, s := range steps {
			fmt.Fprintf(&b, "Step %d (%s): %s\n", i+1, s.Why, s.SQL)
			if s.Error != "" {
				fmt.Fprintf(&b, "Error: %s\n", s.Error)
				continue
			}
			b.WriteString(table(s.Result, 40))
		}
	}
	b.WriteString("\nReply with JSON only, one of:\n")
	if !last {
		b.WriteString(`{"query": "<one SELECT>", "why": "<what it checks, a few plain words>"}` + "\n")
	}
	b.WriteString(`{"answer": "<the answer in plain short sentences for a non-technical user, with the numbers that matter>", ` +
		`"chart": {"step": <the step whose rows to draw>, "type": "bar|line|pie|table", "x": "<column>", "y": ["<column>"]}}` + "\n")
	b.WriteString("For a why question, compare the periods or groups by the columns that could explain it before answering. " +
		"If the data cannot answer, say what is missing in the answer. Leave out chart when no chart helps. Never say SQL, query or table in the answer.\n")
	if last {
		b.WriteString("You have used all your queries: answer now.\n")
	}
	if fix != "" {
		b.WriteString("\n" + fix + "\n")
	}
	return b.String()
}

func table(r *Result, n int) string {
	var b strings.Builder
	b.WriteString(strings.Join(r.Columns, " | ") + "\n")
	for i, row := range r.Rows {
		if i == n {
			fmt.Fprintf(&b, "(%d more rows not shown)\n", len(r.Rows)-n)
			break
		}
		cells := make([]string, len(row))
		for j, v := range row {
			if v == nil {
				cells[j] = "(blank)"
			} else {
				cells[j] = num(v)
			}
		}
		b.WriteString(strings.Join(cells, " | ") + "\n")
	}
	if len(r.Rows) == 0 {
		b.WriteString("(no rows)\n")
	}
	if r.More {
		b.WriteString("(stopped at 500 rows)\n")
	}
	return b.String()
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ---------- readout ----------

// factValues are the numbers of one fact line, as numbers, so a total or
// change of them can be checked like a column of results.
func factValues(f string) []any {
	vals := []any{f}
	for _, w := range numbersIn(f) {
		vals = append(vals, w.value)
	}
	return vals
}

// Readout is the written summary of a dashboard.
type Readout struct {
	Text      string   `json:"text"`
	Unchecked []string `json:"unchecked,omitempty"`
	Facts     string   `json:"facts"` // hash of the facts it was written from
}

// WriteReadout has a model write the readout from the facts the dashboard
// computed, checks its numbers, and keeps it for the same facts.
func (st *Store) WriteReadout(ctx context.Context, id string, facts []string, fresh bool) (Readout, error) {
	d, err := st.dir(id)
	if err != nil {
		return Readout{}, err
	}
	if len(facts) == 0 || len(facts) > 200 {
		return Readout{}, errors.New("no facts to write from")
	}
	h := sha256.Sum256([]byte(strings.Join(facts, "\n")))
	key := hex.EncodeToString(h[:8])
	path := filepath.Join(d, "readout.json")
	var saved Readout
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &saved) == nil && saved.Facts == key && !fresh {
		return saved, nil
	}
	if st.Model == nil {
		return Readout{}, errors.New("no free model is connected: add a key in Connections")
	}
	sh, _ := st.Get(id)
	groups := make([][]any, len(facts))
	for i, f := range facts {
		groups[i] = factValues(clip(f, 400))
	}
	prompt := "Write the readout of a dashboard for a busy, non-technical business owner. Our code computed these facts from their data (" +
		sh.Title + "):\n- " + strings.Join(facts, "\n- ") +
		"\n\nWrite four short sections with these headings, each line starting with ## for a heading or - for a point:\n" +
		"## In short\n## What is going well\n## What needs attention\n## What to do next\n" +
		"Two to four points a section, plain short sentences, most important first. Use only numbers from the facts, written the same way. " +
		"Never invent causes the facts do not show; say \"check\" when a cause is unknown. No other text."
	var out Readout
	for try := 0; try < 2; try++ {
		text, err := st.Model(prompt, false)
		if err != nil {
			return Readout{}, modelError(err)
		}
		text = strings.TrimSpace(fence.ReplaceAllString(text, ""))
		out = Readout{Text: text, Unchecked: Unchecked(text, groups), Facts: key}
		if len(out.Unchecked) == 0 {
			break
		}
		prompt += "\n\nYour last version had numbers not in the facts: " + strings.Join(out.Unchecked, ", ") + ". Use only the facts' numbers."
	}
	b, _ := json.Marshal(out)
	_ = os.WriteFile(path, b, 0o600)
	return out, nil
}
