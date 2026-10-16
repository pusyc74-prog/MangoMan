package ingress

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mySkill = "---\nname: my-tone\ndescription: Write in our shop's voice.\nmetadata:\n  kind: method\n---\n\n# Our voice\n"

// The library lists MangoMan's skills and the user's own; only the user's
// own can be saved or removed, and a copy of one of ours becomes theirs.
func TestSkillsLibrary(t *testing.T) {
	t.Setenv("MANGOMAN_HOME", t.TempDir())
	h, _ := dashServer(t, mem{})
	host := "127.0.0.1:4141"
	own, _ := ownDir()
	list := func() map[string]librarySkill {
		w := call(h, "GET", "/mangoman/skills", host, authz, "")
		var got struct{ Skills []librarySkill }
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		m := map[string]librarySkill{}
		for _, s := range got.Skills {
			m[s.Name] = s
		}
		return m
	}
	if s := list()["mangoman-strategy-report"]; s.Own || s.Kind != "pack" {
		t.Fatalf("ours: %+v", s)
	}
	body, _ := json.Marshal(map[string]string{"text": strings.ReplaceAll(mySkill, "\n", "\r\n")})
	if w := call(h, "PUT", "/mangoman/skills/my-tone", host, authz, string(body)); w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	if s := list()["my-tone"]; !s.Own || s.Kind != "method" {
		t.Fatalf("own: %+v", s)
	}
	for path, text := range map[string]string{
		"/mangoman/skills/mangoman-copywriting": mySkill,                                                                            // ours
		"/mangoman/skills/other-name":           mySkill,                                                                            // name differs
		"/mangoman/skills/my-tone":              "no frontmatter",                                                                   // not a skill
		"/mangoman/skills/my-tone2":             strings.Replace(mySkill, "my-tone", "my-tone2", 1) + strings.Repeat("x", maxSkill), // too big
	} {
		b, _ := json.Marshal(map[string]string{"text": text})
		if w := call(h, "PUT", path, host, authz, string(b)); w.Code != 400 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	// A broken own skill is listed with its error, so it can be fixed.
	os.MkdirAll(filepath.Join(own, "broken"), 0o755)
	os.WriteFile(filepath.Join(own, "broken", "SKILL.md"), []byte("---\nname: other\ndescription: x\n---\n"), 0o644)
	if s := list()["broken"]; !s.Own || s.Error == "" {
		t.Fatalf("broken: %+v", s)
	}

	w := call(h, "POST", "/mangoman/skills/mangoman-social-posts/copy", host, authz, `{"as":"my-posts"}`)
	if w.Code != 200 {
		t.Fatalf("copy: %d %s", w.Code, w.Body)
	}
	raw, _ := os.ReadFile(filepath.Join(own, "my-posts", "SKILL.md"))
	if !strings.Contains(string(raw), "\nname: my-posts\n") || strings.Contains(string(raw), "Proprietary") {
		t.Fatalf("copy's SKILL.md: %.300s", raw)
	}
	for _, f := range []string{"scripts/build_posts.py", "scripts/checks.py"} {
		if _, err := os.Stat(filepath.Join(own, "my-posts", f)); err != nil {
			t.Fatalf("copy is missing %s", f)
		}
	}
	if s := list()["my-posts"]; !s.Own || s.Error != "" {
		t.Fatalf("copy listed as %+v", s)
	}
	for _, c := range []struct{ path, body string }{
		{"/mangoman/skills/mangoman-social-posts/copy", `{"as":"my-posts"}`},   // taken
		{"/mangoman/skills/mangoman-social-posts/copy", `{"as":"mangoman-x"}`}, // our prefix
		{"/mangoman/skills/no-such-pack/copy", `{"as":"my-other"}`},            // not ours
	} {
		if w := call(h, "POST", c.path, host, authz, c.body); w.Code < 400 {
			t.Fatalf("%s %s: %d", c.path, c.body, w.Code)
		}
	}
	if _, err := os.Stat(filepath.Join(own, "my-other")); err == nil {
		t.Fatal("a failed copy left a folder")
	}

	if w := call(h, "GET", "/mangoman/skills/my-tone", host, authz, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "Our voice") {
		t.Fatalf("get own: %d %s", w.Code, w.Body)
	}
	if w := call(h, "GET", "/mangoman/skills/mangoman-copywriting", host, authz, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"own": false`) {
		t.Fatalf("get ours: %d", w.Code)
	}
	if w := call(h, "DELETE", "/mangoman/skills/mangoman-copywriting", host, authz, ""); w.Code != 404 {
		t.Fatalf("removed ours: %d", w.Code)
	}
	if w := call(h, "DELETE", "/mangoman/skills/my-tone", host, authz, ""); w.Code != 200 {
		t.Fatalf("remove: %d", w.Code)
	}
	if _, ok := list()["my-tone"]; ok {
		t.Fatal("still listed")
	}
}
