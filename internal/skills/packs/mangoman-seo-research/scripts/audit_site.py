"""Read a website the way a search engine does and list what to fix.

Usage: python3 audit_site.py <site> [<page> ...] [--pages 10] [--out seo]
<site> is the home page (a link, or a folder with a saved site). Pages not
named are taken from the sitemap, else from the home page's links, up to
--pages in all. Writes seo-audit.json (robots.txt, the sitemap, each page's
title, description, headings, canonical, noindex, structured data, images
without alt text, words and links, and the findings in priority order, each
with where, why and the fix) and seo-audit.md, the same for reading.

Checks follow claude-seo (github.com/AgriciDaniel/claude-seo, MIT). Only
public pages, a pause between pages, never logged in. Real speed cannot be
measured by reading a page: use PageSpeed Insights for that.
"""
import datetime as dt
import json
import os
import pathlib
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import urllib.robotparser

OUT = "seo-audit"
AGENT = "Mozilla/5.0 (compatible; MangoMan site audit)"
PAUSE = 1  # seconds between pages
MAX_PAGES = 30
# Crawlers that decide whether a page can be found in search and quoted in AI
# answers. Training crawlers (GPTBot, ClaudeBot, Google-Extended, CCBot) are
# left out: blocking them does not hide a site from search or AI answers.
SEARCH_BOTS = ("Googlebot", "Bingbot", "OAI-SearchBot", "Claude-SearchBot", "PerplexityBot")
ORDER = {"high": 0, "medium": 1, "low": 2}


def now():
    return dt.datetime.now(dt.timezone.utc)


def root_of(site):
    """The site's root: scheme and host for a link, the folder for a saved site."""
    if os.path.isdir(site):
        return pathlib.Path(os.path.abspath(site)).as_uri() + "/"
    if not site.startswith(("http://", "https://")):
        site = "https://" + site
    p = urllib.parse.urlsplit(site)
    return "%s://%s/" % (p.scheme, p.netloc)


def fetch(url):
    """(status, final url, headers, body, seconds, why). A saved file reads as 200."""
    import ssl
    try:
        import certifi
        ctx = ssl.create_default_context(cafile=certifi.where())
    except ImportError:
        ctx = None
    start = time.monotonic()
    try:
        req = urllib.request.Request(url, headers={"User-Agent": AGENT})
        with urllib.request.urlopen(req, timeout=20, context=ctx if url.startswith("https") else None) as r:
            body = r.read(5 << 20)
            status = getattr(r, "status", None) or 200
            return status, r.geturl(), r.headers, body, time.monotonic() - start, ""
    except urllib.error.HTTPError as e:
        return e.code, url, e.headers, b"", time.monotonic() - start, "the page answered %d" % e.code
    except (urllib.error.URLError, OSError, ValueError) as e:
        return None, url, {}, b"", time.monotonic() - start, "did not open (%s)" % str(getattr(e, "reason", e))[:120]


def read_robots(root):
    status, _, _, body, _, why = fetch(root + "robots.txt")
    text = body.decode("utf-8", "replace") if status == 200 else ""
    rp = urllib.robotparser.RobotFileParser()
    rp.parse(text.splitlines())
    return {"found": status == 200, "why": why, "sitemaps": re.findall(r"(?im)^\s*sitemap:\s*(\S+)", text),
            "blocked": {b: not rp.can_fetch(b, root) for b in SEARCH_BOTS}, "text": text[:5000]}


def read_sitemap(root, listed):
    """Page links from the sitemap (following an index one level), at most 500."""
    urls, read, seen = [], [], set()
    todo = list(listed) or [root + "sitemap.xml"]
    while todo and len(read) < 5:
        u = todo.pop(0)
        if u in seen:
            continue
        seen.add(u)
        status, _, _, body, _, _ = fetch(u)
        if status != 200:
            continue
        read.append(u)
        locs = re.findall(rb"<loc>\s*([^<\s]+)\s*</loc>", body)
        locs = [x.decode("utf-8", "replace").replace("&amp;", "&") for x in locs]
        if b"<sitemapindex" in body:
            todo += locs
        else:
            urls += locs
    return {"found": bool(read), "read": read, "urls": urls[:500], "count": len(urls)}


