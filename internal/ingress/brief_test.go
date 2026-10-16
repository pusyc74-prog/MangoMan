package ingress

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The business is kept once, only its known fields, and a super agent's
// run gets its own folder with brief.json and a copy of the logo.
func TestBusinessAndBrief(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("MANGOMAN_HOME", t.TempDir())
	h, _ := dashServer(t, mem{})
	projects := filepath.Join(home, "MangoMan Projects")
	host := "127.0.0.1:4141"

	if w := call(h, "GET", "/mangoman/business", host, authz, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"fields": {}`) {
		t.Fatalf("empty business: %d %s", w.Code, w.Body)
	}
	if w := call(h, "PUT", "/mangoman/business", host, authz, `{"fields":{"name":" Kulhad Cafe ","where":"Pune","tone":""}}`); w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	for _, bad := range []string{`{"fields":{"secret":"x"}}`, `{"fields":{"name":"` + strings.Repeat("a", maxField+1) + `"}}`, `nope`} {
		if w := call(h, "PUT", "/mangoman/business", host, authz, bad); w.Code != 400 {
			t.Fatalf("%.40s: %d", bad, w.Code)
		}
	}
	for _, bad := range []string{"logo.gif", "logo", "../logo.svg"} {
		if w := call(h, "POST", "/mangoman/business/logo?name="+url.QueryEscape(bad), host, authz, "x"); w.Code != 400 {
			t.Fatalf("%s: %d", bad, w.Code)
		}
	}
	if w := call(h, "POST", "/mangoman/business/logo?name=a.jpg", host, authz, "jpg"); w.Code != 200 {
		t.Fatalf("logo: %d %s", w.Code, w.Body)
	}
	// A new logo of another type replaces the old one.
	if w := call(h, "POST", "/mangoman/business/logo?name=Logo.PNG", host, authz, "png"); w.Code != 200 {
		t.Fatalf("logo: %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(projects, "Brand", "logo.jpg")); !os.IsNotExist(err) {
		t.Fatal("the old logo is still there")
	}
	var b business
	w := call(h, "GET", "/mangoman/business", host, authz, "")
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil || b.Logo != "logo.png" || b.Fields["name"] != "Kulhad Cafe" || len(b.Fields) != 2 {
		t.Fatalf("kept %+v: %v", b, err)
	}

	day := time.Now().Format("2006-01-02")
	brief := `{"agent":"social-media-manager","title":"Social Media Manager","job":{"goal":["More sales"],"period":"Next month"}}`
	for i, want := range []string{"social-media-manager-" + day, "social-media-manager-" + day + "-2"} {
		w := call(h, "POST", "/mangoman/agents/brief", host, authz, brief)
		var got struct{ Folder string }
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 || got.Folder != filepath.Join(projects, want) {
			t.Fatalf("run %d: %d %s", i, w.Code, w.Body)
		}
		raw, err := os.ReadFile(filepath.Join(got.Folder, "brief.json"))
		var saved struct {
			Agent    string
			Logo     string
			Business map[string]string
			Job      map[string]any
		}
		if err != nil || json.Unmarshal(raw, &saved) != nil || saved.Agent != "social-media-manager" || saved.Logo != "logo.png" ||
			saved.Business["where"] != "Pune" || saved.Job["period"] != "Next month" {
			t.Fatalf("brief.json: %s %v", raw, err)
		}
		if l, err := os.ReadFile(filepath.Join(got.Folder, "logo.png")); err != nil || string(l) != "png" {
			t.Fatalf("logo copy: %v", err)
		}
	}
	for _, bad := range []string{`{"agent":"../x","job":{}}`, `{"agent":"Bad Name"}`,
		`{"agent":"a","job":{"notes":"` + strings.Repeat("a", maxField+1) + `"}}`, `{"agent":"a","job":{"n":3}}`} {
		if w := call(h, "POST", "/mangoman/agents/brief", host, authz, bad); w.Code != 400 {
			t.Fatalf("%.40s: %d", bad, w.Code)
		}
	}
	if w := call(h, "GET", "/mangoman/business", host, nil, ""); w.Code != 401 {
		t.Fatalf("no token: %d", w.Code)
	}
}
