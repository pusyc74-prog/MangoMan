"""Build an SEO blog article from article.json.

Usage: python3 build_article.py article.json --out article [--no-shots]
Writes into the out folder: article.html (a complete page with title,
description, sharing tags and Article and FAQ structured data), article.md
(to paste into a CMS such as WordPress, Shopify or Webflow), seo.md (title,
description, address, keywords, the structured data block) and preview.png.

Text fields accept **bold**, [link text](https://...) and citation markers
[1] that point to the numbered sources.
"""
import html
import json
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import render  # noqa: E402

LINK = re.compile(r"\[([^\]]+)\]\(((?:https?://|/(?!/))[^)\s]+)\)")
CITE = re.compile(r"\[(\d{1,2})\](?!\()")
e = lambda s: html.escape(str(s if s is not None else ""), quote=True)


def load(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def blocks(spec):
    """Every paragraph-like text in reading order: (place, text)."""
    a = spec["article"]
    out = [("intro", t) for t in a.get("intro", [])]
    for s in a.get("sections", []):
        out += [("section: " + s["h2"], t) for t in s.get("paragraphs", []) + s.get("bullets", [])]
        for sub in s.get("subsections", []):
            out += [("section: " + sub["h3"], t) for t in sub.get("paragraphs", []) + sub.get("bullets", [])]
    out += [("faq", f["a"]) for f in a.get("faq", [])] + [("conclusion", t) for t in a.get("conclusion", [])]
    return out


def headings(spec):
    a = spec["article"]
    return [a["h1"]] + [x for s in a.get("sections", []) for x in [s["h2"]] + [sub["h3"] for sub in s.get("subsections", [])]]


def plain(t):
    return CITE.sub("", LINK.sub(r"\1", t)).replace("**", "")


def validate(spec):
    p = []
    m = spec.get("meta") or {}
    for k in ("title", "description", "slug"):
        if not m.get(k):
            p.append("meta.%s is required" % k)
    if m.get("slug") and not re.match(r"^[a-z0-9]+(?:-[a-z0-9]+)*$", m["slug"]):
        p.append("meta.slug: lowercase words joined by dashes")
    if not spec.get("keyword"):
        p.append("keyword (the main search phrase) is required")
    a = spec.get("article")
    if not isinstance(a, dict) or not a.get("h1") or not isinstance(a.get("sections"), list) or not a["sections"]:
        return p + ["article needs h1 and sections"]
    for s in a["sections"]:
        if not isinstance(s, dict) or not s.get("h2"):
            p.append("every section needs an h2")
        elif any(not isinstance(sub, dict) or not sub.get("h3") for sub in s.get("subsections", [])):
            p.append("every subsection needs an h3")
    if p:
        return p  # the checks below walk the article
    ids = {str(x.get("id")) for x in spec.get("sources", [])}
    for x in spec.get("sources", []):
        if not str(x.get("url", "")).startswith("https://") or not x.get("title"):
            p.append("source %s needs a title and an https url" % x.get("id"))
    for place, t in blocks(spec):
        for n in CITE.findall(t):
            if n not in ids:
                p.append("%s cites [%s], which is not in sources" % (place, n))
    places = {"intro"} | {s.get("h2") for s in a.get("sections", [])}
    for im in spec.get("images", []):
        if not im.get("alt") or not im.get("src"):
            p.append("every image needs src and alt")
        if im.get("after", "intro") not in places:
            p.append("image %s: after must be intro or a section's h2" % im.get("src"))
    return p


def inline(t, cites=True):
    s = e(t)
    s = re.sub(r"\*\*(.+?)\*\*", r"<strong>\1</strong>", s)
    s = LINK.sub(lambda m: '<a href="%s">%s</a>' % (m.group(2), m.group(1)), s)
    if cites:
        s = CITE.sub(r'<sup><a href="#source-\1">[\1]</a></sup>', s)
    return s


def article_html(spec):
    a, m = spec["article"], spec["meta"]
    url = (m.get("url") or "").rstrip("/")
    body = ["<h1>%s</h1>" % e(a["h1"])]
    if spec.get("author") or spec.get("date"):
        body.append('<p class="by">%s</p>' % e(" · ".join(x for x in (spec.get("author"), spec.get("date")) if x)))
    body += ["<p>%s</p>" % inline(t) for t in a.get("intro", [])]
    imgs = {}
    for im in spec.get("images", []):
        imgs.setdefault(im.get("after", "intro"), []).append(im)

    def figs(key):
        return "".join('<figure><img src="%s" alt="%s" loading="lazy">%s</figure>' % (e(i["src"]), e(i["alt"]), "<figcaption>%s</figcaption>" % e(i["caption"]) if i.get("caption") else "")
                       for i in imgs.get(key, []))
    body.append(figs("intro"))
    for s in a["sections"]:
        body.append('<h2 id="%s">%s</h2>' % (re.sub(r"[^a-z0-9]+", "-", s["h2"].lower()).strip("-"), e(s["h2"])))
        body += ["<p>%s</p>" % inline(t) for t in s.get("paragraphs", [])]
        if s.get("bullets"):
            body.append("<ul>%s</ul>" % "".join("<li>%s</li>" % inline(b) for b in s["bullets"]))
        for sub in s.get("subsections", []):
            body.append("<h3>%s</h3>" % e(sub["h3"]))
            body += ["<p>%s</p>" % inline(t) for t in sub.get("paragraphs", [])]
            if sub.get("bullets"):
                body.append("<ul>%s</ul>" % "".join("<li>%s</li>" % inline(b) for b in sub["bullets"]))
        body.append(figs(s["h2"]))
    if a.get("faq"):
        body.append("<h2>Frequently asked questions</h2>" + "".join("<h3>%s</h3><p>%s</p>" % (e(f["q"]), inline(f["a"])) for f in a["faq"]))
    body += ["<p>%s</p>" % inline(t) for t in a.get("conclusion", [])]
    if a.get("cta"):
        body.append('<p><a class="cta" href="%s">%s</a></p>' % (e(a["cta"]["href"]), e(a["cta"]["label"])))
    if spec.get("sources"):
        body.append('<h2>Sources</h2><ol class="src">%s</ol>' % "".join(
            '<li id="source-%s"><a href="%s">%s</a>%s</li>' % (e(x["id"]), e(x["url"]), e(x["title"]), e(", " + x["publisher"]) if x.get("publisher") else "") for x in spec["sources"]))
    ld = [{"@context": "https://schema.org", "@type": "Article", "headline": a["h1"], "description": m["description"],
           **({"author": {"@type": "Person", "name": spec["author"]}} if spec.get("author") else {}),
           **({"datePublished": spec["date"]} if spec.get("date") else {}), **({"mainEntityOfPage": url + "/" + m["slug"]} if url else {})}]
    if a.get("faq"):
        ld.append({"@context": "https://schema.org", "@type": "FAQPage", "mainEntity": [
            {"@type": "Question", "name": f["q"], "acceptedAnswer": {"@type": "Answer", "text": plain(f["a"])}} for f in a["faq"]]})
    head = ['<meta charset="utf-8">', '<meta name="viewport" content="width=device-width, initial-scale=1">', "<title>%s</title>" % e(m["title"]),
            '<meta name="description" content="%s">' % e(m["description"]), '<meta property="og:type" content="article">',
            '<meta property="og:title" content="%s">' % e(m["title"]), '<meta property="og:description" content="%s">' % e(m["description"])]
    if url:
        head += ['<link rel="canonical" href="%s/%s">' % (e(url), e(m["slug"]))]
    head.append('<script type="application/ld+json">%s</script>' % json.dumps(ld, ensure_ascii=False).replace("</", "<\\/"))
    css = ("body{margin:0;background:#fff;color:#1d1d1b;font:18px/1.7 Georgia,Cambria,'Times New Roman',serif}"
           "main{max-width:700px;margin:0 auto;padding:48px 20px 80px}h1,h2,h3{font-family:'Segoe UI',Roboto,Arial,sans-serif;line-height:1.2;letter-spacing:-0.01em}"
           "h1{font-size:2.3rem;margin:0 0 8px}h2{font-size:1.55rem;margin:2.2em 0 .5em}h3{font-size:1.15rem;margin:1.6em 0 .3em}"
           ".by{color:#6b6a65;font:15px 'Segoe UI',Roboto,Arial,sans-serif;margin-bottom:28px}a{color:#1f5fa8}sup a{text-decoration:none;font-size:.75em}"
           "figure{margin:28px 0}img{max-width:100%;height:auto;border-radius:8px}figcaption{color:#6b6a65;font-size:15px}"
           ".cta{display:inline-block;background:#1d1d1b;color:#fff;padding:12px 22px;border-radius:999px;text-decoration:none;font:600 16px 'Segoe UI',Arial,sans-serif}"
           ".src{font-size:15px}")
    return '<!doctype html><html lang="%s"><head>%s<style>%s</style></head><body><main>%s</main></body></html>' % (
        e(spec.get("language", "en")), "\n".join(head), css, "\n".join(x for x in body if x))


def article_md(spec):
    a = spec["article"]
    imgs = lambda key: ["![%s](%s)\n" % (i["alt"], i["src"]) for i in spec.get("images", []) if i.get("after", "intro") == key]
    paras = lambda x: [t + "\n" for t in x.get("paragraphs", [])] + ["- " + b for b in x.get("bullets", [])] + ([""] if x.get("bullets") else [])
    out = ["# " + a["h1"], ""] + [t + "\n" for t in a.get("intro", [])] + imgs("intro")
    for s in a["sections"]:
        out += ["## " + s["h2"], ""] + paras(s)
        for sub in s.get("subsections", []):
            out += ["### " + sub["h3"], ""] + paras(sub)
        out += imgs(s["h2"])
    if a.get("faq"):
        out += ["## Frequently asked questions", ""] + [x for f in a["faq"] for x in ("### " + f["q"], "", f["a"], "")]
    out += [t + "\n" for t in a.get("conclusion", [])]
    if spec.get("sources"):
        out += ["## Sources", ""] + ["%s. [%s](%s)" % (x["id"], x["title"], x["url"]) for x in spec["sources"]]
    return "\n".join(out) + "\n"


def seo_md(spec):
    m = spec["meta"]
    url = (m.get("url") or "").rstrip("/")
    return "\n".join(["# Search settings", "", "Title (%d characters): %s" % (len(m["title"]), m["title"]),
                      "Description (%d characters): %s" % (len(m["description"]), m["description"]),
                      "Address: %s/%s" % (url, m["slug"]) if url else "Address (slug): %s" % m["slug"],
                      "Main keyword: %s" % spec["keyword"], "Other keywords: %s" % ", ".join(spec.get("secondary_keywords", [])), "",
                      "Structured data (Article and FAQ) is in the head of article.html; most SEO plugins (Yoast, Rank Math) add Article data themselves, so paste only the FAQ part if you use one.", ""])


def main():
    a = sys.argv[1:]
    if not a:
        sys.exit(__doc__)
    spec = load(a[0])
    out = a[a.index("--out") + 1] if "--out" in a else "article"
    probs = validate(spec)
    if probs:
        sys.exit("spec problems:\n- " + "\n- ".join(probs))
    os.makedirs(out, exist_ok=True)
    for name, text in (("article.html", article_html(spec)), ("article.md", article_md(spec)), ("seo.md", seo_md(spec))):
        with open(os.path.join(out, name), "w", encoding="utf-8") as f:
            f.write(text)
    made = ["article.html", "article.md", "seo.md"]
    if "--no-shots" not in a and render.inspect(os.path.join(out, "article.html"), "() => true", widths=(390,), height=900,
                                                shots={390: os.path.abspath(os.path.join(out, "preview.png"))}) is not None:
        made.append("preview.png")
    words = sum(len(plain(t).split()) for _, t in blocks(spec))
    print("built %s: %s (%d words)" % (out, ", ".join(made), words))


if __name__ == "__main__":
    main()
