"""Run the analysis script and build the deck from what it prints.

Usage: python3 build_deck.py analysis.py --out deck [--no-pdf] [--no-pptx]
Writes <out>.html (all slides, 1280x720 each), <out>.pdf (one slide per page),
<out>.pptx (editable, native charts; needs python-pptx) and <out>.spec.json.
"""
import json
import os
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import vizlib as V  # noqa: E402
import render  # noqa: E402

SLIDE_TYPES = ("title", "answer", "kpis", "chart", "bullets", "table", "next_steps", "section")


def run_analysis(script):
    res = subprocess.run([sys.executable, script], capture_output=True, text=True, timeout=600,
                         cwd=os.path.dirname(os.path.abspath(script)) or ".")
    if res.returncode != 0:
        sys.exit("analysis failed:\n" + res.stderr[-4000:])
    out = res.stdout.strip()
    start = out.find("{")
    if start < 0:
        sys.exit("analysis printed no JSON object")
    try:
        return json.loads(out[start:])
    except json.JSONDecodeError as e:
        sys.exit("analysis output is not valid JSON: %s" % e)


def validate(spec):
    p = []
    slides = spec.get("slides", [])
    if not spec.get("title"):
        p.append("deck needs a title")
    if not slides:
        p.append("deck has no slides")
    for i, s in enumerate(slides, 1):
        t = s.get("type")
        if t not in SLIDE_TYPES:
            p.append("slide %d: type must be one of %s" % (i, ", ".join(SLIDE_TYPES)))
            continue
        if t not in ("title",) and not s.get("headline"):
            p.append("slide %d: needs a headline (the takeaway, as a sentence)" % i)
        if t == "chart":
            p += ["slide %d: %s" % (i, x) for x in V.validate_chart(dict(s.get("chart", {}), title=s.get("headline", "")))]
        if t == "kpis" and not (1 <= len(s.get("kpis", [])) <= 4):
            p.append("slide %d: 1 to 4 KPIs per slide" % i)
        if t == "table" and not s.get("table", {}).get("rows"):
            p.append("slide %d: table has no rows" % i)
    return p


CSS = """
* { box-sizing: border-box; margin: 0; }
@page { size: 1280px 720px; margin: 0; }
html, body { background: #e9e8e3; }
body { font-family: system-ui, -apple-system, "Segoe UI", Roboto, Arial, sans-serif; color: #0b0b0b; padding: 24px 0; }
.slide { width: 1280px; height: 720px; margin: 0 auto 24px; background: #fcfcfb; position: relative; overflow: hidden;
  padding: 64px 80px 72px; display: flex; flex-direction: column; box-shadow: 0 2px 12px rgba(0,0,0,.08); }
@media print { html, body { background: #fcfcfb; padding: 0; } .slide { margin: 0; box-shadow: none; break-after: page; } }
.slide h2 { font-size: 34px; line-height: 1.2; font-weight: 650; letter-spacing: -0.01em; max-width: 1080px; }
.slide .rule { width: 56px; height: 4px; background: #2a78d6; border-radius: 2px; margin: 0 0 22px; }
.body { flex: 1; min-height: 0; margin-top: 28px; display: flex; flex-direction: column; justify-content: center; }
.foot { position: absolute; left: 80px; right: 80px; bottom: 28px; display: flex; justify-content: space-between; color: #898781; font-size: 13px; }
.title-slide { justify-content: center; background: #10213a; color: #ffffff; }
.title-slide h1 { font-size: 56px; line-height: 1.1; font-weight: 700; letter-spacing: -0.02em; max-width: 1000px; }
.title-slide .tsub { font-size: 24px; color: #c9d6ea; margin-top: 18px; max-width: 1000px; }
.title-slide .tdate { font-size: 18px; color: #8fa7c9; margin-top: 36px; }
.title-slide .rule { background: #eda100; }
.title-slide .foot { color: #8fa7c9; }
.section-slide { justify-content: center; background: #f1f0eb; }
.points { list-style: none; padding: 0; display: grid; gap: 22px; }
.points li { display: grid; grid-template-columns: 44px 1fr; align-items: baseline; font-size: 26px; line-height: 1.35; }
.points li span { color: #2a78d6; font-weight: 700; font-size: 22px; }
.bullets { padding-left: 28px; display: grid; gap: 16px; font-size: 26px; line-height: 1.35; }
.bullets li::marker { color: #2a78d6; }
.kgrid { display: grid; gap: 20px; }
.ktile { border: 1px solid rgba(11,11,11,.10); border-radius: 14px; padding: 26px 28px; background: #ffffff; }
.ktile .kl { color: #52514e; font-size: 19px; }
.ktile .kv { font-size: 54px; font-weight: 700; letter-spacing: -0.02em; margin-top: 6px; }
.ktile .kd { font-size: 18px; font-weight: 600; margin-top: 6px; }
.kd.up { color: #006300; } .kd.down { color: #d03b3b; } .kd.flat { color: #52514e; } .kd .vs { color: #898781; font-weight: 400; }
.chart text { font-size: 15px !important; }
.takeaway { margin-top: 14px; font-size: 19px; color: #52514e; }
table.t { border-collapse: collapse; width: 100%; font-size: 20px; font-variant-numeric: tabular-nums; }
table.t th { text-align: left; color: #52514e; font-weight: 600; font-size: 17px; padding: 10px 14px; border-bottom: 2px solid #c3c2b7; }
table.t td { padding: 11px 14px; border-bottom: 1px solid #e1e0d9; }
table.t .n { text-align: right; }
"""

