"""Start a single-file web app with the brand's look and a small storage helper.

Usage: python3 scaffold.py app.json --out app
Writes app/index.html: brand colours and fonts as CSS variables, styles for
forms, buttons, cards and tables that work on phones, and a script section
with `store` (saves data in the browser) and `$` helpers. Write the app's own
markup in <main> and its logic at the marked place in the script.
"""
import html
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import brandkit as BK  # noqa: E402

TEMPLATE = """<!doctype html>
<html lang="%(lang)s">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%(title)s</title>
<style>
:root { --base: #%(dark)s; --accent: #%(btn)s; --on-accent: #%(on_btn)s; --text: #%(text)s; --body: #%(body)s; --muted: #%(muted)s;
  --tint: #%(tint)s; --line: #%(grid)s; --good: #%(good)s; --bad: #%(bad)s; --radius: 12px; }
* { box-sizing: border-box; }
body { margin: 0; font: 16px/1.5 system-ui, -apple-system, "Segoe UI", Roboto, Arial, sans-serif; color: var(--text); background: var(--tint); }
header { background: var(--base); color: #fff; padding: 16px 20px; } header h1 { margin: 0; font-size: 1.25rem; }
main { width: min(960px, 100%%); margin: 0 auto; padding: 20px; display: grid; gap: 16px; }
.card { background: #fff; border: 1px solid var(--line); border-radius: var(--radius); padding: 18px; }
h2 { margin: 0 0 12px; font-size: 1.1rem; }
label { display: grid; gap: 6px; font-weight: 600; font-size: .95rem; }
input, select, textarea { font: inherit; padding: 11px 12px; border: 1.5px solid var(--line); border-radius: 10px; min-height: 46px; width: 100%%; background: #fff; color: var(--text); }
input:focus, select:focus, textarea:focus { outline: 3px solid color-mix(in srgb, var(--accent) 35%%, transparent); border-color: var(--accent); }
.row { display: grid; gap: 12px; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); align-items: end; }
button { font: inherit; font-weight: 700; min-height: 46px; padding: 0 18px; border-radius: 999px; border: 0; background: var(--accent); color: var(--on-accent); cursor: pointer; }
button.secondary { background: transparent; color: var(--text); border: 1.5px solid var(--line); }
button:focus-visible { outline: 3px solid var(--text); outline-offset: 2px; }
table { width: 100%%; border-collapse: collapse; font-variant-numeric: tabular-nums; } th, td { text-align: left; padding: 9px 8px; border-bottom: 1px solid var(--line); }
.num { text-align: right; } .muted { color: var(--muted); } .error { color: var(--bad); font-weight: 600; } .ok { color: var(--good); font-weight: 600; }
.big { font-size: 1.8rem; font-weight: 800; }
</style>
</head>
<body>
<header><h1>%(title)s</h1></header>
<main>
  <!-- app markup goes here: sections with class="card", labelled inputs, buttons that say what they do -->
</main>
<script>
const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];
const store = {  // data kept in this browser; survives reloads
  get(key, fallback) { try { const v = localStorage.getItem(%(ns)s + key); return v === null ? fallback : JSON.parse(v); } catch { return fallback; } },
  set(key, value) { try { localStorage.setItem(%(ns)s + key, JSON.stringify(value)); } catch {} },
};
const inr = n => new Intl.NumberFormat("en-IN", { style: "currency", currency: "INR", maximumFractionDigits: 2 }).format(n);

// app logic goes here
</script>
</body>
</html>
"""


def main():
    a = sys.argv[1:]
    if not a:
        sys.exit(__doc__)
    spec = json.load(open(a[0], encoding="utf-8"))
    out = a[a.index("--out") + 1] if "--out" in a else "app"
    if not spec.get("title"):
        sys.exit("spec problems:\n- title is required")
    T, note = BK.resolve_spec(spec, os.path.dirname(os.path.abspath(a[0])))
    btn = T["accent_fill"] if BK.contrast(T["accent_fill"], "FFFFFF") >= 3 else T["dark"]
    os.makedirs(out, exist_ok=True)
    path = os.path.join(out, "index.html")
    if os.path.exists(path) and "--force" not in a:
        sys.exit("%s exists; edit it, or pass --force to start again" % path)
    ns = json.dumps(spec.get("id", "app") + ":").replace("</", "<\\/")
    with open(path, "w", encoding="utf-8") as f:
        f.write(TEMPLATE % dict(T, title=html.escape(spec["title"]), lang=html.escape(spec.get("language", "en")), btn=btn, ns=ns,
                                on_btn=max(("FFFFFF", T["text"]), key=lambda c: BK.contrast(c, btn))))
    if note:
        print(note)
    print("wrote " + path)


if __name__ == "__main__":
    main()
