"""Run the analysis script and build a monthly client report (A4 PDF) from what it prints.

Usage: python3 build_report.py analysis.py --out report [--no-pdf]
Writes <out>.pdf, <out>.html and <out>.spec.json. Layout: a header with the
client, period and summary; KPI tiles with change against the last period;
one section per finding (a takeaway headline, a chart and what it means);
wins, issues and the plan for next month.
"""
import html
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import brandkit as BK  # noqa: E402
import render  # noqa: E402
import vizlib as V  # noqa: E402

e = lambda s: html.escape(str(s if s is not None else ""), quote=True)
run_analysis = V.run_analysis


def validate(spec):
    p = []
    for k in ("title", "client", "period", "summary"):
        if not spec.get(k):
            p.append("%s is required" % k)
    if not (1 <= len(spec.get("kpis", [])) <= 6):
        p.append("give 1 to 6 kpis")
    for i, s in enumerate(spec.get("sections", []), 1):
        if not s.get("headline"):
            p.append("section %d needs a headline (the takeaway)" % i)
        if s.get("chart"):
            p += ["section %d: %s" % (i, x) for x in V.validate_chart(dict(s["chart"], title=s.get("headline", "")))]
    if not spec.get("sections"):
        p.append("give at least one section")
    if spec.get("theme") and spec["theme"] not in BK.CURATED:
        p.append("theme must be one of %s" % ", ".join(BK.CURATED))
    return p


CSS = """
@page :first { margin-top: 0; }
@page { size: A4; margin: 14mm 0 16mm; @bottom-right { content: "Page " counter(page) " of " counter(pages); font: 8pt sans-serif; color: #%(muted)s; margin-right: 16mm; } }
* { box-sizing: border-box; }
html { -webkit-print-color-adjust: exact; print-color-adjust: exact; }
body { margin: 0; font: 10pt/1.5 "Segoe UI", Calibri, Carlito, Arial, sans-serif; color: #%(text)s; }
header { background: #%(dark)s; color: #fff; padding: 14mm 16mm 10mm; }
header .row { display: flex; justify-content: space-between; align-items: center; }
header img { height: 11mm; background: #fff; border-radius: 2mm; padding: 1.5mm 2.5mm; }
header .k { color: #%(accent)s; font-weight: 700; margin-top: 8mm; }
header h1 { margin: 1mm 0 0; font-size: 22pt; line-height: 1.15; }
header .sub { color: #%(on_dark2)s; margin-top: 1.5mm; }
main { padding: 8mm 16mm 0; }
.summary { font-size: 11.5pt; line-height: 1.55; margin: 0 0 6mm; }
.kpis { display: grid; grid-template-columns: repeat(var(--n), 1fr); border-top: 2px solid #%(text)s; margin-bottom: 7mm; }
.kpis div { padding: 3mm 3mm 0 0; }
.kpis .l { color: #%(muted)s; font-size: 8.5pt; font-weight: 700; }
.kpis .v { font-size: 18pt; font-weight: 700; line-height: 1.2; }
.kpis .d { font-size: 8.5pt; font-weight: 700; }
.up { color: #%(good)s; } .down { color: #%(bad)s; } .flat { color: #%(muted)s; }
section { break-inside: avoid; margin-bottom: 7mm; }
h2 { font-size: 13.5pt; margin: 0 0 2mm; line-height: 1.25; }
.chart { --s1:#%(accent_fill)s; --dim:#%(dim)s; --muted:#%(muted)s; --ink:#%(text)s; --ink-2:#%(body)s; --grid:#%(grid)s; --axis:#%(dim)s; --surface:#fff; margin: 2mm 0; }
.chart text { font-size: 11px; fill: #%(muted)s; } .chart text.lbl { fill: #%(body)s; font-weight: 700; }
p { margin: 0 0 2.5mm; color: #%(body)s; }
.two { display: grid; grid-template-columns: 1fr 1fr; gap: 8mm; break-inside: avoid; margin-bottom: 7mm; }
.two h2 { font-size: 11.5pt; }
ul { margin: 0; padding-left: 4.5mm; } li { margin: 0 0 1.5mm; } li::marker { color: #%(accent_fill)s; }
table { width: 100%%; border-collapse: collapse; font-variant-numeric: tabular-nums; margin: 2mm 0; }
th { text-align: left; font-size: 8.5pt; color: #%(muted)s; border-bottom: 1.5px solid #%(text)s; padding: 1.5mm 2mm; }
td { padding: 1.8mm 2mm; border-bottom: 1px solid #%(grid)s; }
.n { text-align: right; }
.src { font-size: 8pt; color: #%(muted)s; margin-top: 4mm; }
"""


