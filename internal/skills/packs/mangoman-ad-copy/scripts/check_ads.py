"""Check ad copy before uploading.

Usage: python3 check_ads.py ads.json ads
Google search ads: 3 to 15 headlines of up to 30 characters, 2 to 4
descriptions of up to 90, paths up to 15; no exclamation marks in headlines,
no repeated punctuation, no all-capital words, no phone numbers, no duplicate
headlines, https final URLs; keywords appear in headlines; keyword and
negative conflicts. Meta ads: text within what phones show, a valid button,
no wording that asserts a person's personal attributes. Both: every number
traces to the user's facts, risky claims, placeholders.
Prints PASS, WARN or FAIL; exits 1 on any FAIL.
"""
import os
import re
import sys
import unicodedata

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_ads as B  # noqa: E402
import checks as C  # noqa: E402

PHONE = re.compile(r"\+?\(?\d(?:[\s().-]*\d){9,}")
CAPS = re.compile(r"\b[A-Z]{2,}\b")
_ATTR = r"(?:diabetic|overweight|obese|fat|depressed|anxious|gay|lesbian|christian|muslim|hindu|sikh|jewish|in debt|broke|bankrupt|divorced|single|pregnant|disabled|sick|bald|infertile)"
# "Are you diabetic?", "you're in debt", "other single parents", "fellow Muslims": statements about the reader
ATTRIBUTE = re.compile(r"\b(?:are you|you're|you are)\s+(?:\w+\s+){0,2}%s\b|\b(?:other|fellow)\s+%ss?\b(?!\s*(?:-|estate|origin|variety|brands?))" % (_ATTR, _ATTR), re.I)


def blocks(neg, kw):
    """A phrase negative blocks a keyword when its words appear in it, in order."""
    n, k = neg.lower().split(), kw.lower().split()
    return any(k[i:i + len(n)] == n for i in range(len(k) - len(n) + 1))


def symbols(t):
    """Emoji and decorative symbols, which Google disapproves (trademark signs are allowed)."""
    return sorted({c for c in t if unicodedata.category(c) == "So" and c not in "©®™"})


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
    brand_caps = {w.upper() for w in re.findall(r"\w+", spec["brand"])} | set(spec.get("allowed_caps", []))
    texts = []
    g = spec.get("google")
    if g:
        hard, soft = [], []
        for ag in g["ad_groups"]:
            n, hs, ds = ag["name"], ag["headlines"], ag["descriptions"]
            lo, hi = B.GOOGLE["headlines"]
            if not lo <= len(hs) <= hi:
                hard.append("%s: %d headlines (3 to 15)" % (n, len(hs)))
            elif len(hs) < 10:
                soft.append("%s: %d headlines; give 10 to 15 so Google can test combinations" % (n, len(hs)))
            lo, hi = B.GOOGLE["descriptions"]
            if not lo <= len(ds) <= hi:
                hard.append("%s: %d descriptions (2 to 4)" % (n, len(ds)))
            # Each over-long line with its text and how much to cut, so all
            # of them can be rewritten in one pass.
            hard += ["%s: headline %r is %d characters, cut %d (30)" % (n, h, B.glen(h), B.glen(h) - B.GOOGLE["headline"])
                     for h in hs if B.glen(h) > B.GOOGLE["headline"]]
            hard += ["%s: description %d %r is %d characters, cut %d (90)" % (n, i, d, B.glen(d), B.glen(d) - B.GOOGLE["description"])
                     for i, d in enumerate(ds, 1) if B.glen(d) > B.GOOGLE["description"]]
            hard += ["%s: %s is %d characters (15)" % (n, k, B.glen(ag[k])) for k in ("path1", "path2") if B.glen(ag.get(k, "")) > B.GOOGLE["path"]]
            hard += ["%s: emoji or symbols are not allowed (%s) in %r" % (n, " ".join(symbols(t)), t) for t in hs + ds if symbols(t)]
            hard += ["%s: no exclamation marks in headlines: %r" % (n, h) for h in hs if "!" in h]
            hard += ["%s: repeated punctuation in %r" % (n, t) for t in hs + ds if re.search(r"([!?.,])\1", t)]
            hard += ["%s: all-capital word %s in %r" % (n, w, t) for t in hs + ds for w in CAPS.findall(t) if w not in brand_caps]
            hard += ["%s: phone numbers are not allowed in ad text (use a call asset): %r" % (n, t) for t in hs + ds if PHONE.search(t)]
            dup = sorted({h for h in hs if [x.lower() for x in hs].count(h.lower()) > 1})
            hard += ["%s: duplicate headline %r" % (n, h) for h in dup]
            if not ag["final_url"].startswith("https://"):
                hard.append("%s: final_url must be an https address" % n)
            kws = [k["text"].lower() for k in ag.get("keywords", [])]
            if kws and sum(1 for h in hs if any(k in h.lower() for k in kws)) < 2:
                soft.append("%s: fewer than 2 headlines contain a keyword; ads match searches better when they do" % n)
            clash = sorted({x for x in ag.get("negatives", []) for k in kws if blocks(x, k)})
            if clash:
                hard.append("%s: negative keywords block your own keywords: %s" % (n, ", ".join(clash)))
            if not kws:
                soft.append("%s: no keywords" % n)
            texts += hs + ds
        rep.check(hard, "Google ads follow the limits and editorial rules", "Google ads")
        if soft:
            rep.add("WARN", "Google ads: " + "; ".join(soft))
    m = spec.get("meta")
    if m:
        soft, hard = [], []
        for ad in m["ads"]:
            for k in ("primary_text", "headline", "description"):
                if len(ad.get(k, "")) > B.META[k]:
                    soft.append("%s: %s is %d characters; phones cut it at about %d" % (ad["name"], k.replace("_", " "), len(ad[k]), B.META[k]))
            for k in ("primary_text", "headline", "description"):
                mm = ATTRIBUTE.search(ad.get(k, ""))
                if mm:
                    hard.append('%s: "%s" asserts a personal attribute, which Meta rejects; speak about the product instead' % (ad["name"], mm.group(0)))
            if not ad["url"].startswith("https://"):
                hard.append("%s: url must be an https address" % ad["name"])
            if not ad.get("image_brief"):
                soft.append("%s: no image brief" % ad["name"])
            texts += [ad[k] for k in ("primary_text", "headline", "description") if ad.get(k)]
        rep.check(hard, "Meta ads follow the policy basics", "Meta ads")
        rep.check(soft, "Meta text fits what phones show", "Meta ads", "WARN")
    pool = C.fact_pool(spec.get("facts", {}))
    rep.check(sorted({u for t in texts for u in C.untraced(t, pool)}), "every number comes from the facts the user gave", "numbers not in facts (ask the user, or remove them)")
    rep.check(C.first_match(C.RISKY, texts), "no risky claims", "claims that need proof and are often disapproved", "WARN")
    rep.check(C.first_match(C.PLACEHOLDER, texts), "no placeholder text", "placeholder text left in")
    for f in ("ads.md",) + (("google_ads.csv",) if g else ()) + (("meta_ads.csv",) if m else ()):
        if not os.path.exists(os.path.join(out, f)):
            rep.add("FAIL", "%s not built" % f)
    rep.finish()


if __name__ == "__main__":
    main()
