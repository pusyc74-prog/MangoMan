"""Build a strategy report (HTML, PDF, Markdown, and a calendar CSV) from report.json.

Usage: python3 build_report.py report.json --out report [--no-pdf]
Writes <out>.html (the designed report), <out>.pdf (A4), <out>.md (plain text
to paste anywhere) and, when there is a calendar, <out>-calendar.csv.

The model writes the research, the strategy and the plan as typed blocks;
this script lays them out the same tested way every time: cover, summary,
one section per part, sources with links. It does no thinking of its own.
"""
import csv
import datetime
import html
import json
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import brandkit as BK  # noqa: E402
import render  # noqa: E402
import vizlib as V  # noqa: E402

KINDS = {
    "social": ("Social media strategy", ["research", "competitors", "audience", "strategy", "voice", "pillars", "platforms", "calendar", "measure"]),
    "marketing": ("Marketing strategy", ["market", "competitors", "audience", "positioning", "swot", "channels", "plan", "measure"]),
    "seo": ("Search strategy", ["audit", "competitors", "keywords", "content", "technical", "measure"]),
}
# The block a section must hold to do its job.
NEEDS = {"pillars": "pillars", "calendar": "calendar", "measure": "kpis", "voice": "voice", "swot": "swot",
         "plan": "timeline", "audit": "issues", "keywords": "keywords"}
BLOCKS = ("text", "points", "table", "cards", "pillars", "voice", "swot", "kpis", "calendar", "timeline", "issues", "keywords", "budget")
PRIORITY = {"high": "bad", "medium": "warn", "low": "soft"}
CITE = re.compile(r"\[(s\d+)\]")
e = lambda s: html.escape(str(s if s is not None else ""), quote=True)


