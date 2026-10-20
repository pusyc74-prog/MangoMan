"""Check reel scripts before shooting.

Usage: python3 check_reels.py reels.json reels
Each reel answers one item from the audience research; has three different
hook options that can be said in about 3 seconds; has value and a solution;
ends with one call to action that serves the reel's goal; fits its target
length when spoken; puts a search keyword in the caption; has 3 to 8
one-word hashtags. Every number traces to the user's facts; risky claims and
placeholder text are flagged. Prints PASS, WARN or FAIL; exits 1 on any FAIL.
"""
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_reels as B  # noqa: E402
import checks as C  # noqa: E402

# A second call to action before the end splits the viewer's attention.
EARLY_CTA = re.compile(r"\b(follow (?:us|me|for)|(?:save|share) this (?:reel|video|post)|save (?:this|it) for later|send this to|comment below|link in (?:the )?bio|dm (?:us|me))\b", re.I)


def norm(s):
    return " ".join(s.lower().split())


def main():
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    src, out = sys.argv[1], sys.argv[2]
    spec = C.load_json(src)
    rep = C.Report()
    probs = B.validate(spec)
    rep.check(probs, "spec is valid", "spec problems")
    if probs:
        rep.finish()
    aud = {k: {norm(x) for x in v} for k, v in spec["audience"].items()}
    angle, hooks, body, cta, length, seo, tags, soft, texts = [], [], [], [], [], [], [], [], []
    used = {}
    for r in spec["reels"]:
        n = r["id"]
        a = r["angle"]
        if norm(a["text"]) not in aud.get(a["from"], set()):
            angle.append('%s: "%s" is not in the audience %s; use an item from the research, word for word' % (n, a["text"], a["from"]))
        if norm(a["text"]) in used:
            soft.append("%s answers the same audience item as %s" % (n, used[norm(a["text"])]))
        used.setdefault(norm(a["text"]), n)
        hs = r["hooks"]
        if len(hs) < 3:
            hooks.append("%s: %d hook options; write 3 so the best one can be picked" % (n, len(hs)))
        if len({norm(h["say"]) for h in hs}) < len(hs):
            hooks.append("%s: two hook options say the same thing" % n)
        hooks += ['%s: hook "%s" takes about %d s to say; keep it within 3 s (8 words or fewer)' % (n, h["say"], B.seconds(h["say"]))
                  for h in hs if B.seconds(h["say"]) > 3]
        soft += ['%s: on-screen hook "%s" has %d words; 8 or fewer read in a glance' % (n, h["text"], B.words(h["text"]))
                 for h in hs if B.words(h.get("text")) > 8]
        parts = {b["part"] for b in r["beats"]}
        body += ["%s: no %s beat" % (n, p) for p in B.PARTS if p not in parts]
        if r["cta"]["action"] not in B.GOALS[r["goal"]]:
            cta.append("%s: the goal is %s but the call to action is %s (use %s)" % (n, r["goal"], r["cta"]["action"], " or ".join(B.GOALS[r["goal"]])))
        cta += ['%s: a call to action before the end ("%s"); keep one, at the end' % (n, m.group(0))
                for s in hs + r["beats"] for k in ("say", "text") for m in [EARLY_CTA.search(s.get(k, ""))] if m]
        spoken = B.timeline(r)[-1][1]
        if spoken > r["length"] + max(3, r["length"] // 5):
            length.append("%s: about %d s when spoken, target %d s; cut words" % (n, spoken, r["length"]))
        elif spoken < r["length"] // 2:
            soft.append("%s: about %d s when spoken, target %d s; it may feel thin" % (n, spoken, r["length"]))
        first = r["caption"].split("\n", 1)[0]
        if len(first) > 125:
            soft.append("%s: the caption's first line is %d characters; Instagram shows about 125 before 'more'" % (n, len(first)))
        kws = [k for k in r.get("keywords", []) if norm(k) in norm(r["caption"])]
        if not r.get("keywords"):
            seo.append("%s: no search keywords" % n)
        elif not kws:
            seo.append("%s: no keyword appears in the caption (%s)" % (n, ", ".join(r["keywords"])))
        said = " ".join(s.get(k, "") for s in hs[:1] + r["beats"] for k in ("say", "text"))
        if r.get("keywords") and not any(norm(k) in norm(said) for k in r["keywords"]):
            soft.append("%s: no keyword is said or shown on screen; platforms read both for search" % n)
        if re.search(r"(?<![\w&])#\w", r["caption"]):
            tags.append("%s: hashtags are in the caption; list them in hashtags only (the build adds them)" % n)
        ht = r.get("hashtags", [])
        tags += ["%s: hashtag %r must be one word" % (n, h) for h in ht if re.search(r"\s", h.strip())]
        if not 3 <= len(ht) <= 8:
            soft.append("%s: %d hashtags; use 3 to 8 relevant ones" % (n, len(ht)))
        texts += [r["title"], r["caption"], r.get("cover", ""), r["cta"]["say"], r["cta"].get("text", "")]
        texts += [s.get(k, "") for s in hs + r["beats"] for k in ("say", "text")]
    rep.check(angle, "every reel answers an item from the audience research", "reels not tied to the research")
    rep.check(hooks, "three different hooks per reel, each said within 3 seconds", "hooks")
    rep.check(body, "every reel gives value and a solution", "missing parts")
    rep.check(cta, "one call to action per reel, at the end, serving its goal", "calls to action")
    rep.check(length, "every reel fits its target length", "too long")
    rep.check(seo, "search keywords are in every caption", "search keywords")
    rep.check(tags, "hashtags are single words, listed once", "hashtags")
    rep.check(soft, "hooks, captions and hashtags read well", "to improve", "WARN")
    pool = C.fact_pool(spec.get("facts", {}))
    rep.check(sorted({u for t in texts for u in C.untraced(t, pool)}), "every number comes from the facts the user gave",
              "numbers not in facts (ask the user, or remove them)")
    rep.check(C.first_match(C.RISKY, texts), "no risky claims", "claims that need proof", "WARN")
    rep.check(C.first_match(C.PLACEHOLDER, texts), "no placeholder text", "placeholder text left in")
    for f in ("reels.md", "reels.csv"):
        if not os.path.exists(os.path.join(out, f)):
            rep.add("FAIL", "%s not built" % f)
    rep.finish()


if __name__ == "__main__":
    main()