def page_facts(url, status, final, headers, body, secs, why, root):
    p = {"url": url, "ok": status == 200 and bool(body), "status": status, "why": why,
         "final_url": final if final != url else "", "seconds": round(secs, 2), "kb": round(len(body) / 1024)}
    if not p["ok"]:
        p["why"] = why or "the page was empty"
        return p
    import lxml.html
    try:
        doc = lxml.html.fromstring(body)
    except Exception as e:
        p.update(ok=False, why="the page could not be read as HTML (%s)" % str(e)[:80])
        return p
    meta = lambda n: next((m.get("content", "").strip() for m in doc.iter("meta") if (m.get("name") or "").lower() == n), None)
    link = lambda rel: next((x.get("href", "").strip() for x in doc.iter("link") if rel in (x.get("rel") or "").lower().split()), None)
    schema, broken = [], 0
    for s in doc.xpath('//script[@type="application/ld+json"]'):
        try:
            data = json.loads(s.text or "")
        except ValueError:
            broken += 1
            continue
        for d in data if isinstance(data, list) else data.get("@graph", [data]) if isinstance(data, dict) else []:
            t = d.get("@type") if isinstance(d, dict) else None
            schema += t if isinstance(t, list) else [t] if t else []
    for bad in doc.xpath("//script|//style|//noscript|//template"):
        bad.drop_tree()
    body_el = doc.find("body")
    words = len(re.findall(r"\w+", body_el.text_content() if body_el is not None else ""))
    host = urllib.parse.urlsplit(root).netloc
    hrefs = [urllib.parse.urljoin(final, a.get("href", "")) for a in doc.iter("a") if a.get("href")]
    internal = sorted({h.split("#")[0] for h in hrefs if urllib.parse.urlsplit(h).netloc == host and h.startswith(root)})
    title = doc.findtext(".//title")
    p.update(title=(title or "").strip() or None, description=meta("description"), robots=(meta("robots") or "").lower(),
             x_robots=(headers.get("X-Robots-Tag") or "").lower(), canonical=link("canonical"), viewport=meta("viewport") is not None,
             lang=doc.get("lang") or "", h1=[h.text_content().strip() for h in doc.iter("h1")],
             h2=[h.text_content().strip() for h in doc.iter("h2")][:20], schema=sorted(set(map(str, schema))), schema_broken=broken,
             images=len(list(doc.iter("img"))), no_alt=sum(1 for i in doc.iter("img") if i.get("alt") is None),
             og_image=any((m.get("property") or "") == "og:image" for m in doc.iter("meta")), words=words, internal_links=internal[:200])
    return p


