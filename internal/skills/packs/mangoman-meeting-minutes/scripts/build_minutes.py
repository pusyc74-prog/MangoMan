"""Build meeting minutes (PDF, Markdown and an action list) from minutes.json.

Usage: python3 build_minutes.py minutes.json --out minutes [--no-pdf]
Writes <out>.pdf (A4), <out>.md (to paste into email, Slack, Notion or a
doc), <out>-actions.csv (owner, action, due date) and <out>.html.
"""
import csv
import datetime
import html
import json
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import render  # noqa: E402

e = lambda s: html.escape(str(s if s is not None else ""), quote=True)


def load(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def transcript_text(path):
    """Plain text of a .txt, .md, .vtt or .srt transcript (timestamps removed)."""
    with open(path, encoding="utf-8", errors="ignore") as f:
        t = f.read()
    t = re.sub(r"^\d+\s*$|^WEBVTT.*$|^[\d:.,]+\s*-->\s*[\d:.,]+.*$", "", t, flags=re.M)
    return re.sub(r"\n{2,}", "\n", t)


def day(s):
    try:
        return datetime.date.fromisoformat(str(s))
    except ValueError:
        return None


def nice(s):
    d = day(s)
    return d.strftime("%d %b %Y").lstrip("0") if d else str(s or "")


def validate(spec, bdir="."):
    p = []
    for k in ("title", "date", "summary"):
        if not spec.get(k):
            p.append("%s is required" % k)
    if spec.get("date") and not day(spec["date"]):
        p.append("date must be YYYY-MM-DD")
    if not spec.get("attendees"):
        p.append("attendees are required")
    for i, a in enumerate(spec.get("actions", []), 1):
        if not a.get("action"):
            p.append("action %d needs the action text" % i)
        if a.get("due") and not day(a["due"]):
            p.append("action %d: due must be YYYY-MM-DD" % i)
    t = spec.get("transcript")
    if t and not os.path.exists(os.path.join(bdir, t)):
        p.append("transcript not found: %s" % t)
    return p


def names(spec):
    return [a["name"] if isinstance(a, dict) else str(a) for a in spec.get("attendees", [])]


CSS = """
@page { size: A4; margin: 18mm 18mm 18mm; @bottom-right { content: counter(page) " / " counter(pages); font: 8pt sans-serif; color: #888; } }
body { font: 10.5pt/1.55 "Segoe UI", Calibri, Carlito, Arial, sans-serif; color: #1f2430; margin: 0; }
h1 { font-size: 19pt; margin: 0; } h2 { font-size: 12pt; margin: 7mm 0 2mm; padding-bottom: 1mm; border-bottom: 1.5px solid #1f2430; }
.meta { color: #5c6270; margin-top: 1.5mm; } .summary { font-size: 11pt; margin-top: 5mm; }
table { width: 100%; border-collapse: collapse; } th { text-align: left; font-size: 8.5pt; color: #5c6270; padding: 1.5mm 2mm; border-bottom: 1px solid #1f2430; }
td { padding: 2mm; border-bottom: 1px solid #e3e5ea; vertical-align: top; } tr { break-inside: avoid; }
ul { margin: 1mm 0; padding-left: 5mm; } li { margin: 0 0 1.2mm; } h3 { font-size: 10.5pt; margin: 4mm 0 1mm; }
"""


def build_html(spec):
    meta = " · ".join(x for x in (nice(spec["date"]), spec.get("time"), spec.get("location")) if x)
    att = ", ".join("%s%s" % (n["name"], " (%s)" % n["role"] if n.get("role") else "") if isinstance(n, dict) else str(n) for n in spec["attendees"])
    out = ["<h1>%s</h1><div class='meta'>%s</div><div class='meta'>Attendees: %s%s</div>" % (
        e(spec["title"]), e(meta), e(att), "<br>Absent: %s" % e(", ".join(spec["absent"])) if spec.get("absent") else ""),
        "<p class='summary'>%s</p>" % e(spec["summary"])]
    if spec.get("decisions"):
        out.append("<h2>Decisions</h2><ul>%s</ul>" % "".join("<li>%s</li>" % e(d["text"] if isinstance(d, dict) else d) for d in spec["decisions"]))
    if spec.get("actions"):
        out.append("<h2>Actions</h2><table><tr><th>Action</th><th>Owner</th><th>Due</th></tr>%s</table>" % "".join(
            "<tr><td>%s</td><td>%s</td><td>%s</td></tr>" % (e(a["action"]), e(a.get("owner", "")), e(nice(a.get("due")))) for a in spec["actions"]))
    if spec.get("topics"):
        out.append("<h2>Discussion</h2>" + "".join("<h3>%s</h3><ul>%s</ul>" % (e(t["title"]), "".join("<li>%s</li>" % e(x) for x in t.get("points", [])))
                                                    for t in spec["topics"]))
    if spec.get("open_questions"):
        out.append("<h2>Open questions</h2><ul>%s</ul>" % "".join("<li>%s</li>" % e(q) for q in spec["open_questions"]))
    nm = spec.get("next_meeting")
    if nm:
        out.append("<h2>Next meeting</h2><p>%s</p>" % e(" · ".join(x for x in (nice(nm.get("date")), nm.get("time"), nm.get("agenda")) if x)))
    return '<!doctype html><html lang="en"><head><meta charset="utf-8"><title>%s</title><style>%s</style></head><body>%s</body></html>' % (
        e(spec["title"]), CSS, "".join(out))


def build_md(spec):
    out = ["# %s" % spec["title"], "", "%s%s" % (nice(spec["date"]), " · " + spec["time"] if spec.get("time") else ""),
           "Attendees: " + ", ".join(names(spec)), "", spec["summary"], ""]
    if spec.get("decisions"):
        out += ["## Decisions", ""] + ["- " + (d["text"] if isinstance(d, dict) else d) for d in spec["decisions"]] + [""]
    if spec.get("actions"):
        out += ["## Actions", ""] + ["- [ ] %s (%s%s)" % (a["action"], a.get("owner", "no owner"), ", by " + nice(a["due"]) if a.get("due") else "") for a in spec["actions"]] + [""]
    for t in spec.get("topics", []):
        out += ["### " + t["title"], ""] + ["- " + x for x in t.get("points", [])] + [""]
    if spec.get("open_questions"):
        out += ["## Open questions", ""] + ["- " + q for q in spec["open_questions"]] + [""]
    return "\n".join(out)


def main():
    a = sys.argv[1:]
    if not a:
        sys.exit(__doc__)
    spec = load(a[0])
    out = a[a.index("--out") + 1] if "--out" in a else "minutes"
    probs = validate(spec, os.path.dirname(os.path.abspath(a[0])))
    if probs:
        sys.exit("spec problems:\n- " + "\n- ".join(probs))
    with open(out + ".html", "w", encoding="utf-8") as f:
        f.write(build_html(spec))
    with open(out + ".md", "w", encoding="utf-8") as f:
        f.write(build_md(spec))
    with open(out + "-actions.csv", "w", newline="", encoding="utf-8-sig") as f:
        w = csv.writer(f)
        w.writerow(["Owner", "Action", "Due"])
        w.writerows([[x.get("owner", ""), x["action"], x.get("due", "")] for x in spec.get("actions", [])])
    made = [out + ".md", out + "-actions.csv", out + ".html"]
    if "--no-pdf" not in a:
        try:
            render.html_to_pdf(out + ".html", out + ".pdf")
            made.append(out + ".pdf")
        except Exception as ex:  # noqa: BLE001
            print("PDF skipped: %s" % ex)
    print("built " + ", ".join(made))


if __name__ == "__main__":
    main()