TOKENS_LIGHT = ":root{--surface:#fcfcfb;--ink:#0b0b0b;--ink-2:#52514e;--muted:#898781;--grid:#e1e0d9;--axis:#c3c2b7;--ring:rgba(11,11,11,.10);%s}" % \
    "".join("--s%d:%s;" % (i + 1, c) for i, c in enumerate(V.PALETTE_LIGHT))


def kpi_tile(k):
    kind, cur = k.get("format", "number"), k.get("currency")
    d = ""
    if isinstance(k.get("delta"), (int, float)):
        dv = k["delta"]
        good = (dv >= 0) == k.get("up_is_good", True)
        cls = "flat" if dv == 0 else ("up" if good else "down")
        arrow = "▲" if dv > 0 else ("▼" if dv < 0 else "●")
        d = '<div class="kd %s">%s %s <span class="vs">%s</span></div>' % (cls, arrow, V.esc(V.fmt_delta(dv, k.get("delta_kind", "percent"))), V.esc(k.get("delta_label", "")))
    return '<div class="ktile"><div class="kl">%s</div><div class="kv">%s</div>%s</div>' % (V.esc(k["label"]), V.esc(V.fmt(k["value"], kind, cur)), d)


def table_html(t):
    cols = t["columns"]
    fm = t.get("formats") or ["number"] * len(cols)
    isnum = [all(isinstance(r[i], (int, float)) for r in t["rows"]) for i in range(len(cols))]
    head = "".join('<th class="%s">%s</th>' % ("n" if isnum[i] else "", V.esc(c)) for i, c in enumerate(cols))
    rows = "".join("<tr>%s</tr>" % "".join('<td class="%s">%s</td>' % (
        "n" if isnum[i] else "", V.esc(V.fmt(v, fm[i], t.get("currency"), False) if isinstance(v, (int, float)) else v)) for i, v in enumerate(r)) for r in t["rows"])
    return '<table class="t"><tr>%s</tr>%s</table>' % (head, rows)


def slide_html(s, n, total, spec):
    t = s["type"]
    foot = '<div class="foot"><span>%s</span><span>%d / %d</span></div>' % (V.esc(spec.get("source", spec.get("title", ""))), n, total)
    if t == "title":
        return '<section class="slide title-slide" id="s%d"><div class="rule"></div><h1>%s</h1>%s%s%s</section>' % (
            n, V.esc(s.get("title") or spec["title"]),
            '<div class="tsub">%s</div>' % V.esc(s.get("subtitle") or spec.get("subtitle", "")) if (s.get("subtitle") or spec.get("subtitle")) else "",
            '<div class="tdate">%s</div>' % V.esc(spec.get("date", "")) if spec.get("date") else "", foot)
    if t == "section":
        return '<section class="slide section-slide" id="s%d"><div class="rule"></div><h2>%s</h2>%s</section>' % (n, V.esc(s["headline"]), foot)
    body = ""
    if t == "answer":
        body = '<ol class="points">%s</ol>' % "".join('<li><span>%d</span><div>%s</div></li>' % (i + 1, V.esc(p)) for i, p in enumerate(s.get("points", [])))
    elif t == "bullets":
        body = '<ul class="bullets">%s</ul>' % "".join("<li>%s</li>" % V.esc(b) for b in s.get("bullets", []))
    elif t == "kpis":
        ks = s["kpis"]
        body = '<div class="kgrid" style="grid-template-columns:repeat(%d,1fr)">%s</div>' % (len(ks), "".join(kpi_tile(k) for k in ks))
    elif t == "chart":
        c = dict(s["chart"], title=s["headline"])
        h = 420 if not s.get("takeaway") else 380
        body = '<div class="chart">%s%s</div>%s' % (V.legend(c), V.svg_chart(c, 1120, h),
                                                    '<p class="takeaway">%s</p>' % V.esc(s["takeaway"]) if s.get("takeaway") else "")
    elif t == "table":
        body = table_html(s["table"])
    elif t == "next_steps":
        items = s.get("items", [])
        body = table_html({"columns": ["Action", "Owner", "By when"], "rows": [[i.get("action", ""), i.get("owner", ""), i.get("date", "")] for i in items]})
    return '<section class="slide" id="s%d"><div class="rule"></div><h2 data-check="headline-%d">%s</h2><div class="body" data-check="body-%d">%s</div>%s</section>' % (
        n, n, V.esc(s["headline"]), n, body, foot)


