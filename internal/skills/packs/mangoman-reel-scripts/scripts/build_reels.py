"""Build ready-to-shoot short video scripts (Instagram Reels, YouTube Shorts,
Facebook Reels, TikTok) from reels.json.

Usage: python3 build_reels.py reels.json --out reels
Writes reels.md (every script with three hook options, a timed shot table,
caption, keywords, hashtags, cover text and sound) and reels.csv (one row per
reel and platform, for a posting sheet or scheduler).
"""
import csv
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import checks as C  # noqa: E402

GOALS = {  # what the reel is for -> the actions that serve it
    "followers": ("follow",),
    "comments": ("comment",),
    "saves": ("save",),
    "shares": ("share",),
    "leads": ("dm", "link", "call", "whatsapp", "book", "order", "visit"),
}
ANGLES = ("problems", "frustrations", "outcomes", "questions", "mistakes", "objections", "beliefs", "emotions")
PLATFORMS = ("instagram", "youtube", "facebook", "tiktok")
PARTS = ("value", "solution")
WORDS_PER_SECOND = 2.5  # about 150 words a minute, a normal speaking pace


def words(text):
    return len((text or "").split())


def seconds(text):
    """Spoken time in whole seconds, at least 1 for any spoken line."""
    n = words(text)
    return max(1, round(n / WORDS_PER_SECOND)) if n else 0


def shot_ok(s):
    return isinstance(s, dict) and all(isinstance(s.get(k, ""), str) for k in ("say", "text", "show")) and s.get("show")


def validate(spec):
    if not isinstance(spec, dict):
        return ["reels.json must be an object with brand, facts, audience and reels"]
    p = []
    if not spec.get("brand"):
        p.append("brand is required")
    aud = spec.get("audience")
    if not isinstance(aud, dict) or not any(aud.get(k) for k in ANGLES):
        p.append("audience is required: the research map with lists for %s" % ", ".join(ANGLES))
    else:
        for k, v in aud.items():
            if k not in ANGLES + ("phrases",) or not (isinstance(v, list) and all(isinstance(x, str) and x for x in v)):
                p.append("audience: %s must be one of %s, phrases, each a list of text" % (k, ", ".join(ANGLES)))
    reels = spec.get("reels")
    if not isinstance(reels, list) or not reels:
        p.append("reels must be a list with at least one reel")
        return p
    ids = set()
    for i, r in enumerate(reels, 1):
        tag = "reel %d (%s)" % (i, r.get("id", "?") if isinstance(r, dict) else "?")
        if not isinstance(r, dict):
            p.append("%s must be an object" % tag)
            continue
        for k in ("id", "title", "goal", "angle", "length", "hooks", "beats", "cta", "caption", "platforms"):
            if not r.get(k):
                p.append("%s: needs %s" % (tag, k))
        for k in ("id", "title", "goal", "caption", "cover", "sound", "date", "time"):
            if not isinstance(r.get(k, ""), str):
                p.append("%s: %s must be text" % (tag, k))
        if isinstance(r.get("id"), str) and r["id"] in ids:
            p.append("%s: id is used twice" % tag)
        ids.add(str(r.get("id")))
        if isinstance(r.get("goal"), str) and r["goal"] and r["goal"] not in GOALS:
            p.append("%s: goal must be one of %s (one per reel)" % (tag, ", ".join(GOALS)))
        a = r.get("angle")
        if a and not (isinstance(a, dict) and a.get("from") in ANGLES and isinstance(a.get("text"), str) and a["text"]):
            p.append('%s: angle is {"from": one of %s, "text": the item from the audience list}' % (tag, ", ".join(ANGLES)))
        n = r.get("length")
        if n and not (isinstance(n, int) and 7 <= n <= 180):
            p.append("%s: length is the target in whole seconds, 7 to 180" % tag)
        if r.get("hooks") and not (isinstance(r["hooks"], list) and all(shot_ok(h) and h.get("say") for h in r["hooks"])):
            p.append('%s: each hook is {"say", "text", "show"} with say and show filled' % tag)
        b = r.get("beats")
        if b and not (isinstance(b, list) and all(shot_ok(x) and x.get("part") in PARTS for x in b)):
            p.append('%s: each beat is {"part": "value" or "solution", "say", "text", "show"} with show filled' % tag)
        c = r.get("cta")
        if c and not (isinstance(c, dict) and c.get("action") in sum(GOALS.values(), ()) and c.get("say") and shot_ok(c)):
            p.append('%s: cta is {"action": one of %s, "say", "text", "show"}' % (tag, ", ".join(sum(GOALS.values(), ()))))
        for k in ("platforms", "keywords", "hashtags"):
            if not (isinstance(r.get(k, []), list) and all(isinstance(x, str) and x.strip("# ") for x in r.get(k, []))):
                p.append("%s: %s must be a list of text" % (tag, k))
        bad = [x for x in r.get("platforms", []) if x not in PLATFORMS] if isinstance(r.get("platforms"), list) else []
        if bad:
            p.append("%s: platforms must be from %s" % (tag, ", ".join(PLATFORMS)))
    return p


