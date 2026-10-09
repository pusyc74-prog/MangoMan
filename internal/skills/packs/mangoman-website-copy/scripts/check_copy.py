"""Check website copy before it goes to the user.

Usage: python3 check_copy.py site.json site
Checks: the spec is valid; the files were built; every number is in the user's
facts; no five-word run is copied from a competitor claim; no placeholders,
clichés or risky claims; the voice rules hold; each page has a keyword in its
title or h1, a title and description of the right length, and a button; titles
and descriptions are not repeated; internal links resolve and every page is
linked; no sentence repeats across pages; no page is thin; sentences are
short (English). Prints PASS, WARN or FAIL; exits 1 on any FAIL.
"""
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_copy as B  # noqa: E402
import checks as C  # noqa: E402

SENT = re.compile(r"(?<=[.!?])\s+(?=[A-Z0-9\"'(])")
CLICHE = re.compile(r"\b(world[- ]class|state[- ]of[- ]the[- ]art|cutting[- ]edge|one[- ]stop|second to none|passion for|take(?:s)? it to the next level|unlock|seamless(?:ly)?|leverage|synergy|game[- ]chang\w+|innovative solutions|your trusted partner)\b", re.I)
COPY_RUN = 5


def words(t):
    """Words in any script (Indic vowel signs are not \\w, so they are added)."""
    return re.findall(r"[\wऀ-෿]+", t.lower())


def plain(t):
    return re.sub(r"[*_]|\[([^\]]+)\]\([^)]+\)", lambda m: m.group(1) or "", t)


def has_phrase(text, phrase):
    w, p = words(text), words(phrase)
    return bool(p) and any(w[i:i + len(p)] == p for i in range(len(w) - len(p) + 1))


def copied(text, claim):
    """The first COPY_RUN-word run of a competitor claim found in text, or ''."""
    c, w = words(claim), " ".join(words(text))
    for i in range(len(c) - COPY_RUN + 1):
        run = " ".join(c[i:i + COPY_RUN])
        if run in w:
            return run
    return ""


def page_text(pg):
    return [plain(t) for t in B.texts(pg["sections"])]


def sentences(pg):
    return [s.strip() for t in page_text(pg) for s in SENT.split(t) if len(words(s)) >= 5]