def build_html(spec):
    slides = spec["slides"]
    return '<!doctype html><html lang="en"><head><meta charset="utf-8"><title>%s</title><style>%s%s%s</style></head><body>%s<script>%s</script></body></html>' % (
        V.esc(spec["title"]), TOKENS_LIGHT, V.CHART_CSS.replace(".chart text { font: 12px", ".chart text { font: 15px"), CSS,
        "".join(slide_html(s, i + 1, len(slides), spec) for i, s in enumerate(slides)), V.TIP_JS)


# ---------- PowerPoint ----------

def build_pptx(spec, path):
    try:
        from pptx import Presentation
        from pptx.chart.data import CategoryChartData
        from pptx.dml.color import RGBColor
        from pptx.enum.chart import XL_CHART_TYPE, XL_LEGEND_POSITION, XL_TICK_LABEL_POSITION, XL_LABEL_POSITION
        from pptx.enum.text import PP_ALIGN
        from pptx.util import Inches, Pt, Emu
    except ImportError:
        return False
    rgb = lambda h: RGBColor.from_string(h.lstrip("#"))
    prs = Presentation()
    prs.slide_width, prs.slide_height = Inches(13.333), Inches(7.5)
    blank = prs.slide_layouts[6]
    INK, INK2, MUTED, ACCENT = "0B0B0B", "52514E", "898781", "2A78D6"

    def text(slide, x, y, w, h, s, size, color=INK, bold=False):
        tb = slide.shapes.add_textbox(Inches(x), Inches(y), Inches(w), Inches(h))
        tf = tb.text_frame
        tf.word_wrap = True
        p = tf.paragraphs[0]
        r = p.add_run()
        r.text = s
        r.font.size, r.font.bold, r.font.name = Pt(size), bold, "Arial"
        r.font.color.rgb = rgb(color)
        return tf

    def bg(slide, color):
        f = slide.background.fill
        f.solid()
        f.fore_color.rgb = rgb(color)

    def rule(slide, x, y, color=ACCENT):
        sh = slide.shapes.add_shape(1, Inches(x), Inches(y), Inches(0.58), Inches(0.05))
        sh.fill.solid()
        sh.fill.fore_color.rgb = rgb(color)
        sh.line.fill.background()

    def foot(slide, n, total, color=MUTED):
        text(slide, 0.83, 6.95, 9, 0.35, spec.get("source", spec["title"]), 10, color)
        tf = text(slide, 11.0, 6.95, 1.5, 0.35, "%d / %d" % (n, total), 10, color)
        tf.paragraphs[0].alignment = 3

    total = len(spec["slides"])
    for n, s in enumerate(spec["slides"], 1):
        sl = prs.slides.add_slide(blank)
        t = s["type"]
        if t == "title":
            bg(sl, "10213A")
            rule(sl, 0.83, 2.4, "EDA100")
            text(sl, 0.83, 2.6, 11.5, 1.6, s.get("title") or spec["title"], 44, "FFFFFF", True)
            sub = s.get("subtitle") or spec.get("subtitle")
            if sub:
                text(sl, 0.83, 4.2, 11.5, 0.8, sub, 20, "C9D6EA")
            if spec.get("date"):
                text(sl, 0.83, 5.2, 11.5, 0.5, spec["date"], 14, "8FA7C9")
            foot(sl, n, total, "8FA7C9")
            continue
        bg(sl, "FCFCFB" if t != "section" else "F1F0EB")
        rule(sl, 0.83, 0.62)
        text(sl, 0.83, 0.75, 11.6, 1.1, s["headline"], 26, INK, True)
        top = 2.0
        if t == "answer":
            tf = None
            y = top + 0.1
            for i, p in enumerate(s.get("points", []), 1):
                text(sl, 0.83, y, 0.5, 0.6, str(i), 18, ACCENT, True)
                text(sl, 1.35, y, 11.0, 1.0, p, 20, INK)
                y += 1.25
        elif t == "bullets":
            tb = sl.shapes.add_textbox(Inches(0.83), Inches(top), Inches(11.6), Inches(4.6))
            tf = tb.text_frame
            tf.word_wrap = True
            for i, b in enumerate(s.get("bullets", [])):
                p = tf.paragraphs[0] if i == 0 else tf.add_paragraph()
                r = p.add_run()
                r.text = "•  " + b
                r.font.size, r.font.name = Pt(20), "Arial"
                r.font.color.rgb = rgb(INK)
                p.space_after = Pt(12)
        elif t == "kpis":
            ks = s["kpis"]
            w = (11.6 - 0.3 * (len(ks) - 1)) / len(ks)
            for i, k in enumerate(ks):
                x = 0.83 + i * (w + 0.3)
                box = sl.shapes.add_shape(5, Inches(x), Inches(top + 0.4), Inches(w), Inches(2.4))
                box.fill.solid()
                box.fill.fore_color.rgb = rgb("FFFFFF")
                box.line.color.rgb = rgb("E1E0D9")
                box.adjustments[0] = 0.06
                box.shadow.inherit = False
                text(sl, x + 0.25, top + 0.6, w - 0.5, 0.5, k["label"], 15, INK2)
                text(sl, x + 0.25, top + 1.1, w - 0.5, 0.9, V.fmt(k["value"], k.get("format", "number"), k.get("currency")), 36, INK, True)
                if isinstance(k.get("delta"), (int, float)):
                    dv = k["delta"]
                    good = (dv >= 0) == k.get("up_is_good", True)
                    col = INK2 if dv == 0 else ("006300" if good else "D03B3B")
                    text(sl, x + 0.25, top + 2.0, w - 0.5, 0.5, "%s %s %s" % ("▲" if dv > 0 else ("▼" if dv < 0 else "●"), V.fmt_delta(dv, k.get("delta_kind", "percent")), k.get("delta_label", "")), 13, col, True)
        elif t == "chart":
            c = s["chart"]
            kind = {"line": XL_CHART_TYPE.LINE, "bar": XL_CHART_TYPE.COLUMN_CLUSTERED,
                    "hbar": XL_CHART_TYPE.BAR_CLUSTERED, "stacked": XL_CHART_TYPE.COLUMN_STACKED}[c.get("type", "line")]
            cd = CategoryChartData()
            xs = list(c["x"])
            series = c["series"]
            if c.get("type") == "hbar":  # PowerPoint draws bar categories bottom-up
                xs = xs[::-1]
                series = [dict(series[0], values=series[0]["values"][::-1])]
            cd.categories = [str(x) for x in xs]
            for sr in series:
                cd.add_series(sr["name"], [v if v is not None else None for v in sr["values"]])
            h = 4.4 if not s.get("takeaway") else 3.9
            gf = sl.shapes.add_chart(kind, Inches(0.83), Inches(top), Inches(11.6), Inches(h), cd)
            ch = gf.chart
            ch.font.size, ch.font.name = Pt(12), "Arial"
            ch.has_title = False
            ch.category_axis.tick_label_position = XL_TICK_LABEL_POSITION.LOW
            ch.has_legend = len(series) > 1
            if ch.has_legend:
                ch.legend.position, ch.legend.include_in_layout = XL_LEGEND_POSITION.TOP, False
            numfmt = {"percent": '0.0%', "currency": ('"₹"#,##,##0' if c.get("currency") == "INR" else '"$"#,##0')}.get(c.get("format"), '#,##0')
            va = ch.value_axis
            va.has_major_gridlines = True
            va.major_gridlines.format.line.color.rgb = rgb("E1E0D9")
            va.tick_labels.number_format, va.tick_labels.number_format_is_linked = numfmt, False
            va.format.line.fill.background()
            ch.category_axis.format.line.color.rgb = rgb("C3C2B7")
            hl = c.get("highlight")
            for i, plot_s in enumerate(ch.plots[0].series):
                name = series[i]["name"]
                col = V.PALETTE_LIGHT[i % 8].lstrip("#")
                if hl and c.get("type") != "hbar":
                    col = ACCENT if name == hl else MUTED
                if kind == XL_CHART_TYPE.LINE:
                    plot_s.format.line.color.rgb = rgb(col)
                    plot_s.format.line.width = Pt(2.25)
                    plot_s.smooth = False
                else:
                    plot_s.format.fill.solid()
                    plot_s.format.fill.fore_color.rgb = rgb(col)
                    if c.get("type") == "hbar" and hl:
                        for j, x in enumerate(xs):
                            pt = plot_s.points[j]
                            pt.format.fill.solid()
                            pt.format.fill.fore_color.rgb = rgb(ACCENT if x == hl else MUTED)
            if kind != XL_CHART_TYPE.LINE and len(series) == 1 and len(xs) <= 12:
                pl = ch.plots[0]
                pl.has_data_labels = True
                pl.data_labels.number_format, pl.data_labels.number_format_is_linked = numfmt, False
                pl.data_labels.position = XL_LABEL_POSITION.OUTSIDE_END
                pl.data_labels.font.size = Pt(12)
                pl.data_labels.font.color.rgb = rgb(INK2)
            if kind != XL_CHART_TYPE.LINE:
                ch.plots[0].gap_width = 80
                ch.plots[0].overlap = 100 if c.get("type") == "stacked" else -10
            if s.get("takeaway"):
                text(sl, 0.83, top + h + 0.1, 11.6, 0.5, s["takeaway"], 15, INK2)
        elif t in ("table", "next_steps"):
            tbl = s["table"] if t == "table" else {"columns": ["Action", "Owner", "By when"], "rows": [[i.get("action", ""), i.get("owner", ""), i.get("date", "")] for i in s.get("items", [])]}
            rows, cols = len(tbl["rows"]) + 1, len(tbl["columns"])
            gt = sl.shapes.add_table(rows, cols, Inches(0.83), Inches(top), Inches(11.6), Inches(0.45 * rows)).table
            fm = tbl.get("formats") or ["number"] * cols
            for j, cname in enumerate(tbl["columns"]):
                cell = gt.cell(0, j)
                cell.text = str(cname)
            for i, r in enumerate(tbl["rows"], 1):
                for j, v in enumerate(r):
                    gt.cell(i, j).text = V.fmt(v, fm[j], tbl.get("currency"), False) if isinstance(v, (int, float)) else str(v)
            isnum = [all(isinstance(rw[j], (int, float)) for rw in tbl["rows"]) for j in range(cols)]
            for i in range(rows):
                for j in range(cols):
                    cell = gt.cell(i, j)
                    if isnum[j]:
                        for p in cell.text_frame.paragraphs:
                            p.alignment = PP_ALIGN.RIGHT
                    cell.fill.solid()
                    cell.fill.fore_color.rgb = rgb("F1F0EB" if i == 0 else "FFFFFF")
                    for p in cell.text_frame.paragraphs:
                        for r in p.runs:
                            r.font.size, r.font.name = Pt(14), "Arial"
                            r.font.bold = i == 0
                            r.font.color.rgb = rgb(INK if i else INK2)
        foot(sl, n, total)
    prs.save(path)
    return True


def main():
    args = sys.argv[1:]
    if not args:
        sys.exit(__doc__)
    out = args[args.index("--out") + 1] if "--out" in args else "deck"
    spec = run_analysis(args[0])
    probs = validate(spec)
    if probs:
        sys.exit("spec problems:\n- " + "\n- ".join(probs))
    with open(out + ".spec.json", "w", encoding="utf-8") as f:
        json.dump(spec, f, indent=2, ensure_ascii=False)
    with open(out + ".html", "w", encoding="utf-8") as f:
        f.write(build_html(spec))
    made = [out + ".html", out + ".spec.json"]
    if "--no-pdf" not in args:
        try:
            render.html_to_pdf(out + ".html", out + ".pdf")
            made.append(out + ".pdf")
        except Exception as e:  # noqa: BLE001
            print("PDF skipped: %s" % e)
    if "--no-pptx" not in args:
        if build_pptx(spec, out + ".pptx"):
            made.append(out + ".pptx")
        else:
            print("PowerPoint skipped: run `pip install python-pptx` and build again")
    print("built " + ", ".join(made))


if __name__ == "__main__":
    main()