def findings(a):
    """What to fix, from what was read: priority, what, where, why, fix."""
    out = []
    add = lambda pr, what, where, why, fix: out.append({"priority": pr, "what": what, "where": where, "why": why, "fix": fix})
    root, robots, sitemap = a["site"], a["robots"], a["sitemap"]
    if not robots["found"]:
        add("low", "No robots.txt", root + "robots.txt", "Crawlers find the sitemap and the rules there.", "Add a robots.txt that names the sitemap.")
    for b in [b for b, blocked in robots["blocked"].items() if blocked]:
        search = b in ("Googlebot", "Bingbot")
        add("high" if search else "medium", "robots.txt blocks %s from the site" % b, root + "robots.txt",
            "The site cannot show in %s." % ("search results" if search else "the AI answers that crawler feeds"),
            "Remove the Disallow rule for %s, unless hiding the site is intended." % b)
    if not sitemap["found"]:
        add("medium", "No sitemap found", root + "sitemap.xml", "A sitemap tells search engines which pages exist.",
            "Publish sitemap.xml listing every page that should rank, and name it in robots.txt.")
    pages = [p for p in a["pages"] if p["ok"]]
    for p in a["pages"]:
        if not p["ok"]:
            add("high", "Page did not open: %s" % p["why"], p["url"], "Search engines and visitors cannot see it.", "Fix the page or remove links to it.")
    if pages and not pages[0]["url"].startswith("https") and not (pages[0]["final_url"] or "").startswith("https"):
        if pages[0]["url"].startswith("http"):
            add("high", "The site is not on HTTPS", pages[0]["url"], "Browsers mark it not secure and search engines prefer HTTPS.",
                "Turn on HTTPS and redirect every http address to it.")
    titles = {}
    for p in pages:
        u = p["url"]
        titles.setdefault((p["title"] or "").lower(), []).append(u)
        if "noindex" in p["robots"] or "noindex" in p["x_robots"]:
            add("high", "The page asks not to be indexed (noindex)", u, "It will not show in search.", "Remove noindex if this page should rank.")
        if p["canonical"] and urllib.parse.urljoin(u, p["canonical"]).rstrip("/") not in (u.rstrip("/"), (p["final_url"] or u).rstrip("/")):
            add("medium", "Canonical points to another page: %s" % p["canonical"], u, "Search engines rank the other page instead.",
                "Point the canonical to the page itself, unless it is a copy.")
        if not p["viewport"]:
            add("high", "No mobile viewport tag", u, "Phones show the desktop layout; Google ranks the mobile version.",
                'Add <meta name="viewport" content="width=device-width, initial-scale=1">.')
        if not p["title"]:
            add("high", "No title", u, "The title is the blue link in search results.", "Write a title that says what the page is and for whom.")
        elif not 15 <= len(p["title"]) <= 60:
            add("low", "Title is %d characters" % len(p["title"]), u, "Search results cut titles at about 60 characters; very short ones say little.",
                "Aim for 30 to 60 characters, the main phrase first.")
        if not p["description"]:
            add("medium", "No meta description", u, "Search engines then pick some text from the page as the snippet.",
                "Write 120 to 160 characters that give a reason to click.")
        if not p["h1"]:
            add("medium", "No H1 heading", u, "The H1 tells readers and search engines what the page is about.", "Add one H1 that matches the page's job.")
        elif len(p["h1"]) > 1:
            add("low", "%d H1 headings" % len(p["h1"]), u, "One clear main heading is easier to understand.", "Keep one H1; make the others H2.")
        if p["schema_broken"]:
            add("medium", "%d structured data block(s) are not valid JSON" % p["schema_broken"], u, "Search engines ignore broken structured data.",
                "Fix the JSON-LD; test it with Google's Rich Results Test.")
        if p["no_alt"]:
            add("low", "%d of %d images have no alt text" % (p["no_alt"], p["images"]), u, "Alt text describes images to search and to people who cannot see them.",
                "Add short alt text that says what each image shows.")
        if p["kb"] > 2048:
            add("medium", "The page's HTML is %d KB" % p["kb"], u, "Heavy pages load slowly on phones.", "Move scripts and inline data out of the HTML.")
        if p["words"] < 250 and p is not pages[0]:
            add("low", "Thin page: %d words" % p["words"], u, "Short pages rarely answer a search fully.", "Add what a customer needs to decide, or merge it with a related page.")
        if not p["lang"]:
            add("low", "No language on the page", u, "The lang attribute tells search engines which searchers it suits.", 'Add lang to the html tag, for example lang="en".')
    if pages and not pages[0]["schema"]:
        add("medium", "No structured data on the home page", pages[0]["url"], "Structured data (Organization, LocalBusiness) helps search engines and AI answers know the business.",
            "Add JSON-LD for the business: name, logo, address, phone, opening hours, social profiles.")
    if pages and not pages[0]["og_image"]:
        add("low", "No share image (og:image) on the home page", pages[0]["url"], "Links shared on social media show no picture.", "Add an og:image of 1200 by 630 pixels.")
    for title, urls in titles.items():
        if title and len(urls) > 1:
            add("medium", "%d pages share one title" % len(urls), ", ".join(urls), "Search engines cannot tell them apart.", "Give each page its own title.")
    return sorted(out, key=lambda f: ORDER[f["priority"]])


