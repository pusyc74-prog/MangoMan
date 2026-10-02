"""Build a resume (HTML and PDF) from resume.json.

Usage: python3 build.py resume.json --template classic|modern --out resume
  classic: single column, ATS-safe (job portals, recruiters' systems)
  modern:  accent colour and a side column (direct sharing, design roles)
"""
import html
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import render  # noqa: E402

e = lambda s: html.escape(str(s or ""), quote=True)


def dates(x):
    s, t = x.get("start", ""), x.get("end", "")
    return e(s + (" – " + t if t else "")) if s or t else ""


def contact_items(r):
    c = r.get("contact", {})
    items = [c.get(k) for k in ("email", "phone", "location") if c.get(k)]
    for l in c.get("links", []):
        items.append('<a href="%s">%s</a>' % (e(l.get("url")), e(l.get("label") or l.get("url"))))
    return [i if i.startswith("<a ") else e(i) for i in items]


def bullets(b):
    return "<ul>%s</ul>" % "".join("<li>%s</li>" % e(x) for x in b) if b else ""


def section(title, body, cls=""):
    return '<section class="%s"><h2>%s</h2>%s</section>' % (cls, e(title), body) if body else ""


def experience(r):
    out = []
    for x in r.get("experience", []):
        where = ", ".join(v for v in (x.get("company"), x.get("location")) if v)
        out.append('<div class="entry"><div class="row"><div><b>%s</b>%s</div><div class="when">%s</div></div>%s</div>' % (
            e(x.get("role")), (" · " + e(where)) if where else "", dates(x), bullets(x.get("bullets", []))))
    return "".join(out)


def education(r):
    return "".join('<div class="entry"><div class="row"><div><b>%s</b>%s</div><div class="when">%s</div></div>%s</div>' % (
        e(x.get("degree")), (" · " + e(x.get("school"))) if x.get("school") else "", e(x.get("year", "")),
        '<p class="det">%s</p>' % e(x["details"]) if x.get("details") else "") for x in r.get("education", []))


def projects(r):
    return "".join('<div class="entry"><div class="row"><div><b>%s</b></div><div class="when">%s</div></div><p class="det">%s</p></div>' % (
        e(p.get("name")), '<a href="%s">%s</a>' % (e(p["link"]), e(p["link"].replace("https://", ""))) if p.get("link") else "",
        e(p.get("description"))) for p in r.get("projects", []))


def skills(r, inline=True):
    sk = r.get("skills", [])
    if not sk:
        return ""
    if inline:
        return "".join('<p class="sk"><b>%s:</b> %s</p>' % (e(g.get("group")), e(", ".join(g.get("items", [])))) for g in sk)
    return "".join('<div class="skg"><h3>%s</h3><p>%s</p></div>' % (e(g.get("group")), e(", ".join(g.get("items", [])))) for g in sk)


def simple_list(r, key):
    items = r.get(key, [])
    return bullets([i if isinstance(i, str) else " · ".join(str(v) for v in i.values() if v) for i in items]) if items else ""


BASE = """
@page { size: %(size)s; margin: %(margin)s; }
* { box-sizing: border-box; margin: 0; padding: 0; }
html { background: #e9e8e3; }
body { font-family: "Inter", "Segoe UI", "Helvetica Neue", Arial, sans-serif; color: #1a1a1a; font-size: 10.2pt; line-height: 1.38; }
.page { width: %(width)s; min-height: %(height)s; margin: 24px auto; background: #fff; padding: %(margin)s; box-shadow: 0 2px 12px rgba(0,0,0,.08); }
@media print { html { background: #fff; } .page { margin: 0; padding: 0; width: auto; min-height: 0; box-shadow: none; } }
a { color: inherit; text-decoration: none; }
ul { padding-left: 14pt; margin-top: 3pt; }
li { margin: 1.5pt 0; }
.entry { margin-top: 8pt; break-inside: avoid; }
.row { display: flex; justify-content: space-between; gap: 12pt; }
.when { color: #555; white-space: nowrap; font-size: 9.6pt; }
.det { color: #333; margin-top: 2pt; }
.sk { margin-top: 3pt; }
"""

