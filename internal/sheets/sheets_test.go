package sheets

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnchecked(t *testing.T) {
	vals := []any{437300.0, 177200.0, 57000.0, "2026-09-01", "Amazon"}
	cases := []struct {
		text string
		bad  []string
	}{
		{"Amazon fell from ₹1,77,200 to ₹57,000.", nil},
		{"Amazon fell by 1,20,200 (68%).", nil},    // a difference and a change of two known numbers
		{"Total ₹4.37 lakh, about 4.4 lakh.", nil}, // rounded as written
		{"Sales were 4,12,000 in September 2026.", []string{"4,12,000"}},
		{"1. Top 3 channels\n2. Next", nil}, // list numbers and small counts
		{"It grew 250% to 9,99,999.", []string{"250", "9,99,999"}},
	}
	for _, c := range cases {
		if got := Unchecked(c.text, vals); strings.Join(got, "|") != strings.Join(c.bad, "|") {
			t.Errorf("%q: unchecked %v, want %v", c.text, got, c.bad)
		}
	}
}

func TestGoogleID(t *testing.T) {
	for link, want := range map[string]string{
		"https://docs.google.com/spreadsheets/d/1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms/edit#gid=0":   "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms",
		"https://docs.google.com/spreadsheets/u/1/d/1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms/edit":     "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms",
		"http://docs.google.com/spreadsheets/d/1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms":               "",
		"https://evil.example/docs.google.com/spreadsheets/d/1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms": "",
		"https://docs.google.com/document/d/1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms":                  "",
	} {
		if got := GoogleID(link); got != want {
			t.Errorf("GoogleID(%q) = %q, want %q", link, got, want)
		}
	}
}

// needPython skips when python3 (and, for Excel files, openpyxl) is missing.
func needPython(t *testing.T, excel bool) {
	t.Helper()
	args := []string{"-c", "import sqlite3"}
	if excel {
		args = []string{"-c", "import openpyxl"}
	}
	if err := exec.Command("python3", args...).Run(); err != nil {
		t.Skip("python3 with openpyxl is not here; MangoMan's Python has it")
	}
}

