"""Build website copy from site.json.

Usage: python3 build_copy.py site.json --out site
Writes into the out folder: copy/<slug>.md (one Markdown file per page, ready
to paste into a CMS), pages.csv (one row per page: title, description, h1,
button, body), sitemap.md (every page with its goal, keyword and links) and
pages.json (the pages in the landing page pack's section format).

Text fields accept **bold** and [link text](https://... or /page). A link to
/ is the first page (home); /about is the page whose slug is about.
"""
import csv
import json
import os
import re
import sys

TYPES = ("hero", "text", "features", "steps", "stats", "testimonials", "faq", "cta")
LIST_KEY = {"features": "items", "steps": "items", "stats": "items", "testimonials": "items", "faq": "items", "text": "body"}
SKIP = {"type", "href", "id", "nav", "tone"}
LINK = re.compile(r"\[([^\]]+)\]\((https?://[^)\s]+|/[a-z0-9-]*)\)")


def load(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def texts(o):
    """Every piece of copy inside a page's sections (links, ids and types left out)."""
    if isinstance(o, str):
        yield o
    elif isinstance(o, dict):
        for k, v in o.items():
            if k not in SKIP:
                yield from texts(v)
    elif isinstance(o, list):
        for v in o:
            yield from texts(v)


def target(href, slugs):
    """The slug an internal link points to, or None when it points nowhere."""
    if href == "/":
        return slugs[0]
    return href.strip("/") if href.startswith("/") and href.strip("/") in slugs else None


def internal_links(page):
    """Every internal link on a page: from text links and from buttons."""
    out = [h for t in texts(page["sections"]) for _, h in LINK.findall(t) if h.startswith("/")]
    out += [c["href"] for s in page["sections"] for c in (s.get("cta"), s.get("cta2")) if isinstance(c, dict) and str(c.get("href", "")).startswith("/")]
    return out


def validate(spec):
    p = []
    for k in ("brand", "promise"):
        if not spec.get(k):
            p.append("%s is required" % k)
    if not str(spec.get("url", "")).startswith("https://"):
        p.append("url must be the site address starting with https://")
    pages = spec.get("pages")
    if not isinstance(pages, list) or not pages:
        return p + ["pages is required"]
    slugs = [pg.get("slug", "") for pg in pages]
    for pg in pages:
        tag = "page %s" % pg.get("slug", "?")
        if not re.match(r"^[a-z0-9]+(?:-[a-z0-9]+)*$", pg.get("slug", "")):
            p.append("%s: slug is lowercase words joined by dashes" % tag)
        if slugs.count(pg.get("slug")) > 1:
            p.append("%s: slug used twice" % tag)
        for k in ("name", "goal", "keyword"):
            if not pg.get(k):
                p.append("%s: %s is required" % (tag, k))
        if not (pg.get("meta") or {}).get("title") or not (pg.get("meta") or {}).get("description"):
            p.append("%s: meta.title and meta.description are required" % tag)
        c = pg.get("cta") or {}
        if not c.get("label") or not c.get("href"):
            p.append("%s: cta needs a label and an href" % tag)
        secs = pg.get("sections")
        if not isinstance(secs, list) or not secs or secs[0].get("type") != "hero":
            p.append("%s: sections must start with a hero" % tag)
            continue
        if sum(1 for s in secs if s.get("type") == "hero") > 1:
            p.append("%s: only one hero (it holds the h1)" % tag)
        for i, s in enumerate(secs, 1):
            t = s.get("type")
            if t not in TYPES:
                p.append("%s section %d: type must be one of %s" % (tag, i, ", ".join(TYPES)))
            elif not s.get("headline"):
                p.append("%s section %d (%s): needs a headline" % (tag, i, t))
            elif t in LIST_KEY and not s.get(LIST_KEY[t]):
                p.append("%s section %d (%s): needs %s" % (tag, i, t, LIST_KEY[t]))
        for h in internal_links(pg):
            if target(h, slugs) is None:
                p.append("%s: link %s points to no page (slugs: %s)" % (tag, h, ", ".join(slugs)))
    return p


def btn(c):
    return "**[%s](%s)**" % (c["label"], c["href"])


def section_md(s):
    """One section as Markdown."""
    t, head = s["type"], "# " if s["type"] == "hero" else "## "
    out = [head + s["headline"]]
    if s.get("sub"):
        out.append(s["sub"])
    if t == "hero" and s.get("proof"):
        out.append(s["proof"])
    items = s.get("items", [])
    if t == "text":
        out += s["body"]
    elif t in ("features", "steps"):
        out.append("\n".join("%s **%s**: %s" % ("1." if t == "steps" else "-", i["title"], i["text"]) for i in items))
    elif t == "stats":
        out.append("\n".join("- **%s** %s" % (i["value"], i["label"]) for i in items))
    elif t == "testimonials":
        out.append("\n\n".join("> %s\n>\n> %s%s" % (i["quote"], i["author"], ", " + i["role"] if i.get("role") else "") for i in items))
    elif t == "faq":
        out.append("\n\n".join("### %s\n\n%s" % (i["q"], i["a"]) for i in items))
    if isinstance(s.get("cta"), dict):
        out.append(btn(s["cta"]))
    return "\n\n".join(out)


def page_md(page):
    return "\n\n".join(section_md(s) for s in page["sections"]) + "\n"


def sitemap_md(spec):
    slugs = [pg["slug"] for pg in spec["pages"]]
    rows = ["| Page | Address | Goal | Keyword | Button | Links to |", "| --- | --- | --- | --- | --- | --- |"]
    for i, pg in enumerate(spec["pages"]):
        to = sorted({target(h, slugs) for h in internal_links(pg)} - {pg["slug"]})
        rows.append("| %s | %s | %s | %s | %s | %s |" % (pg["name"], "/" if i == 0 else "/" + pg["slug"], pg["goal"], pg["keyword"],
                                                         pg["cta"]["label"], ", ".join(to) or "none"))
    return "# %s: sitemap\n\n%s\n" % (spec["brand"], "\n".join(rows))


def build(spec, out):
    os.makedirs(os.path.join(out, "copy"), exist_ok=True)
    for pg in spec["pages"]:
        with open(os.path.join(out, "copy", pg["slug"] + ".md"), "w", encoding="utf-8") as f:
            f.write(page_md(pg))
    with open(os.path.join(out, "pages.csv"), "w", encoding="utf-8", newline="") as f:
        w = csv.writer(f)
        w.writerow(["slug", "name", "title", "description", "h1", "button", "body"])
        for pg in spec["pages"]:
            w.writerow([pg["slug"], pg["name"], pg["meta"]["title"], pg["meta"]["description"], pg["sections"][0]["headline"], pg["cta"]["label"], page_md(pg)])
    with open(os.path.join(out, "sitemap.md"), "w", encoding="utf-8") as f:
        f.write(sitemap_md(spec))
    keep = {"brand": spec["brand"], "url": spec["url"], "facts": spec.get("facts", {}),
            "pages": [{"slug": pg["slug"], "meta": pg["meta"], "sections": pg["sections"]} for pg in spec["pages"]]}
    with open(os.path.join(out, "pages.json"), "w", encoding="utf-8") as f:
        json.dump(keep, f, ensure_ascii=False, indent=2)


def main():
    if len(sys.argv) < 4 or sys.argv[2] != "--out":
        sys.exit(__doc__)
    spec = load(sys.argv[1])
    probs = validate(spec)
    if probs:
        sys.exit("site.json problems:\n- " + "\n- ".join(probs))
    build(spec, sys.argv[3])
    print("Built %d pages into %s" % (len(spec["pages"]), sys.argv[3]))


if __name__ == "__main__":
    main()