def kpi(k):
    v = V.fmt(k["value"], k.get("format", "number"), k.get("currency"))
    d = ""
    if isinstance(k.get("delta"), (int, float)):
        good = (k["delta"] >= 0) == k.get("up_is_good", True)
        cls = "flat" if k["delta"] == 0 else ("up" if good else "down")
        d = '<div class="d %s">%s <span style="color:#888;font-weight:400">%s</span></div>' % (cls, e(V.fmt_delta(k["delta"], k.get("delta_kind", "percent"))), e(k.get("delta_label", "")))
    return '<div><div class="l">%s</div><div class="v">%s</div>%s</div>' % (e(k["label"]), e(v), d)


def table_html(t):
    fm = t.get("formats") or ["text"] * len(t["columns"])
    num = [f != "text" for f in fm]
    head = "".join('<th class="%s">%s</th>' % ("n" if num[i] else "", e(c)) for i, c in enumerate(t["columns"]))
    rows = "".join("<tr>%s</tr>" % "".join('<td class="%s">%s</td>' % ("n" if num[i] else "", e(V.fmt(v, fm[i], t.get("currency"), False) if num[i] else v))
                                           for i, v in enumerate(r)) for r in t["rows"])
    return "<table><tr>%s</tr>%s</table>" % (head, rows)


def build_html(spec, T):
    logo = ""
    if T.get("logo"):
        import base64
        with open(T["logo"], "rb") as f:
            logo = '<img src="data:image/png;base64,%s" alt="">' % base64.b64encode(f.read()).decode()
    head = '<header><div class="row"><b>%s</b>%s</div><div class="k">%s</div><h1>%s</h1><div class="sub">%s, %s</div></header>' % (
        e(spec.get("prepared_by", "")), logo, e(spec.get("kicker", "Monthly report")), e(spec["title"]), e(spec["client"]), e(spec["period"]))
    body = ['<p class="summary">%s</p>' % e(spec["summary"]), '<div class="kpis" style="--n:%d">%s</div>' % (len(spec["kpis"]), "".join(kpi(k) for k in spec["kpis"]))]
    for s in spec["sections"]:
        parts = ["<h2>%s</h2>" % e(s["headline"])]
        if s.get("chart"):
            c = dict(s["chart"], title=s["headline"])
            parts.append('<div class="chart">%s%s</div>' % (V.legend(c), V.svg_chart(dict(c, bar_max=40), 640, 230)))
        parts += ["<p>%s</p>" % e(t) for t in (s.get("body") if isinstance(s.get("body"), list) else [s["body"]] if s.get("body") else [])]
        if s.get("table"):
            parts.append(table_html(s["table"]))
        body.append("<section>%s</section>" % "".join(parts))
    if spec.get("wins") or spec.get("issues"):
        body.append('<div class="two"><div><h2>What went well</h2><ul>%s</ul></div><div><h2>What needs attention</h2><ul>%s</ul></div></div>' % (
            "".join("<li>%s</li>" % e(x) for x in spec.get("wins", [])), "".join("<li>%s</li>" % e(x) for x in spec.get("issues", []))))
    if spec.get("next"):
        body.append("<section><h2>Plan for next month</h2>%s</section>" % table_html(
            {"columns": ["Action", "Owner", "By"], "rows": [[x["action"], x.get("owner", ""), x.get("date", "")] for x in spec["next"]]}))
    if spec.get("source"):
        body.append('<p class="src">Source: %s</p>' % e(spec["source"]))
    return '<!doctype html><html lang="en"><head><meta charset="utf-8"><title>%s</title><style>%s</style></head><body>%s<main>%s</main></body></html>' % (
        e(spec["title"]), V.CHART_CSS + CSS % T, head, "".join(body))


def main():
    a = sys.argv[1:]
    if not a:
        sys.exit(__doc__)
    out = a[a.index("--out") + 1] if "--out" in a else "report"
    spec = run_analysis(a[0])
    probs = validate(spec)
    if probs:
        sys.exit("spec problems:\n- " + "\n- ".join(probs))
    T, note = BK.resolve_spec(spec, os.path.dirname(os.path.abspath(a[0])))
    with open(out + ".spec.json", "w", encoding="utf-8") as f:
        json.dump(spec, f, indent=2, ensure_ascii=False)
    with open(out + ".html", "w", encoding="utf-8") as f:
        f.write(build_html(spec, T))
    made = [out + ".html", out + ".spec.json"]
    if "--no-pdf" not in a:
        try:
            render.html_to_pdf(out + ".html", out + ".pdf")
            made.append(out + ".pdf")
        except Exception as ex:  # noqa: BLE001
            print("PDF skipped: %s" % ex)
    if note:
        print(note)
    print("built " + ", ".join(made))


if __name__ == "__main__":
    main()