CLASSIC = BASE + """
header { text-align: center; margin-bottom: 10pt; }
h1 { font-size: 22pt; letter-spacing: 0.5pt; font-weight: 700; }
.title { font-size: 11.5pt; color: #333; margin-top: 2pt; }
.contact { font-size: 9.6pt; color: #333; margin-top: 4pt; }
.contact span + span::before { content: "  |  "; color: #999; }
h2 { font-size: 10.5pt; letter-spacing: 1.2pt; text-transform: uppercase; border-bottom: 1.2pt solid #1a1a1a; padding-bottom: 2pt; margin: 12pt 0 2pt; }
.summary { margin-top: 4pt; }
"""

MODERN = BASE + """
.page { display: grid; grid-template-columns: 1fr 34%%; column-gap: 20pt; }
@media print { .page { display: grid; } }
header { grid-column: 1 / -1; border-left: 5pt solid %(accent)s; padding-left: 12pt; margin-bottom: 12pt; }
h1 { font-size: 25pt; font-weight: 750; letter-spacing: -0.3pt; line-height: 1.1; }
.title { font-size: 12pt; color: %(accent)s; font-weight: 600; margin-top: 3pt; }
.main h2, .side h2 { font-size: 10pt; letter-spacing: 1.1pt; text-transform: uppercase; color: %(accent)s; margin: 12pt 0 2pt; }
.main > section:first-child h2, .side > section:first-child h2 { margin-top: 0; }
.side { background: #f6f7f9; border-radius: 6pt; padding: 12pt 12pt; align-self: start; font-size: 9.6pt; }
.side .contact span { display: block; margin: 2pt 0; overflow-wrap: anywhere; }
.side .skg { margin-top: 6pt; } .side h3 { font-size: 9.6pt; } .side ul { padding-left: 11pt; }
"""

SIZES = {"A4": ("A4", "210mm", "297mm"), "Letter": ("Letter", "8.5in", "11in")}


def build(r, template="classic"):
    size, width, height = SIZES.get(r.get("page_size", "A4"), SIZES["A4"])
    margin = r.get("margin", "13mm")
    accent = r.get("accent", "#1f5fa8")
    css = (CLASSIC if template == "classic" else MODERN) % {"size": size, "width": width, "height": height, "margin": margin, "accent": e(accent)}
    head = '<header><h1>%s</h1>%s%s</header>' % (
        e(r.get("name")), '<div class="title">%s</div>' % e(r["headline"]) if r.get("headline") else "",
        '<div class="contact">%s</div>' % "".join("<span>%s</span>" % i for i in contact_items(r)) if template == "classic" else "")
    summary = '<p class="summary">%s</p>' % e(r["summary"]) if r.get("summary") else ""
    extra = "".join(section(t, simple_list(r, k)) for k, t in (("certifications", "Certifications"), ("awards", "Awards")))
    if r.get("languages"):
        extra += section("Languages", '<p class="sk">%s</p>' % e(", ".join(r["languages"])))
    if template == "classic":
        body = head + section("Summary", summary) + section("Experience", experience(r)) + section("Projects", projects(r)) + \
            section("Education", education(r)) + section("Skills", skills(r)) + extra
    else:
        side = section("Contact", '<div class="contact">%s</div>' % "".join("<span>%s</span>" % i for i in contact_items(r))) + \
            section("Skills", skills(r, inline=False)) + section("Education", education(r)) + extra
        main = section("Profile", summary) + section("Experience", experience(r)) + section("Projects", projects(r))
        body = head + '<div class="main">%s</div><aside class="side">%s</aside>' % (main, side)
    return '<!doctype html><html lang="en"><head><meta charset="utf-8"><title>%s, resume</title><style>%s</style></head><body><div class="page">%s</div></body></html>' % (
        e(r.get("name")), css, body)


def main():
    a = sys.argv[1:]
    if not a:
        sys.exit(__doc__)
    tpl = a[a.index("--template") + 1] if "--template" in a else "classic"
    out = a[a.index("--out") + 1] if "--out" in a else "resume"
    if tpl not in ("classic", "modern"):
        sys.exit("template must be classic or modern")
    with open(a[0], encoding="utf-8") as f:
        r = json.load(f)
    with open(out + ".html", "w", encoding="utf-8") as f:
        f.write(build(r, tpl))
    made = [out + ".html"]
    try:
        render.html_to_pdf(out + ".html", out + ".pdf")
        made.append(out + ".pdf")
    except Exception as ex:  # noqa: BLE001
        print("PDF skipped: %s" % ex)
    if render.engine() == "playwright":
        render.screenshot(out + ".html", out + ".png", 900, 1200)
        made.append(out + ".png")
    print("built " + ", ".join(made))


if __name__ == "__main__":
    main()
