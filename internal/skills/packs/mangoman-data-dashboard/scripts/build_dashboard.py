"""Run the analysis script and build the dashboard from what it prints.

Usage: python3 build_dashboard.py analysis.py --out dashboard [--no-shot]
  analysis.py must print ONE JSON spec on stdout (see SKILL.md). Every number
  in the dashboard comes from that run; nothing is typed in by hand.
Writes <out>.html, <out>.spec.json and, when a browser engine exists, <out>.png.
"""
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import vizlib as V  # noqa: E402
import render  # noqa: E402


run_analysis = V.run_analysis


def validate(spec):
    p = []
    if not spec.get("title"):
        p.append("spec needs a title")
    if not spec.get("headline"):
        p.append("spec needs a headline: the one finding, with its number")
    for k in spec.get("kpis", []):
        if "label" not in k or not isinstance(k.get("value"), (int, float)):
            p.append("every KPI needs a label and a numeric value: %r" % k)
    if len(spec.get("kpis", [])) > 6:
        p.append("at most 6 KPIs; put the rest in the table")
    charts = spec.get("charts", [])
    if not charts and not spec.get("kpis"):
        p.append("nothing to show: add KPIs or charts")
    if len(charts) > 8:
        p.append("at most 8 charts per dashboard")
    for c in charts:
        p += V.validate_chart(c)
    return p


def kpi_tile(k, hero=False):
    kind, cur = k.get("format", "number"), k.get("currency")
    delta = ""
    if isinstance(k.get("delta"), (int, float)):
        d = k["delta"]
        good = (d >= 0) == k.get("up_is_good", True)
        arrow = "▲" if d > 0 else ("▼" if d < 0 else "●")
        cls = "up" if good else "down"
        if d == 0:
            cls = "flat"
        delta = '<div class="delta %s"><span aria-hidden="true">%s</span> %s <span class="vs">%s</span></div>' % (
            cls, arrow, V.esc(V.fmt_delta(d, k.get("delta_kind", "percent"))), V.esc(k.get("delta_label", "")))
    return '<div class="kpi%s"><div class="klabel">%s</div><div class="kvalue">%s</div>%s</div>' % (
        " hero" if hero else "", V.esc(k["label"]), V.esc(V.fmt(k["value"], kind, cur)), delta)


PAGE_CSS = """
* { box-sizing: border-box; }
html, body { background: var(--page); margin: 0; }
body { color: var(--ink); font: 15px/1.5 system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; padding: 32px 24px 48px; }
main { max-width: 1180px; margin: 0 auto; }
header { display: flex; justify-content: space-between; align-items: flex-end; gap: 16px; flex-wrap: wrap; border-bottom: 1px solid var(--grid); padding-bottom: 16px; }
h1 { margin: 0; font-size: 26px; letter-spacing: -0.01em; }
.sub { color: var(--ink-2); margin: 4px 0 0; }
.period { color: var(--muted); font-size: 13px; text-align: right; }
.headline { margin: 20px 0 8px; font-size: 19px; font-weight: 600; max-width: 900px; }
.kpis { display: grid; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); gap: 12px; margin: 16px 0 8px; }
.kpi { background: var(--surface); border: 1px solid var(--ring); border-radius: 12px; padding: 14px 16px; }
.klabel { color: var(--ink-2); font-size: 13px; }
.kvalue { font-size: 28px; font-weight: 650; letter-spacing: -0.02em; margin-top: 2px; }
.kpi.hero .kvalue { font-size: 48px; line-height: 1.05; }
.delta { font-size: 13px; margin-top: 4px; font-weight: 600; }
.delta.up { color: var(--up); } .delta.down { color: var(--down); } .delta.flat { color: var(--ink-2); }
.delta .vs { color: var(--muted); font-weight: 400; }
.grid2 { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; margin-top: 16px; }
.card { background: var(--surface); border: 1px solid var(--ring); border-radius: 12px; padding: 16px 18px 12px; min-width: 0; }
.card.wide { grid-column: 1 / -1; }
.card h2 { font-size: 15px; margin: 0 0 2px; font-weight: 600; }
.card .note { color: var(--muted); font-size: 12.5px; margin: 0 0 10px; }
table.data { width: 100%; border-collapse: collapse; font-size: 14px; font-variant-numeric: tabular-nums; }
table.data th { text-align: left; color: var(--ink-2); font-weight: 600; font-size: 13px; padding: 8px 10px; }
table.data td { border-top: 1px solid var(--grid); padding: 8px 10px; }
table.data td.n, table.data th.n { text-align: right; }
.notes { color: var(--ink-2); font-size: 14px; margin-top: 16px; }
footer { color: var(--muted); font-size: 12px; margin-top: 24px; }
@media (max-width: 760px) { .grid2 { grid-template-columns: 1fr; } .kpi.hero .kvalue { font-size: 38px; } }
@media print { body { padding: 0; } .tableview { display: none; } }
"""