func add(t *testing.T, st *Store, id string, names ...string) {
	t.Helper()
	for _, n := range names {
		f, err := os.Open(filepath.Join("testdata", n))
		if err != nil {
			t.Fatal(err)
		}
		_, err = st.AddFile(id, n, f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadReadsMessyFiles(t *testing.T) {
	needPython(t, true)
	st := &Store{Dir: t.TempDir()}
	sh, err := st.New("")
	if err != nil {
		t.Fatal(err)
	}
	add(t, st, sh.ID, "sales.xlsx", "jan.csv", "feb.csv", "ads.xlsx")
	if sh, err = st.Load(context.Background(), sh.ID); err != nil {
		t.Fatal(err)
	}
	if sh.Title != "sales" || sh.Roles.Table != "sales · Orders" || sh.Rows != 120 {
		t.Fatalf("title %q table %q rows %d", sh.Title, sh.Roles.Table, sh.Rows)
	}
	p, err := st.Profile(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Dataset{}
	for _, ds := range p.Datasets {
		byName[ds.Name] = ds
	}
	orders := byName["sales · Orders"]
	kinds := map[string]string{}
	for _, c := range orders.Columns {
		kinds[c.Name] = c.Kind + "/" + c.Unit
	}
	want := map[string]string{"Order ID": "id/", "Order Date": "date/", "Customer ID": "id/", "City": "category/",
		"Amount": "money/INR", "Qty": "number/", "Customers · Segment": "category/"}
	for k, v := range want {
		if kinds[k] != v {
			t.Errorf("%s read as %q, want %q", k, kinds[k], v)
		}
	}
	notes := strings.Join(orders.Notes, " ")
	for _, w := range []string{"Skipped 2 title rows", "Left out 1 total row", "Linked sales · Customers by Customer ID: 100.0%"} {
		if !strings.Contains(notes, w) {
			t.Errorf("notes %q lack %q", notes, w)
		}
	}
	if c, _ := column(orders, "City"); c.Merged != 2 || c.Distinct != 3 {
		t.Errorf("City: merged %d distinct %d, want 2 and 3 (Mumbai written three ways)", c.Merged, c.Distinct)
	}
	if ds := byName["jan + feb"]; ds.Rows != 20 || len(ds.Origins) != 2 {
		t.Errorf("stacked months: %+v", ds)
	}
	ads := byName["ads"]
	if _, ok := column(ads, "Jul Spend"); !ok {
		t.Errorf("two header rows not joined: %+v", ads.Columns)
	}
	// The money total matches the file's own Total row (₹12,77,400).
	r, err := st.Query(context.Background(), sh.ID, `SELECT SUM("Amount") FROM sales_orders`)
	if err != nil || len(r.Rows) != 1 || r.Rows[0][0] != 1277400.0 {
		t.Fatalf("sum %v %v", r, err)
	}
	for _, bad := range []string{"DROP TABLE sales_orders", "SELECT * FROM pragma_table_info('sales_orders')", "SELECT 1; SELECT 2", "ATTACH 'x' AS y"} {
		if _, err := st.Query(context.Background(), sh.ID, bad); err == nil {
			t.Errorf("%q ran", bad)
		}
	}
	// A correction from the check screen is honoured.
	if _, err := st.SetChoices(context.Background(), sh.ID, Choices{Kinds: map[string]string{"sales · Orders/Qty": "money"}, NoLink: []string{p.Links[0].ID}}); err != nil {
		t.Fatal(err)
	}
	p, _ = st.Profile(sh.ID)
	for _, ds := range p.Datasets {
		if ds.Name == "sales · Orders" {
			if c, _ := column(ds, "Qty"); c.Kind != "money" {
				t.Errorf("Qty kind %q after choosing money", c.Kind)
			}
			if _, ok := column(ds, "Customers · Segment"); ok {
				t.Error("link stayed on after turning it off")
			}
		}
	}
}

func TestAddFileRefusesOtherKinds(t *testing.T) {
	st := &Store{Dir: t.TempDir()}
	sh, _ := st.New("x")
	for _, n := range []string{"a.xls", "a.pdf", ".hidden.csv"} {
		if _, err := st.AddFile(sh.ID, n, strings.NewReader("x")); err == nil {
			t.Errorf("%s accepted", n)
		}
	}
	if _, err := st.AddFile(sh.ID, "../../x.csv", strings.NewReader("a,b\n1,2\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(st.Dir, sh.ID, "files", "x.csv")); err != nil {
		t.Errorf("file not kept inside the sheet: %v", err)
	}
	if _, err := st.Get("../x"); err == nil {
		t.Error("odd id accepted")
	}
}

func TestGoogleSheetAndRefresh(t *testing.T) {
	needPython(t, true)
	data, _ := os.ReadFile(filepath.Join("testdata", "sales.xlsx"))
	served := data
	st := &Store{Dir: t.TempDir(), Fetch: func(_ context.Context, id string) ([]byte, string, error) {
		if id != "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms" {
			return nil, "", errNotShared
		}
		return served, "Kesar sales", nil
	}}
	sh, _ := st.New("")
	if _, err := st.AddGoogle(context.Background(), sh.ID, "https://docs.google.com/spreadsheets/d/nope-nope-nope-nope-nope/edit"); !errors.Is(err, errNotShared) {
		t.Fatalf("unshared sheet: %v", err)
	}
	sh, err := st.AddGoogle(context.Background(), sh.ID, "https://docs.google.com/spreadsheets/d/1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms/edit#gid=0")
	if err != nil || sh.Title != "Kesar sales" || !sh.Live() {
		t.Fatalf("%+v %v", sh, err)
	}
	if _, err := st.Load(context.Background(), sh.ID); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := st.Refresh(context.Background(), sh.ID); err != nil || changed {
		t.Fatalf("unchanged sheet: changed %v err %v", changed, err)
	}
	other, _ := os.ReadFile(filepath.Join("testdata", "ads.xlsx"))
	served = other
	sh, changed, err := st.Refresh(context.Background(), sh.ID)
	if err != nil || !changed || sh.Changed == "" || sh.Roles.Table != "Kesar sales" {
		t.Fatalf("changed sheet: %v %q %q %v", changed, sh.Changed, sh.Roles.Table, err)
	}
}

func TestAskLoop(t *testing.T) {
	needPython(t, true)
	st := &Store{Dir: t.TempDir()}
	sh, _ := st.New("")
	add(t, st, sh.ID, "sales.xlsx")
	if _, err := st.Load(context.Background(), sh.ID); err != nil {
		t.Fatal(err)
	}
	replies := []string{
		"Let me look.\n" + `{"query": "SELECT \"Channel\", SUM(\"Amount\") AS total FROM sales_orders GROUP BY 1 ORDER BY 2 DESC", "why": "sales by channel"}`,
		`{"answer": "Amazon sold the most: ₹9,99,999.", "chart": {"step": 1, "type": "bar", "x": "Channel", "y": ["total"]}}`,
		`{"answer": "Amazon sold the most: ₹4,37,300.", "chart": {"step": 1, "type": "bar", "x": "Channel", "y": ["total", "nope"]}}`,
	}
	var prompts []string
	st.Model = func(p string) (string, error) {
		prompts = append(prompts, p)
		r := replies[0]
		replies = replies[1:]
		return r, nil
	}
	var seen []Step
	a, err := st.Ask(context.Background(), sh.ID, "Which channel sold the most?", nil, func(s Step) { seen = append(seen, s) })
	if err != nil {
		t.Fatal(err)
	}
	if a.Text != "Amazon sold the most: ₹4,37,300." || len(a.Unchecked) != 0 || len(seen) != 1 {
		t.Fatalf("answer %+v", a)
	}
	if a.Chart == nil || a.Chart.X != "Channel" || strings.Join(a.Chart.Y, ",") != "total" {
		t.Errorf("chart %+v", a.Chart)
	}
	if !strings.Contains(prompts[0], `"Amount" REAL money (INR)`) || !strings.Contains(prompts[1], "Amazon | 437300") ||
		!strings.Contains(prompts[2], "not in the query results: 9,99,999") {
		t.Errorf("prompts:\n%s", strings.Join(prompts, "\n-----\n"))
	}
	// A failing query is shown to the model, which can fix it.
	replies = []string{`{"query": "SELECT nope FROM sales_orders", "why": "x"}`, `{"answer": "I could not find that."}`}
	a, err = st.Ask(context.Background(), sh.ID, "x?", []Turn{{Q: "Which channel sold the most?", A: "Amazon"}}, nil)
	if err != nil || a.Steps[0].Error == "" || !strings.Contains(prompts[len(prompts)-1], "Error: no such column") {
		t.Fatalf("%+v %v", a, err)
	}
}

func TestDescribeChecksTheModel(t *testing.T) {
	needPython(t, true)
	st := &Store{Dir: t.TempDir(), Model: func(string) (string, error) {
		return `{"about": "Orders of a soap brand.", "measures": ["Order ID", "Amount", "Nope"], "date": "City", "dims": ["Channel", "Amount"],
			"entity": "Customers · Name", "questions": ["Which channel sells most?", ""]}`, nil
	}}
	sh, _ := st.New("")
	add(t, st, sh.ID, "sales.xlsx")
	sh, err := st.Load(context.Background(), sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	r := sh.Roles
	if strings.Join(r.Measures, ",") != "Amount" || r.Date != "Order Date" || strings.Join(r.Dims, ",") != "Channel" ||
		r.Entity != "Customers · Name" || sh.About != "Orders of a soap brand." || len(sh.Questions) != 1 {
		t.Errorf("roles %+v about %q questions %v", r, sh.About, sh.Questions)
	}
}
