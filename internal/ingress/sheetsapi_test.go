package ingress

import (
	"encoding/json"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/pusyc74-prog/mangoman/internal/sheets"
)

func TestSheetsAPI(t *testing.T) {
	if exec.Command("python3", "-c", "import sqlite3").Run() != nil {
		t.Skip("no python3")
	}
	h, s := dashServer(t, mem{})
	if w := call(h, "GET", "/mangoman/sheets", "127.0.0.1:4141", authz, ""); w.Code != 503 {
		t.Fatalf("without a store: %d", w.Code)
	}
	s.Sheets = &sheets.Store{Dir: t.TempDir(), Model: func(p string, _ bool) (string, error) {
		if strings.Contains(p, "data analyst") {
			if strings.Contains(p, "Step 1") {
				return `{"answer": "Oil sold 3 units."}`, nil
			}
			return `{"query": "SELECT SUM(\"Units\") AS units FROM shop WHERE \"Product\" = 'Oil'", "why": "units of oil"}`, nil
		}
		return `{"about": "Shop sales.", "questions": ["Which product sells most?"]}`, nil
	}}
	do := func(method, path, body string) *httptest.ResponseRecorder {
		return call(h, method, path, "127.0.0.1:4141", authz, body)
	}
	if w := call(h, "GET", "/mangoman/sheets", "127.0.0.1:4141", nil, ""); w.Code != 401 {
		t.Fatalf("no token: %d", w.Code)
	}
	w := do("POST", "/mangoman/sheets", `{"title": ""}`)
	var sh sheets.Sheet
	if json.Unmarshal(w.Body.Bytes(), &sh) != nil || sh.ID == "" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := do("POST", "/mangoman/sheets/"+sh.ID+"/files?name=shop.csv", "Product,Units\nSoap,4\nOil,3\nSoap,1\n"); w.Code != 200 {
		t.Fatalf("upload %d %s", w.Code, w.Body)
	}
	if w := do("POST", "/mangoman/sheets/"+sh.ID+"/files?name=notes.pdf", "x"); w.Code != 400 || !strings.Contains(w.Body.String(), "Chargesheets reads") {
		t.Fatalf("pdf: %d %s", w.Code, w.Body)
	}
	if w := do("POST", "/mangoman/sheets/"+sh.ID+"/google", `{"url": "https://example.com/x"}`); w.Code != 400 || !strings.Contains(w.Body.String(), "Paste the link") {
		t.Fatalf("bad link: %d %s", w.Code, w.Body)
	}
	w = do("POST", "/mangoman/sheets/"+sh.ID+"/load", "")
	var v sheetView
	if json.Unmarshal(w.Body.Bytes(), &v) != nil || v.Profile == nil || v.Sheet.Title != "Shop" || v.Sheet.About != "Shop sales." {
		t.Fatalf("load %d %s", w.Code, w.Body)
	}
	if w := do("GET", "/mangoman/sheets/"+sh.ID+"/data", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"words":["Soap","Oil"]`) {
		t.Fatalf("data %d %s", w.Code, w.Body)
	}
	w = do("POST", "/mangoman/sheets/"+sh.ID+"/ask", `{"question": "How many oil units?"}`)
	var job struct{ Job string }
	_ = json.Unmarshal(w.Body.Bytes(), &job)
	var got struct {
		Done   bool
		Answer *sheets.Answer
		Steps  []sheets.Step
		Error  string
	}
	for i := 0; i < 100 && !got.Done; i++ {
		time.Sleep(50 * time.Millisecond)
		_ = json.Unmarshal(do("GET", "/mangoman/sheets/"+sh.ID+"/ask/"+job.Job, "").Body.Bytes(), &got)
	}
	if got.Answer == nil || got.Answer.Text != "Oil sold 3 units." || len(got.Steps) != 1 {
		t.Fatalf("ask: %+v", got)
	}
	if w := do("PATCH", "/mangoman/sheets/"+sh.ID, `{"title": "My shop"}`); !strings.Contains(w.Body.String(), "My shop") {
		t.Fatalf("rename %s", w.Body)
	}
	if w := do("DELETE", "/mangoman/sheets/"+sh.ID, ""); w.Code != 200 {
		t.Fatalf("delete %d", w.Code)
	}
	if w := do("GET", "/mangoman/sheets/"+sh.ID, ""); w.Code != 404 {
		t.Fatalf("after delete %d", w.Code)
	}
}