def load(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def day(s):
    try:
        return datetime.date.fromisoformat(str(s))
    except ValueError:
        return None


def nice(d):
    return "%d %s %d" % (d.day, d.strftime("%b"), d.year) if d else ""


def inline(s, sources):
    """Escaped text with [s1] citations as numbered links to the sources list."""
    order = {x.get("id"): i + 1 for i, x in enumerate(sources)}
    return CITE.sub(lambda m: '<a class="cite" href="#%s">%s</a>' % (m.group(1), order.get(m.group(1), "?")), e(s))


# ---------- blocks ----------

def block_html(b, sources, order=None):
    t, I = b.get("type"), lambda s: inline(s, sources)
    if t == "text":
        return "".join("<p>%s</p>" % I(p) for p in str(b.get("text", "")).split("\n\n") if p.strip())
    if t == "points":
        return '<ul class="pts">%s</ul>' % "".join("<li>%s</li>" % I(x) for x in b.get("items", []))
    if t == "table":
        head = "".join("<th>%s</th>" % e(c) for c in b.get("columns", []))
        rows = "".join("<tr>%s</tr>" % "".join("<td>%s</td>" % I(c) for c in r) for r in b.get("rows", []))
        return '<div class="tw"><table><thead><tr>%s</tr></thead><tbody>%s</tbody></table></div>' % (head, rows)
    if t == "cards":
        return '<div class="cards">%s</div>' % "".join(
            '<div class="card"><div class="ct">%s%s</div>%s%s</div>' % (
                e(c.get("title")), ' <span class="tag soft">%s</span>' % e(c["tag"]) if c.get("tag") else "",
                "<p>%s</p>" % I(c["text"]) if c.get("text") else "",
                '<ul class="pts">%s</ul>' % "".join("<li>%s</li>" % I(x) for x in c.get("points", [])) if c.get("points") else "")
            for c in b.get("items", []))
    if t == "pillars":
        items = b.get("items", [])
        bar = '<div class="mix">%s</div>' % "".join('<i class="p%d" style="width:%s%%" title="%s %s%%"></i>' % (
            i % 6 + 1, float(p.get("share", 0)), e(p.get("name")), e(p.get("share"))) for i, p in enumerate(items))
        cards = "".join('<div class="card"><div class="ct"><span class="dot p%d"></span>%s <span class="share">%s%%</span></div><p>%s</p>%s</div>' % (
            i % 6 + 1, e(p.get("name")), e(p.get("share")), I(p.get("why", "")),
            '<ul class="pts">%s</ul>' % "".join("<li>%s</li>" % I(x) for x in p.get("ideas", []))) for i, p in enumerate(items))
        return bar + '<div class="cards">%s</div>' % cards
    if t == "voice":
        traits = "".join('<div class="card"><div class="ct">%s</div><p>%s</p><p class="do"><b>Do:</b> %s</p><p class="dont"><b>Avoid:</b> %s</p></div>' % (
            e(v.get("trait")), I(v.get("means", "")), I(v.get("do", "")), I(v.get("dont", ""))) for v in b.get("traits", []))
        sample = "".join("<blockquote>%s</blockquote>" % I(s) for s in b.get("samples", []))
        return '<div class="cards">%s</div>%s' % (traits, '<div class="samples"><div class="lab">Sounds like</div>%s</div>' % sample if sample else "")
    if t == "swot":
        q = (("strengths", "Strengths", "good"), ("weaknesses", "Weaknesses", "bad"), ("opportunities", "Opportunities", "info"), ("threats", "Threats", "warn"))
        return '<div class="swot">%s</div>' % "".join('<div class="card %s"><div class="ct">%s</div><ul class="pts">%s</ul></div>' % (
            cls, lab, "".join("<li>%s</li>" % I(x) for x in b.get(k, []))) for k, lab, cls in q)
    if t == "kpis":
        return '<div class="kpis">%s</div>' % "".join(
            '<div class="kpi"><div class="lab">%s</div><div class="num">%s</div><div class="foot">%s</div></div>' % (
                e(k.get("metric")), e(k.get("target")), I(k.get("how", "")) + (" · " + e(k["when"]) if k.get("when") else ""))
            for k in b.get("items", []))
    if t == "timeline":
        return '<ol class="tl">%s</ol>' % "".join('<li><div class="when">%s</div><div><b>%s</b>%s</div></li>' % (
            e(x.get("when")), I(x.get("what", "")), "<p>%s</p>" % I(x["detail"]) if x.get("detail") else "") for x in b.get("items", []))
    if t == "issues":
        rows = "".join('<tr><td><span class="tag %s">%s</span></td><td><b>%s</b><div class="mut">%s</div></td><td>%s</td><td>%s</td></tr>' % (
            PRIORITY.get(str(x.get("priority", "")).lower(), "soft"), e(str(x.get("priority", "")).capitalize()), I(x.get("issue", "")),
            I(x.get("where", "")), I(x.get("why", "")), I(x.get("fix", ""))) for x in b.get("items", []))
        return '<div class="tw"><table><thead><tr><th>Priority</th><th>What</th><th>Why it matters</th><th>Fix</th></tr></thead><tbody>%s</tbody></table></div>' % rows
    if t == "keywords":
        rows = "".join('<tr><td><b>%s</b></td><td>%s</td><td>%s</td><td><span class="tag %s">%s</span></td><td>%s</td></tr>' % (
            e(x.get("keyword")), e(x.get("intent", "")), I(x.get("page", "")), PRIORITY.get(str(x.get("priority", "")).lower(), "soft"),
            e(str(x.get("priority", "")).capitalize()), I(x.get("note", ""))) for x in b.get("items", []))
        return '<div class="tw"><table><thead><tr><th>Search phrase</th><th>Looking to</th><th>Page</th><th>Priority</th><th>Why</th></tr></thead><tbody>%s</tbody></table></div>' % rows
    if t == "budget":
        total, cur = float(b.get("total", 0) or 0), b.get("currency")
        items = b.get("items", [])
        bar = '<div class="mix">%s</div>' % "".join('<i class="p%d" style="width:%s%%"></i>' % (i % 6 + 1, float(x.get("share", 0))) for i, x in enumerate(items))
        rows = "".join('<tr><td><span class="dot p%d"></span> %s</td><td>%s%%</td><td>%s</td><td>%s</td></tr>' % (
            i % 6 + 1, e(x.get("channel")), e(x.get("share")), e(V.fmt(total * float(x.get("share", 0)) / 100, "currency", cur, compact=False)), I(x.get("why", "")))
            for i, x in enumerate(items))
        return bar + '<div class="tw"><table><thead><tr><th>Where</th><th>Share</th><th>Amount</th><th>Why</th></tr></thead><tbody>%s</tbody></table></div><p class="mut">Of a total of %s, as you gave it.</p>' % (
            rows, e(V.fmt(total, "currency", cur, compact=False)))
    if t == "calendar":
        return calendar_html(b, sources, order or {})
    return ""


def calendar_html(b, sources, order):
    """Month grids with each day's posts, then the full list. A pillar keeps
    the colour it has in the pillars block (order: pillar name to 1..6)."""
    posts = sorted((p for p in b.get("posts", []) if day(p.get("date"))), key=lambda p: (p["date"], p.get("platform", "")))
    if not posts:
        return ""
    pillars = dict(order)
    by_day = {}
    for p in posts:
        pillars.setdefault(p.get("pillar", ""), len(pillars) % 6 + 1)
        by_day.setdefault(p["date"], []).append(p)
    months, d = [], day(posts[0]["date"]).replace(day=1)
    last = day(posts[-1]["date"])
    while d <= last:
        cells = ['<div class="dow">%s</div>' % w for w in ("Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun")]
        cells += ['<div class="cell off"></div>'] * d.weekday()
        cur = d
        while cur.month == d.month:
            items = "".join('<span class="ev p%d" title="%s">%s</span>' % (pillars[p.get("pillar", "")], e(p.get("idea")), e(p.get("platform", ""))) for p in by_day.get(cur.isoformat(), []))
            cells.append('<div class="cell"><b>%d</b>%s</div>' % (cur.day, items))
            cur += datetime.timedelta(days=1)
        months.append('<div class="month"><div class="mt">%s</div><div class="grid7">%s</div></div>' % (d.strftime("%B %Y"), "".join(cells)))
        d = cur
    used = {p.get("pillar", "") for p in posts}
    legend = '<div class="legend">%s</div>' % "".join('<span><i class="p%d"></i>%s</span>' % (n, e(k)) for k, n in pillars.items() if k in used)
    rows = "".join('<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>' % (
        nice(day(p["date"])), e(p.get("platform")), e(p.get("format", "")), e(p.get("pillar", "")), inline(p.get("idea", ""), sources)) for p in posts)
    return legend + "".join(months) + '<div class="tw"><table><thead><tr><th>Date</th><th>Where</th><th>Format</th><th>Pillar</th><th>Post</th></tr></thead><tbody>%s</tbody></table></div>' % rows


# ---------- page ----------

CSS = """
*{box-sizing:border-box}html,body{margin:0}
body{background:#%(tint)s;color:#%(text)s;font:15px/1.6 %(html_body)s;-webkit-font-smoothing:antialiased}
h1,h2,h3,.num,.ct{font-family:%(html_head)s;letter-spacing:-.01em}
.wrap{max-width:1040px;margin:0 auto;padding:0 22px}
.cover{background:#%(dark)s;color:#fff;padding:54px 0 46px}
.cover .kind{color:#%(accent)s;font-weight:700;text-transform:uppercase;font-size:12.5px;letter-spacing:.08em}
.cover h1{font-size:38px;line-height:1.15;margin:8px 0 10px}.cover p{margin:0;color:#%(on_dark2)s}
.cover img{max-height:46px;max-width:160px;margin-bottom:22px;background:#fff;border-radius:8px;padding:6px}
nav.toc{display:flex;gap:6px;flex-wrap:wrap;margin:22px 0 0}nav.toc a{color:#%(on_dark2)s;text-decoration:none;font-size:13px;border:1px solid #%(dim)s;border-radius:20px;padding:3px 11px}
main{padding:28px 0 50px}
section.sec{background:#fff;border:1px solid #%(grid)s;border-radius:14px;padding:24px 26px;margin:0 0 18px;box-shadow:0 1px 2px rgba(0,0,0,.04),0 4px 16px rgba(0,0,0,.04);break-inside:avoid-page}
section.sec h2{font-size:21px;margin:0 0 4px}.intro{color:#%(muted)s;margin:0 0 14px}
.summary{border-left:4px solid #%(accent_fill)s}
.pts{margin:6px 0;padding-left:20px}.pts li{margin:3px 0}
.cards{display:grid;grid-template-columns:repeat(auto-fit,minmax(230px,1fr));gap:12px;margin:10px 0}
.card{border:1px solid #%(grid)s;border-radius:12px;padding:14px 16px;background:#fff}
.card .ct{font-weight:700;font-size:15.5px;margin-bottom:4px;display:flex;gap:8px;align-items:center;flex-wrap:wrap}
.card p{margin:4px 0;font-size:14px}.do b{color:#%(good)s}.dont b{color:#%(bad)s}
.tag{display:inline-block;font-size:11.5px;font-weight:700;border-radius:20px;padding:1px 9px;white-space:nowrap;font-family:%(html_body)s}
.tag.bad{background:#fbe9e7;color:#b3261e}.tag.warn{background:#fff3dc;color:#8a5a00}.tag.soft{background:#%(tint)s;color:#%(muted)s}
.swot{display:grid;grid-template-columns:1fr 1fr;gap:12px}.swot .good{border-top:4px solid #%(good)s}.swot .bad{border-top:4px solid #%(bad)s}.swot .info{border-top:4px solid #2a78d6}.swot .warn{border-top:4px solid #eda100}
.kpis{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:12px}
.kpi{border:1px solid #%(grid)s;border-radius:12px;padding:14px 16px}.kpi .lab{color:#%(muted)s;font-size:12.5px;font-weight:600}
.kpi .num{font-size:24px;font-weight:700;margin:4px 0}.kpi .foot{font-size:13px;color:#%(muted)s}
.mix{display:flex;height:14px;border-radius:7px;overflow:hidden;gap:2px;margin:6px 0 4px}.mix i{display:block;height:100%%}
.share{margin-left:auto;font-family:%(html_body)s;font-size:13px;color:#%(muted)s;font-weight:600}
.dot{width:10px;height:10px;border-radius:3px;display:inline-block}
.p1{background:#1f8a5a}.p2{background:#eda100}.p3{background:#2a78d6}.p4{background:#eb6834}.p5{background:#6250d6}.p6{background:#e87ba4}
.samples{margin-top:12px}.lab{color:#%(muted)s;font-size:12.5px;font-weight:600}
blockquote{margin:8px 0;padding:10px 14px;border-left:3px solid #%(accent_fill)s;background:#%(tint)s;border-radius:6px}
.tl{list-style:none;margin:8px 0;padding:0}.tl li{display:grid;grid-template-columns:150px 1fr;gap:14px;padding:10px 0;border-top:1px solid #%(grid)s}
.tl li:first-child{border-top:0}.tl .when{color:#%(accent_dark)s;font-weight:700}.tl p{margin:2px 0 0;font-size:14px;color:#%(muted)s}
.tw{overflow-x:auto;margin:10px 0}table{width:100%%;border-collapse:collapse;font-size:13.5px}
th{text-align:left;color:#%(muted)s;font-weight:600;font-size:12.5px;padding:8px 10px;border-bottom:1px solid #%(grid)s;white-space:nowrap}
td{padding:8px 10px;border-bottom:1px solid #%(grid)s;vertical-align:top}.mut{color:#%(muted)s;font-size:12.5px}
.legend{display:flex;flex-wrap:wrap;gap:6px 14px;font-size:13px;color:#%(muted)s;margin:6px 0 10px}.legend i{display:inline-block;width:10px;height:10px;border-radius:3px;margin-right:6px}
.month{margin:12px 0}.mt{font-weight:700;margin-bottom:6px}
.grid7{display:grid;grid-template-columns:repeat(7,minmax(0,1fr));gap:4px}
.dow{font-size:11.5px;color:#%(muted)s;text-align:center}
.cell{min-height:62px;border:1px solid #%(grid)s;border-radius:8px;padding:4px 5px;font-size:11.5px;display:flex;flex-direction:column;gap:2px}
.cell.off{border:0}.cell b{font-size:11.5px;color:#%(muted)s}
.ev{color:#fff;border-radius:4px;padding:1px 4px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
a.cite{font-size:11px;vertical-align:super;color:#%(accent_dark)s;text-decoration:none;font-weight:700;margin-left:1px}
.sources li{margin:4px 0;font-size:13.5px}.sources a{color:#%(accent_dark)s;word-break:break-all}
.banner{background:#fff3dc;color:#5c3d00;border-radius:10px;padding:12px 16px;margin:0 0 18px}
footer{color:#%(muted)s;font-size:12.5px;padding:0 0 40px}
@media (max-width:700px){.swot{grid-template-columns:1fr}.tl li{grid-template-columns:1fr;gap:2px}.cover h1{font-size:28px}.cell{min-height:44px}.ev{font-size:0;height:6px;padding:0}}
@media print{body{background:#fff}section.sec{box-shadow:none}nav.toc{display:none}}
"""


def page(spec, base_dir):
    T, _ = BK.resolve_spec(spec, base_dir)
    sources = spec.get("sources", [])
    kind = spec.get("kind", "marketing")
    label = KINDS.get(kind, KINDS["marketing"])[0]
    when = nice(day(spec.get("date"))) or nice(datetime.date.today())
    logo = '<img src="%s" alt="%s">' % (BK.data_uri(T["logo"]), e(spec.get("for"))) if T.get("logo") else ""
    secs = spec.get("sections", [])
    pillars = [p.get("name") for s in secs for b in s.get("blocks", []) if b.get("type") == "pillars" for p in b.get("items", [])]
    order = {n: i % 6 + 1 for i, n in enumerate(pillars)}
    toc = "".join('<a href="#%s">%s</a>' % (e(s.get("id")), e(s.get("title"))) for s in secs)
    body = []
    if spec.get("no_web"):
        body.append('<div class="banner"><b>Researched without the web.</b> %s Check the market and competitor parts before acting on them.</div>' % e(spec["no_web"]))
    if spec.get("summary"):
        body.append('<section class="sec summary"><h2>In short</h2><ul class="pts">%s</ul></section>' % "".join(
            "<li>%s</li>" % inline(x, sources) for x in spec["summary"]))
    for s in secs:
        body.append('<section class="sec" id="%s"><h2>%s</h2>%s%s</section>' % (
            e(s.get("id")), e(s.get("title")), '<p class="intro">%s</p>' % inline(s["intro"], sources) if s.get("intro") else "",
            "".join(block_html(b, sources, order) for b in s.get("blocks", []))))
    if sources:
        body.append('<section class="sec"><h2>Sources</h2><ol class="sources">%s</ol></section>' % "".join(
            '<li id="%s"><b>%s</b>: %s<br><a href="%s">%s</a></li>' % (e(x.get("id")), e(x.get("title")), e(x.get("found", "")), e(x.get("url")), e(x.get("url")))
            for x in sources))
    css = CSS % {k: v for k, v in T.items() if isinstance(v, str)}
    return ("<!doctype html><html lang=\"%s\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">"
            "<title>%s · %s</title><style>%s</style></head><body>"
            "<header class=\"cover\"><div class=\"wrap\">%s<div class=\"kind\">%s</div><h1>%s</h1><p>%s · %s%s</p><nav class=\"toc\">%s</nav></div></header>"
            "<main class=\"wrap\">%s</main><footer class=\"wrap\">Made with MangoMan. Every number is from your facts or a listed source; plans and targets are recommendations.</footer>"
            "</body></html>") % (e(spec.get("language", "en")), e(spec.get("title") or label), e(spec.get("for")), css, logo, e(label),
                                 e(spec.get("title") or label), e(spec.get("for")), e(when),
                                 " · " + e(spec["period"]) if spec.get("period") else "", toc, "".join(body))


# ---------- plain text ----------

def block_md(b):
    t = b.get("type")
    if t == "text":
        return [b.get("text", "")]
    if t == "points":
        return ["- " + x for x in b.get("items", [])]
    if t == "table":
        cols = b.get("columns", [])
        return ["| " + " | ".join(cols) + " |", "|" + " --- |" * len(cols)] + ["| " + " | ".join(str(c) for c in r) + " |" for r in b.get("rows", [])]
    if t == "cards":
        return ["- **%s**%s %s" % (c.get("title"), " (%s)" % c["tag"] if c.get("tag") else "", " ".join([c.get("text", "")] + c.get("points", []))) for c in b.get("items", [])]
    if t == "pillars":
        return ["- **%s** (%s%%): %s Ideas: %s" % (p.get("name"), p.get("share"), p.get("why", ""), "; ".join(p.get("ideas", []))) for p in b.get("items", [])]
    if t == "voice":
        return ["- **%s**: %s Do: %s Avoid: %s" % (v.get("trait"), v.get("means", ""), v.get("do", ""), v.get("dont", "")) for v in b.get("traits", [])] + ["> " + s for s in b.get("samples", [])]
    if t == "swot":
        return ["**%s**: %s" % (k.capitalize(), "; ".join(b.get(k, []))) for k in ("strengths", "weaknesses", "opportunities", "threats")]
    if t == "kpis":
        return ["- **%s**: %s (%s)" % (k.get("metric"), k.get("target"), k.get("how", "")) for k in b.get("items", [])]
    if t == "timeline":
        return ["- **%s**: %s" % (x.get("when"), x.get("what")) for x in b.get("items", [])]
    if t == "issues":
        return ["- [%s] %s (%s): %s Fix: %s" % (x.get("priority"), x.get("issue"), x.get("where", ""), x.get("why", ""), x.get("fix", "")) for x in b.get("items", [])]
    if t == "keywords":
        return ["- %s (%s, %s): %s" % (x.get("keyword"), x.get("intent", ""), x.get("priority", ""), x.get("note", "")) for x in b.get("items", [])]
    if t == "budget":
        tot = float(b.get("total", 0) or 0)
        return ["- %s: %s%% (%s) %s" % (x.get("channel"), x.get("share"), V.fmt(tot * float(x.get("share", 0)) / 100, "currency", b.get("currency"), compact=False), x.get("why", "")) for x in b.get("items", [])]
    if t == "calendar":
        return ["- %s %s [%s] %s: %s" % (p.get("date"), p.get("platform"), p.get("format", ""), p.get("pillar", ""), p.get("idea")) for p in b.get("posts", [])]
    return []


def markdown(spec):
    out = ["# %s: %s" % (spec.get("title") or KINDS.get(spec.get("kind"), KINDS["marketing"])[0], spec.get("for", "")), ""]
    if spec.get("summary"):
        out += ["## In short"] + ["- " + x for x in spec["summary"]] + [""]
    for s in spec.get("sections", []):
        out += ["## " + str(s.get("title", ""))] + ([s["intro"], ""] if s.get("intro") else [])
        for b in s.get("blocks", []):
            out += block_md(b) + [""]
    if spec.get("sources"):
        out += ["## Sources"] + ["%d. %s: %s %s" % (i + 1, x.get("title"), x.get("found", ""), x.get("url")) for i, x in enumerate(spec["sources"])]
    return "\n".join(out) + "\n"


def calendar_rows(spec):
    return [p for s in spec.get("sections", []) for b in s.get("blocks", []) if b.get("type") == "calendar" for p in b.get("posts", [])]


def main():
    args = sys.argv[1:]
    if not args or args[0].startswith("-"):
        print(__doc__)
        sys.exit(2)
    out = args[args.index("--out") + 1] if "--out" in args else "report"
    spec = load(args[0])
    base = os.path.dirname(os.path.abspath(args[0]))
    with open(out + ".html", "w", encoding="utf-8") as f:
        f.write(page(spec, base))
    with open(out + ".md", "w", encoding="utf-8") as f:
        f.write(markdown(spec))
    rows = calendar_rows(spec)
    if rows:
        with open(out + "-calendar.csv", "w", newline="", encoding="utf-8") as f:
            w = csv.writer(f)
            w.writerow(["date", "platform", "format", "pillar", "post"])
            for p in sorted(rows, key=lambda p: (str(p.get("date")), str(p.get("platform")))):
                w.writerow([p.get("date"), p.get("platform"), p.get("format", ""), p.get("pillar", ""), p.get("idea", "")])
    print("Wrote %s.html, %s.md%s" % (out, out, ", %s-calendar.csv" % out if rows else ""))
    if "--no-pdf" not in args:
        try:
            render.html_to_pdf(out + ".html", out + ".pdf")
            print("Wrote %s.pdf" % out)
        except Exception as err:  # noqa: BLE001 - the HTML is the report; the PDF is a convenience
            print("No PDF: %s" % err)


if __name__ == "__main__":
    main()