def main():
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    src, out = sys.argv[1], sys.argv[2]
    spec = B.load(src)
    rep = C.Report()
    probs = B.validate(spec)
    rep.check(probs, "spec is valid", "spec problems")
    if probs:
        rep.finish()
    pages = spec["pages"]
    slugs = [pg["slug"] for pg in pages]
    english = str(spec.get("language", "English")).lower().startswith("en")

    need = ["pages.csv", "sitemap.md", "pages.json"] + [os.path.join("copy", s + ".md") for s in slugs]
    rep.check([f for f in need if not os.path.exists(os.path.join(out, f))], "all output files exist", "missing files")

    allt = [t for pg in pages for t in page_text(pg)] + [t for pg in pages for t in (pg["meta"]["title"], pg["meta"]["description"])]
    pool = C.fact_pool(spec.get("facts", {}))
    bad = sorted(set(n for t in allt for n in C.untraced(t, pool)))
    # Numbers the user gave in facts.md but that never reached "facts" in
    # site.json. Said plainly: measured 9 Oct, a model edited the built pages
    # 7 times instead (they are rebuilt from site.json) and ran out of time.
    md = os.path.join(os.path.dirname(os.path.abspath(src)), "facts.md")
    given = []
    if bad and os.path.exists(md):
        with open(md, encoding="utf-8") as f:
            md_pool = C.fact_pool(f.read())
        given = [n for n in bad if not C.untraced(n, md_pool)]
    if given:
        rep.add("FAIL", "numbers in facts.md but missing from \"facts\" in site.json: %s. Copy those fact lines "
                "into \"facts\" in site.json and build again (the pages in %s are rebuilt from site.json: "
                "do not edit them)" % ("; ".join(given), out))
    rep.check([n for n in bad if n not in given], "every number is in the user's facts",
              "numbers the user never gave: take them out of the copy in site.json")

    claims = [(c.get("name", "a competitor"), cl) for c in spec.get("competitors", []) for cl in c.get("claims", [])]
    hits = ["%s (%s)" % (run, n) for n, cl in claims for t in allt for run in [copied(t, cl)] if run]
    rep.check(hits, "nothing copied from competitor claims", "copied from competitors")

    rep.check(C.first_match(C.PLACEHOLDER, allt), "no placeholders", "placeholders left")
    rep.check(C.first_match(C.RISKY, allt), "no risky claims", "risky claims (only keep them if the facts prove them)", level="WARN")
    rep.check(C.first_match(CLICHE, allt), "no clichés", "clichés", level="WARN")

    voice = spec.get("voice", {})
    joined = " ".join(allt)
    rep.check([w for w in voice.get("avoid", []) if has_phrase(joined, w)], "no avoided words", "words the voice says to avoid")
    rep.check([w for w in voice.get("use", []) if not has_phrase(joined, w)], "voice words used", "voice words never used", level="WARN")

    if english:
        sents = [s for pg in pages for s in sentences(pg)]
        avg = sum(len(words(s)) for s in sents) / max(1, len(sents))
        rep.add("PASS" if avg <= 20 else "WARN", "average sentence is %.1f words%s" % (avg, "" if avg <= 20 else " (aim for 20 or fewer)"))

    seo = []
    for pg in pages:
        t, h1 = pg["meta"]["title"], pg["sections"][0]["headline"]
        k = pg["keyword"]
        if not (has_phrase(t, k) or has_phrase(h1, k)):
            seo.append("%s: keyword '%s' in neither title nor h1" % (pg["slug"], k))
    rep.check(seo, "each page has its keyword in the title or h1", "keyword missing")
    soft = []
    for pg in pages:
        t, d, k = pg["meta"]["title"], pg["meta"]["description"], pg["keyword"]
        first = " ".join(words(" ".join(page_text(pg)))[:100])
        if not 30 <= len(t) <= 60:
            soft.append("%s: title is %d characters (30 to 60)" % (pg["slug"], len(t)))
        if not 70 <= len(d) <= 160:
            soft.append("%s: description is %d characters (70 to 160)" % (pg["slug"], len(d)))
        if not has_phrase(d, k):
            soft.append("%s: keyword not in the description" % pg["slug"])
        if not has_phrase(first, k):
            soft.append("%s: keyword not in the first 100 words" % pg["slug"])
    rep.check(soft, "titles, descriptions and keyword placement fit", "search fit", level="WARN")
    dup = [k for k in ("title", "description") if len({pg["meta"][k].lower() for pg in pages}) < len(pages)]
    rep.check(["two pages share a %s" % k for k in dup], "titles and descriptions are unique", "repeated meta")

    nocta = [pg["slug"] for pg in pages if not any(isinstance(s.get("cta"), dict) for s in pg["sections"])]
    rep.check(["%s has no button in its sections" % s for s in nocta], "every page has a button", "pages without a button")
    labels = {pg["cta"]["label"].lower() for pg in pages}
    rep.check(["%d different button labels: %s" % (len(labels), ", ".join(sorted(labels)))] if len(labels) > 2 else [],
              "buttons are consistent", "too many button labels", level="WARN")

    linked = {B.target(h, slugs) for pg in pages for h in B.internal_links(pg)}
    rep.check([s for s in slugs[1:] if s not in linked], "every page is linked from another page", "pages nobody links to", level="WARN")

    seen, repeats = {}, []
    for pg in pages:
        for s in sentences(pg):
            key = " ".join(words(s))
            if seen.setdefault(key, pg["slug"]) != pg["slug"]:
                repeats.append(s[:60])
    rep.check(repeats, "no sentence repeated across pages", "sentences repeated across pages", level="WARN")

    thin = ["%s (%d words)" % (pg["slug"], n) for pg in pages for n in [len(words(" ".join(page_text(pg))))] if n < 100]
    rep.check(thin, "no thin pages", "thin pages", level="WARN")
    rep.finish()


if __name__ == "__main__":
    main()