def markdown(a):
    md = ["# Site audit: %s" % a["site"], "", "Read %s. Pages read: %d of %d. Sitemap: %s. robots.txt: %s." % (
        a["read_at"][:10], sum(p["ok"] for p in a["pages"]), len(a["pages"]),
        "%d pages listed" % a["sitemap"]["count"] if a["sitemap"]["found"] else "not found", "found" if a["robots"]["found"] else "not found"), "",
        "Real speed is not measured here: run PageSpeed Insights (pagespeed.web.dev) for that.", "", "## Findings", ""]
    md += ["- **%s** %s. Where: %s. Why: %s Fix: %s" % (f["priority"], f["what"], f["where"], f["why"], f["fix"]) for f in a["findings"]] or ["- Nothing to fix was found in the pages read."]
    md += ["", "## Pages", ""]
    for p in a["pages"]:
        md += ["- %s: %s" % (p["url"], "could not read: " + p["why"] if not p["ok"] else "title \"%s\", %d words, H1 %s, structured data %s" % (
            p["title"] or "", p["words"], " / ".join(p["h1"]) or "none", ", ".join(p["schema"]) or "none"))]
    return "\n".join(md) + "\n"


def main():
    a = sys.argv[1:]
    if not a or a[0].startswith("--"):
        sys.exit(__doc__)
    try:
        out = a[a.index("--out") + 1] if "--out" in a else "seo"
        n = int(a[a.index("--pages") + 1]) if "--pages" in a else 10
    except (IndexError, ValueError):
        sys.exit("--out needs a folder and --pages a number from 1 to %d" % MAX_PAGES)
    if not 1 <= n <= MAX_PAGES:
        sys.exit("--pages must be a number from 1 to %d" % MAX_PAGES)
    skip = {i + 1 for i, x in enumerate(a) if x in ("--out", "--pages")}
    named = [x for i, x in enumerate(a) if not x.startswith("--") and i not in skip]
    root = root_of(named[0])
    home = named[0] if named[0].startswith("http") else root if os.path.isdir(named[0]) else "https://" + named[0]
    if home == root and root.startswith("file:"):
        home = root + "index.html"
    got = fetch(home)
    if got[1].startswith("http") and root_of(got[1]) != root:  # example.com that moves to www.example.com
        root = root_of(got[1])
    pages = [page_facts(home, *got, root)]
    robots = read_robots(root)
    sitemap = read_sitemap(root, robots["sitemaps"])
    todo = [urllib.parse.urljoin(root, x) for x in named[1:]] or sitemap["urls"] or pages[0].get("internal_links", [])
    seen = {home.rstrip("/")}
    for u in todo:
        if len(pages) >= n:
            break
        if u.rstrip("/") in seen or not u.startswith(root):
            continue
        seen.add(u.rstrip("/"))
        time.sleep(PAUSE)
        pages.append(page_facts(u, *fetch(u), root))
    audit = {"site": root, "read_at": now().isoformat(timespec="seconds"), "robots": robots, "sitemap": sitemap, "pages": pages}
    audit["findings"] = findings(audit)
    os.makedirs(out, exist_ok=True)
    with open(os.path.join(out, OUT + ".json"), "w", encoding="utf-8") as f:
        json.dump(audit, f, ensure_ascii=False, indent=1)
    with open(os.path.join(out, OUT + ".md"), "w", encoding="utf-8") as f:
        f.write(markdown(audit))
    print("wrote %s.json and %s.md: %d pages read, %d findings (%d high)" % (
        OUT, OUT, sum(p["ok"] for p in pages), len(audit["findings"]), sum(f["priority"] == "high" for f in audit["findings"])))


if __name__ == "__main__":
    main()
