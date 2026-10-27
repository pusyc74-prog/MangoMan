package ingress

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pusyc74-prog/mangoman/internal/buffer"
)

// A stand-in for Buffer's API: one Instagram and one X channel; a draft
// with a time is refused once, as Buffer may do, to test the fallback.
func fakeBuffer(t *testing.T) *[]string {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Query string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		seen = append(seen, in.Query)
		switch {
		case strings.Contains(in.Query, "organizations"):
			_, _ = w.Write([]byte(`{"data":{"account":{"organizations":[{"id":"o1"}]}}}`))
		case strings.Contains(in.Query, "channels"):
			_, _ = w.Write([]byte(`{"data":{"channels":[{"id":"ig","name":"kesari","service":"instagram"},{"id":"tw","name":"kesari","service":"twitter"}]}}`))
		case strings.Contains(in.Query, `channelId: "busy"`):
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
		case strings.Contains(in.Query, "dueAt") && strings.Contains(in.Query, `channelId: "tw"`):
			_, _ = w.Write([]byte(`{"data":{"createPost":{"message":"Drafts cannot have a due date"}}}`))
		default:
			_, _ = w.Write([]byte(`{"data":{"createPost":{"post":{"id":"d1"}}}}`))
		}
	}))
	old := buffer.URL
	buffer.URL = srv.URL
	t.Cleanup(func() { buffer.URL = old; srv.Close() })
	return &seen
}

func TestBuffer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("MANGOMAN_HOME", t.TempDir())
	seen := fakeBuffer(t)
	st := mem{}
	h, s := dashServer(t, st)
	s.Secrets = st
	host := "127.0.0.1:4141"
	if w := call(h, "GET", "/mangoman/buffer", host, authz, ""); !strings.Contains(w.Body.String(), `"connected": false`) {
		t.Fatalf("before connecting: %s", w.Body)
	}
	if w := call(h, "PUT", "/mangoman/buffer", host, authz, `{"key":"wrong"}`); w.Code != 502 || st[bufferSecret] != "" {
		t.Fatalf("a refused key: %d %s", w.Code, w.Body)
	}
	if w := call(h, "PUT", "/mangoman/buffer", host, authz, `{"key":" good "}`); w.Code != 200 || st[bufferSecret] != "good" || !strings.Contains(w.Body.String(), `"twitter"`) {
		t.Fatalf("connect: %d %s", w.Code, w.Body)
	}
	task := filepath.Join(home, "MangoMan Projects", "diwali-posts")
	_ = os.MkdirAll(filepath.Join(task, "posts"), 0o755)
	_ = os.WriteFile(filepath.Join(task, "posts", "calendar.csv"), []byte("\uFEFFDate,Time,Platform,Post,Images,Caption\r\n"+
		"2026-11-02,18:30,instagram,Season opening,season-open-portrait.png,\"Alphonso season is open.\nOrder now.\"\r\n"+
		"2026-11-02,18:30,x,Season opening,season-open-landscape.png,Alphonso season is open.\r\n"+
		"2026-11-03,,whatsapp,Status,s.png,Hi\r\n"), 0o644)
	_ = os.WriteFile(filepath.Join(task, "posts", "posts.spec.json"), []byte(`{"timezone":"Asia/Kolkata"}`), 0o644)
	var files struct{ Items []ProjectItem }
	_ = json.Unmarshal(call(h, "GET", "/mangoman/files", host, authz, "").Body.Bytes(), &files)
	if len(files.Items) != 1 || !files.Items[0].Calendar {
		t.Fatalf("My Files does not show the calendar: %+v", files.Items)
	}
	var plan struct {
		Timezone string
		Rows     []PlanRow
	}
	w := call(h, "GET", "/mangoman/buffer/plan?folder=diwali-posts", host, authz, "")
	if _ = json.Unmarshal(w.Body.Bytes(), &plan); w.Code != 200 || len(plan.Rows) != 3 || plan.Timezone != "Asia/Kolkata" {
		t.Fatalf("plan: %d %s", w.Code, w.Body)
	}
	if r := plan.Rows[0]; r.Due != "2026-11-02T13:00:00Z" || r.Service != "instagram" || r.Caption != "Alphonso season is open.\nOrder now." {
		t.Fatalf("first row: %+v", r)
	}
	if plan.Rows[1].Service != "twitter" || plan.Rows[2].Service != "" || plan.Rows[2].Due != "" {
		t.Fatalf("X maps to twitter; WhatsApp has no Buffer channel and no time: %+v", plan.Rows[1:])
	}
	if w := call(h, "GET", "/mangoman/buffer/plan?folder=..%2F..", host, authz, ""); w.Code != 404 {
		t.Fatalf("a folder outside MangoMan Projects: %d", w.Code)
	}
	w = call(h, "POST", "/mangoman/buffer/drafts", host, authz, `{"drafts":[{"channel":"ig","text":"Alphonso","due":"2026-11-02T13:00:00Z"},`+
		`{"channel":"tw","text":"Alphonso","due":"2026-11-02T13:00:00Z"},{"channel":"","text":"x"}]}`)
	var res struct {
		Results []struct {
			OK          bool
			Note, Error string
		}
	}
	if _ = json.Unmarshal(w.Body.Bytes(), &res); w.Code != 200 || len(res.Results) != 3 || !res.Results[0].OK || res.Results[0].Note != "" ||
		!res.Results[1].OK || !strings.Contains(res.Results[1].Note, "without its time") || res.Results[2].OK {
		t.Fatalf("drafts: %d %s", w.Code, w.Body)
	}
	for _, q := range *seen {
		if strings.Contains(q, "createPost") && !strings.Contains(q, "saveToDraft: true") {
			t.Fatalf("a post was sent that is not a draft: %s", q)
		}
	}
	n := len(*seen)
	w = call(h, "POST", "/mangoman/buffer/drafts", host, authz, `{"drafts":[{"channel":"busy","text":"a","due":"2026-11-02T13:00:00Z"},{"channel":"ig","text":"b"}]}`)
	if _ = json.Unmarshal(w.Body.Bytes(), &res); len(res.Results) != 2 || res.Results[0].OK || !strings.Contains(res.Results[1].Error, "not sent") || len(*seen) != n+1 {
		t.Fatalf("after the limit is used up, nothing more is tried: %s (%d requests)", w.Body, len(*seen)-n)
	}
	if w := call(h, "DELETE", "/mangoman/buffer", host, authz, ""); w.Code != 200 || st[bufferSecret] != "" {
		t.Fatalf("disconnect: %d", w.Code)
	}
	if w := call(h, "POST", "/mangoman/buffer/drafts", host, authz, `{"drafts":[{"channel":"ig","text":"x"}]}`); w.Code != 409 {
		t.Fatalf("drafts without a key: %d", w.Code)
	}
}
