"""Turn what read_social.py saved into numbers the research can cite.

Usage: python3 summarise_social.py social --out social
Reads every account and page file in the folder and writes
social-research.json (per account: posting rate, format mix, typical likes,
comments and views, engagement against followers, the top posts with their
first lines, top hashtags, posting days; per page: where its text and
screenshot are; and what could not be read, with why) and social-research.md,
the same for reading. Every number is computed here from what the sites
showed, so the strategy can quote it.
"""
import collections
import datetime as dt
import json
import os
import statistics
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import checks as C  # noqa: E402

OUT = "social-research"
TALK = ("reddit", "hackernews")  # searches of what people say, not accounts
DAYS = ("Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun")


def load_records(folder):
    recs = []
    for f in sorted(os.listdir(folder)):
        if f.endswith(".json") and f.split("-")[0] in ("instagram", "video", "page", "reddit", "hn"):
            r = C.load_json(os.path.join(folder, f))
            r["file"] = f
            recs.append(r)
    return recs


def problems(r, folder):
    """What is wrong with one saved file, in words; empty when it can be used."""
    f = r["file"]
    miss = [k for k in ("platform", "account", "url", "read_at", "ok", "why") if k not in r]
    if miss:
        return ["%s: missing %s" % (f, ", ".join(miss))]
    try:
        if dt.datetime.fromisoformat(r["read_at"]).tzinfo is None:
            return ["%s: read_at needs its time zone, for example 2026-10-20T10:00:00+00:00" % f]
    except (TypeError, ValueError):
        return ["%s: read_at is not a date and time" % f]
    if not r["ok"]:
        return [] if r["why"] else ["%s: not read, and no reason given" % f]
    if r["platform"] == "page":
        ok = r.get("text") and os.path.exists(os.path.join(folder, r.get("screenshot") or ""))
        return [] if ok else ["%s: a read page needs its text and its screenshot file" % f]
    profile = r.get("profile") or {}
    if not isinstance(profile, dict) or not isinstance(profile.get("followers", 0) or 0, int):
        return ["%s: profile must be an object, with followers a whole number or null" % f]
    if not isinstance(r.get("posts"), list) or not (r["posts"] or r["platform"] in TALK):
        return ["%s: read, but no posts" % f]
    return ["%s: post %d: %s" % (f, i, w) for i, p in enumerate(r["posts"], 1) for w in post_problems(p)][:5]


def post_problems(p):
    if not isinstance(p, dict):
        return ["not an object"]
    out = [k for k in ("url", "type") if not isinstance(p.get(k), str) or not p[k]]
    out = ["needs %s" % ", ".join(out)] if out else []
    if p.get("date") and not _iso(p["date"]):
        out.append("date must be written as YYYY-MM-DD")
    out += ["%s must be a whole number or null" % k for k in ("likes", "comments", "views")
            if p.get(k) is not None and (not isinstance(p[k], int) or isinstance(p[k], bool))]
    if not isinstance(p.get("hashtags", []), list) or not isinstance(p.get("caption", ""), str):
        out.append("hashtags must be a list and caption text")
    return out


def _iso(s):
    try:
        dt.date.fromisoformat(s)
        return True
    except (TypeError, ValueError):
        return False


def median(xs):
    xs = [x for x in xs if isinstance(x, (int, float))]
    return round(statistics.median(xs)) if xs else None


def first_line(text):
    return (text or "").strip().split("\n", 1)[0][:140]


def stats(r):
    posts = r["posts"]
    dates = sorted(dt.date.fromisoformat(p["date"]) for p in posts if p.get("date"))
    profile = r.get("profile") or {}
    span = (dates[-1] - dates[0]).days if len(dates) > 1 else 0
    eng = [(p.get("likes") or 0) + (p.get("comments") or 0) for p in posts if p.get("likes") is not None]
    followers = profile.get("followers")
    typical = median(eng)
    top = sorted(posts, key=lambda p: (p.get("views") or 0, (p.get("likes") or 0) + (p.get("comments") or 0)), reverse=True)[:3]
    tags = collections.Counter(h.lower() for p in posts for h in p.get("hashtags") or [] if isinstance(h, str))
    return {
        "platform": r["platform"], "account": r["account"], "url": r["url"], "read_at": r["read_at"][:10],
        "name": profile.get("name", ""), "followers": followers, "posts_read": len(posts),
        "newest": dates[-1].isoformat() if dates else "", "oldest": dates[0].isoformat() if dates else "",
        "posts_per_week": round((len(dates) - 1) * 7 / span, 1) if span else None,
        "formats": dict(collections.Counter(p["type"] for p in posts).most_common()),
        "median_likes": median([p.get("likes") for p in posts]), "median_comments": median([p.get("comments") for p in posts]),
        "median_views": median([p.get("views") for p in posts]), "median_engagement": typical,
        "engagement_rate_pct": round(typical * 100 / followers, 2) if typical is not None and followers else None,
        "top_posts": [{"url": p["url"], "date": p.get("date") or "", "type": p["type"], "first_line": first_line(p.get("title") or p.get("caption")),
                       "likes": p.get("likes"), "comments": p.get("comments"), "views": p.get("views")} for p in top],
        "top_hashtags": [h for h, _ in tags.most_common(10)],
        "days": dict(collections.Counter(DAYS[d.weekday()] for d in dates).most_common()),
        "bio": profile.get("bio", ""), "note": r.get("note", ""),
        "communities": dict(collections.Counter(p["community"] for p in posts if p.get("community")).most_common(5)),
    }


