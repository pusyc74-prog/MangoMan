"""Check an SEO article before publishing.

Usage: python3 check_article.py article.json article
Checks: the spec is valid (citations point to sources, sources have https
links); every number is in the user's facts or in a sentence that cites a
source; length against the target and not thin; the main keyword in the
title, h1, opening, description, a subheading and the address, without
stuffing; title and description lengths; readable sentences and paragraphs;
internal links; every source used; FAQ answers short; no repeated sentences,
placeholders or risky claims. Prints PASS, WARN or FAIL; exits 1 on any FAIL.
"""
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_article as B  # noqa: E402
import checks as C  # noqa: E402

SENT = re.compile(r"(?<=[.!?])\s+(?=[A-Z0-9\"'(])")


def words(t):
    return re.findall(r"[a-z0-9]+", t.lower())


def has_phrase(text, phrase):
    w, p = words(text), words(phrase)
    return any(w[i:i + len(p)] == p for i in range(len(w) - len(p) + 1))


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
    m, a, kw = spec["meta"], spec["article"], spec["keyword"]
    bl = B.blocks(spec)
    body = [B.plain(t) for _, t in bl]
    text = " ".join(body)
    n = len(text.split())

    pool = C.fact_pool(spec.get("facts", {}))
    bad = []
    for place, t in bl:
        for s in SENT.split(B.LINK.sub(r"\1", t)):
            u = C.untraced(B.CITE.sub("", s), pool)
            if u and not B.CITE.search(s):
                bad.append("%s: %s" % (place, ", ".join(u)))
    rep.check(bad, "every number is from the facts or cites a source", "numbers with no fact and no source (cite a source [n] or remove)")

    target = spec.get("target_words")
    rep.check([] if n >= 300 else ["%d words" % n], "%d words" % n, "too thin for search: at least 300 words")
    if target and abs(n - target) > 0.2 * target:
        rep.add("WARN", "%d words against a target of %d" % (n, target))

    first = " ".join(body)[:700]
    first100 = " ".join(first.split()[:100])
    places = {"title": m["title"], "h1": a["h1"], "first 100 words": first100, "description": m["description"],
              "a subheading": " | ".join(s["h2"] for s in a["sections"]), "address": m["slug"].replace("-", " ")}
    miss = [k for k, v in places.items() if not has_phrase(v, kw)]
    if "title" in miss and "h1" in miss:
        rep.add("FAIL", 'the keyword "%s" is in neither the title nor the h1' % kw)
    else:
        rep.check(miss, 'keyword "%s" in title, h1, opening, description, a subheading and address' % kw, 'keyword "%s" missing from' % kw, "WARN", ", ")
    hits = sum(1 for i in range(len(words(text))) if words(text)[i:i + len(words(kw))] == words(kw))
    density = hits * len(words(kw)) / max(1, n)
    rep.check([] if density <= 0.025 else ["%.1f%%" % (density * 100)], "keyword used naturally (%.1f%% of words)" % (density * 100),
              "keyword stuffing", "WARN")

    tl, dl = len(m["title"]), len(m["description"])
    rep.check([] if 30 <= tl <= 60 else ["%d characters" % tl], "title fits Google results (%d)" % tl, "title: aim for 30 to 60 characters", "WARN")
    rep.check([] if 120 <= dl <= 160 else ["%d characters" % dl], "description fits Google results (%d)" % dl, "description: aim for 120 to 160 characters", "WARN")
    if len(m["slug"]) > 75:
        rep.add("WARN", "address is %d characters; keep it short" % len(m["slug"]))

    sents = [s for t in body for s in SENT.split(t) if s.strip()]
    avg = sum(len(s.split()) for s in sents) / max(1, len(sents))
    longs = sum(1 for s in sents if len(s.split()) > 30)
    rep.check([] if avg <= 21 and longs <= len(sents) * 0.1 else ["average %.0f words, %d over 30" % (avg, longs)],
              "sentences are easy to read (average %.0f words)" % avg, "sentences are long", "WARN")
    long_p = [p for p, t in bl if len(B.plain(t).split()) > 110]
    rep.check(long_p, "paragraphs are short enough for phones", "paragraphs over 110 words", "WARN", ", ")
    if len(a["sections"]) < 3:
        rep.add("WARN", "only %d sections; break the article into 3 or more" % len(a["sections"]))
    dup = sorted({s for s in sents if len(s.split()) > 5 and sents.count(s) > 1})
    rep.check(dup, "no repeated sentences", "repeated sentences", "WARN")

    links = [l for _, t in bl for l in B.LINK.findall(t)]
    host = re.sub(r"^https?://", "", m.get("url", "")).split("/")[0]
    internal = [u for _, u in links if u.startswith("/") or (host and host in u)]
    rep.check([] if len(internal) >= 2 else ["%d" % len(internal)], "%d links to your own pages" % len(internal),
              "fewer than 2 links to your own pages (helps readers and search engines find related pages)", "WARN")
    cited = {c for _, t in bl for c in B.CITE.findall(t)}
    unused = [str(x["id"]) for x in spec.get("sources", []) if str(x["id"]) not in cited]
    rep.check(unused, "every source is cited", "sources never cited", "WARN", ", ")
    faq = a.get("faq", [])
    if faq:
        longa = [f["q"] for f in faq if len(B.plain(f["a"]).split()) > 60]
        rep.check(longa, "FAQ answers are short (fit a search snippet)", "FAQ answers over 60 words", "WARN")
    texts = [B.plain(t) for _, t in bl] + B.headings(spec) + [m["title"], m["description"]]
    rep.check(C.first_match(C.PLACEHOLDER, texts), "no placeholder text", "placeholder text left in")
    rep.check(C.first_match(C.RISKY, texts), "no risky claims", "claims that need proof", "WARN")
    for f in ("article.html", "article.md"):
        if not os.path.exists(os.path.join(out, f)):
            rep.add("FAIL", "%s not built" % f)
    rep.finish()


if __name__ == "__main__":
    main()