def timeline(r):
    """(from, to, part, shot) for hook option 1, the beats and the CTA."""
    out, t = [], 0
    for part, s in [("hook", r["hooks"][0])] + [(b["part"], b) for b in r["beats"]] + [("cta", r["cta"])]:
        d = max(seconds(s.get("say")), 2)  # a shot with no words still needs time on screen
        out.append((t, t + d, part, s))
        t += d
    return out


def cell(s):
    return (s or "").replace("|", "/").replace("\n", " ")


def reel_md(n, r):
    tl = timeline(r)
    total = tl[-1][1]
    when = " ".join(x for x in (r.get("date", ""), r.get("time", "")) if x)
    out = ["## %d. %s" % (n, r["title"]), "",
           "Goal: %s. Platforms: %s. Target length: %d s (spoken: about %d s).%s" % (
               r["goal"], ", ".join(r["platforms"]), r["length"], total, " Post: %s." % when if when else ""),
           "", "Audience %s: \"%s\"" % (r["angle"]["from"].rstrip("s"), r["angle"]["text"]), "",
           "### Hook options (first 3 seconds; the script uses option 1)", ""]
    for i, h in enumerate(r["hooks"], 1):
        out.append("%d. Say: \"%s\" On screen: %s. Show: %s." % (i, h["say"], h.get("text") or "none", h["show"]))
    out += ["", "### Script", "", "| Time | Part | Say | On screen | Show |", "|---|---|---|---|---|"]
    out += ["| %d to %d s | %s | %s | %s | %s |" % (a, b, part, cell(s.get("say")), cell(s.get("text")), cell(s["show"])) for a, b, part, s in tl]
    out += ["", "### Post", ""]
    if r.get("cover"):
        out.append("Cover text: %s" % r["cover"])
    out += ["Caption:", "", "```", r["caption"] + ("\n\n" + " ".join("#" + h.lstrip("#") for h in r["hashtags"]) if r.get("hashtags") else ""), "```", ""]
    if r.get("keywords"):
        out.append("Search keywords (in the caption, on-screen text and spoken lines): %s" % ", ".join(r["keywords"]))
    if r.get("sound"):
        out.append("Sound: %s" % r["sound"])
    return out + [""]


def main():
    a = sys.argv[1:]
    if not a:
        sys.exit(__doc__)
    spec = C.load_json(a[0])
    out = a[a.index("--out") + 1] if "--out" in a else "reels"
    probs = validate(spec)
    if probs:
        sys.exit("spec problems:\n- " + "\n- ".join(probs))
    os.makedirs(out, exist_ok=True)
    md = ["# Reel scripts: %s" % spec["brand"], ""]
    for n, r in enumerate(spec["reels"], 1):
        md += reel_md(n, r)
    with open(os.path.join(out, "reels.md"), "w", encoding="utf-8") as f:
        f.write("\n".join(md))
    rows = []
    for r in spec["reels"]:
        tags = " ".join("#" + h.lstrip("#") for h in r.get("hashtags", []))
        for p in r["platforms"]:
            rows.append({"date": r.get("date", ""), "time": r.get("time", ""), "platform": p, "id": r["id"], "title": r["title"],
                         "goal": r["goal"], "hook": r["hooks"][0]["say"], "cover": r.get("cover", ""),
                         "caption": (r["caption"] + ("\n\n" + tags if tags else "")), "seconds": timeline(r)[-1][1]})
    with open(os.path.join(out, "reels.csv"), "w", newline="", encoding="utf-8-sig") as f:
        w = csv.DictWriter(f, fieldnames=list(rows[0]))
        w.writeheader()
        w.writerows(rows)
    print("built %s: reels.md, reels.csv (%d reels)" % (out, len(spec["reels"])))


if __name__ == "__main__":
    main()