def summary(recs):
    out = {"accounts": [], "pages": [], "not_read": []}
    for r in recs:
        if not r.get("ok"):
            out["not_read"].append({"platform": r["platform"], "account": r["account"], "url": r["url"], "why": r["why"],
                                    "file": r["file"], "screenshot": r.get("screenshot", "")})
        elif r["platform"] == "page":
            out["pages"].append({"page": r["account"], "url": r["url"], "read_at": r["read_at"][:10], "file": r["file"],
                                 "screenshot": r.get("screenshot", ""), "text_chars": len(r.get("text", ""))})
        else:
            out["accounts"].append(stats(r))
    return out


def fmt(v, unit=""):
    return "not shown" if v is None else ("{:,}".format(v) if isinstance(v, int) else str(v)) + unit


def markdown(s):
    md = ["# Social research", "", "Numbers computed from the public pages on the dates shown; \"not shown\" means the site hid it.", ""]
    for a in s["accounts"]:
        talk = a["platform"] in TALK
        if talk and not a["posts_read"]:
            md += ["## %s on %s (%s)" % (a["name"] or a["account"], a["platform"], a["url"]), "",
                   "Read %s. Nothing posted about it in the last 30 days." % a["read_at"], ""]
            continue
        md += ["## %s on %s (%s)" % (a["name"] or a["account"], a["platform"], a["url"]), "",
               "Read %s.%s Posts read: %d, %s." % (
                   a["read_at"], "" if talk else " Followers: %s." % fmt(a["followers"]), a["posts_read"],
                   "from %s to %s, about %s posts a week" % (a["oldest"], a["newest"], fmt(a["posts_per_week"])) if a["newest"] else "dates not shown"),
               "Formats: %s. Posting days: %s." % (", ".join("%s %d" % kv for kv in a["formats"].items()) or "none",
                                                     ", ".join("%s %d" % kv for kv in a["days"].items()) or "not shown"),
               "Typical post: %s likes, %s comments." % (fmt(a["median_likes"]), fmt(a["median_comments"])) if talk else
               "Typical post: %s likes, %s comments, %s views. Engagement against followers: %s." % (
                   fmt(a["median_likes"]), fmt(a["median_comments"]), fmt(a["median_views"]), fmt(a["engagement_rate_pct"], "%")),
               "Where: %s." % ", ".join("%s %d" % kv for kv in a["communities"].items()) if a["communities"] else
               "" if talk else "Top hashtags: %s." % (", ".join("#" + h for h in a["top_hashtags"]) or "none")]
        md = md[:-1] if not md[-1] else md
        md += ["Note: " + a["note"] + "."] if a["note"] else []
        md += ["", "Top posts:", ""]
        md += ["- %s %s: \"%s\" (%s likes, %s comments%s) %s" % (p["date"] or "undated", p["type"], p["first_line"], fmt(p["likes"]),
               fmt(p["comments"]), "" if talk else ", %s views" % fmt(p["views"]), p["url"]) for p in a["top_posts"]] + [""]
    if s["pages"]:
        md += ["## Pages read", ""] + ["- %s: %s (text in %s, screenshot %s), read %s" % (p["page"], p["url"], p["file"], p["screenshot"], p["read_at"])
                                       for p in s["pages"]] + [""]
    if s["not_read"]:
        md += ["## Could not read", ""] + ["- %s %s: %s" % (n["platform"], n["account"], n["why"]) for n in s["not_read"]] + [""]
    return "\n".join(md)


def main():
    a = sys.argv[1:]
    if not a:
        sys.exit(__doc__)
    out = a[a.index("--out") + 1] if "--out" in a else a[0]
    recs = load_records(a[0])
    if not recs:
        sys.exit("no account or page files in %s: run read_social.py first" % a[0])
    bad = [x for r in recs for x in problems(r, a[0])]
    if bad:
        sys.exit("files to fix or read again:\n- " + "\n- ".join(bad))
    s = summary(recs)
    os.makedirs(out, exist_ok=True)
    with open(os.path.join(out, OUT + ".json"), "w", encoding="utf-8") as f:
        json.dump(s, f, ensure_ascii=False, indent=1)
    with open(os.path.join(out, OUT + ".md"), "w", encoding="utf-8") as f:
        f.write(markdown(s))
    print("wrote %s.json and %s.md: %d accounts, %d pages, %d not read" % (OUT, OUT, len(s["accounts"]), len(s["pages"]), len(s["not_read"])))


if __name__ == "__main__":
    main()