def table_html(t):
    nums = [all(isinstance(r[i], (int, float)) for r in t["rows"] if i < len(r)) for i in range(len(t["columns"]))]
    fmts = t.get("formats") or ["number"] * len(t["columns"])
    head = "".join('<th class="%s">%s</th>' % ("n" if nums[i] else "", V.esc(c)) for i, c in enumerate(t["columns"]))
    body = "".join("<tr>%s</tr>" % "".join(
        '<td class="%s">%s</td>' % ("n" if nums[i] else "", V.esc(V.fmt(v, fmts[i] if i < len(fmts) else "number", t.get("currency"), False) if isinstance(v, (int, float)) else v))
        for i, v in enumerate(r)) for r in t["rows"])
    return '<div class="card wide"><h2>%s</h2><table class="data"><tr>%s</tr>%s</table></div>' % (V.esc(t.get("title", "")), head, body)


def build(spec):
    kpis = spec.get("kpis", [])
    tiles = "".join(kpi_tile(k, hero=(i == 0 and spec.get("hero_first", True) and len(kpis) <= 4)) for i, k in enumerate(kpis))
    cards = []
    for c in spec.get("charts", []):
        wide = c.get("wide") or (c.get("type", "line") == "line" and len(c["x"]) > 14)
        w = 1100 if wide else 540
        cards.append('<section class="card%s"><h2>%s</h2>%s%s</section>' % (
            " wide" if wide else "", V.esc(c["title"]),
            '<p class="note">%s</p>' % V.esc(c["note"]) if c.get("note") else "",
            V.chart_block(c, w, 300 if wide else 260)))
    table = table_html(spec["table"]) if spec.get("table") else ""
    notes = "".join("<p>%s</p>" % V.esc(n) for n in spec.get("notes", []))
    return """<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title><style>%s%s%s</style></head><body><main>
<header><div><h1>%s</h1>%s</div><div class="period">%s</div></header>
<p class="headline">%s</p>
<div class="kpis">%s</div>
<div class="grid2">%s%s</div>
<div class="notes">%s</div>
<footer>Source: %s. Every number was computed from the data by the analysis script; built with MangoMan.</footer>
</main><script>%s</script></body></html>""" % (
        V.esc(spec["title"]), V.CSS_TOKENS, V.CHART_CSS, PAGE_CSS, V.esc(spec["title"]),
        '<p class="sub">%s</p>' % V.esc(spec["subtitle"]) if spec.get("subtitle") else "",
        V.esc(spec.get("period", "")), V.esc(spec["headline"]), tiles, "".join(cards), table, notes,
        V.esc(spec.get("source", "the provided data")), V.TIP_JS)


def main():
    args = sys.argv[1:]
    if not args:
        sys.exit(__doc__)
    out = args[args.index("--out") + 1] if "--out" in args else "dashboard"
    spec = run_analysis(args[0])
    problems = validate(spec)
    if problems:
        sys.exit("spec problems:\n- " + "\n- ".join(problems))
    with open(out + ".spec.json", "w", encoding="utf-8") as f:
        json.dump(spec, f, indent=2, ensure_ascii=False)
    with open(out + ".html", "w", encoding="utf-8") as f:
        f.write(build(spec))
    shot = ""
    if "--no-shot" not in args and render.screenshot(out + ".html", out + ".png", 1280, 900):
        shot = ", " + out + ".png"
    print("built %s.html, %s.spec.json%s" % (out, out, shot))


if __name__ == "__main__":
    main()
