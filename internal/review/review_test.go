package review

import (
	"strings"
	"testing"
)

func TestScan(t *testing.T) {
	diff := `diff --git a/app.py b/app.py
--- a/app.py
+++ b/app.py
@@ -10,2 +10,5 @@ def pay():
 x = 1
+STRIPE = "sk_live_` + strings.Repeat("a", 24) + `"
+cur.execute("SELECT * FROM users WHERE id=" + uid)
-old = 2
+subprocess.run(cmd, shell=True)
+total = price * qty
--- a/package.json
+++ b/package.json
@@ -3,0 +4,1 @@
+    "left-pad": "^1.3.0",
`
	got := Scan(diff)
	want := map[string]int{"live payment key (Stripe or Razorpay)": 11, "SQL built by joining text (SQL injection)": 12, "shell command built from text": 13}
	for _, f := range got {
		if l, ok := want[f.Detail]; ok {
			if f.Line != l || f.File != "app.py" {
				t.Fatalf("%s at %s:%d, want line %d", f.Detail, f.File, f.Line, l)
			}
			delete(want, f.Detail)
		}
	}
	if len(want) != 0 || !Blocking(got) || got[len(got)-1].Kind != "dependency" || len(got) != 4 {
		t.Fatalf("missing %v in %+v", want, got)
	}
	if Blocking(Scan("+++ b/x.go\n@@ -1 +1 @@\n+fmt.Println(1)\n")) {
		t.Fatal("plain code must not block")
	}
	if r := Report(got, "Looks fine."); !strings.Contains(r, "MUST FIX: live payment key") || !strings.Contains(r, "Looks fine.") {
		t.Fatal(r)
	}
}
