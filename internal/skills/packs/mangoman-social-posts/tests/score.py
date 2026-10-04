"""Score a social post set made for one test case, 0 to 100.

Usage: python3 score.py CASE_FOLDER   (prints {"score": n, "notes": "..."})
The folder holds the case inputs (brief.md and any logo) and the contender's
posts.json. The set is rebuilt here so every contender is judged the same way:
- rules (40): the pack's checker; each FAIL costs 15, each WARN 3;
- coverage (15): every platform the brief asks for, plus stories when it asks
  (10); exactly the number of posts it asks for (5);
- message (25): share of captions (each post on each platform) that carry
  the brief's call to action, 3 in 4 of its words (15); share of posts whose
  image text and captions carry the key message, half of its words (10);
- hashtags (10): share of hashtags used that come from the brief's list (5);
  share of posts with 3 to 8 hashtags (5);
- variety (10): different layouts, full marks at 4 (or one per post).
Claims cost 5 each: a number in the posts that the brief does not give.
"""
import json
import math
import os
import re
import subprocess
import sys
import tempfile

SCRIPTS = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "scripts")
sys.path.insert(0, SCRIPTS)
import build_posts as B  # noqa: E402
import check_posts as K  # noqa: E402
import checks as C  # noqa: E402

STOP = set("a an and as at be by for from in into is it of on or our the this to us we with you your".split())


def stem(w):
    for suf in ("ing", "ed", "es", "s"):
        if len(w) > 4 and w.endswith(suf):
            return w[: -len(suf)]
    return w


def terms(text):
    return {stem(w) for w in re.findall(r"\w+", text.lower()) if w not in STOP}


def field(brief, name):
    m = re.search(r"^%s\s*:\s*(.+)$" % name, brief, re.I | re.M)
    return m.group(1).strip() if m else ""


def has(text, want, share):
    return len(want & terms(text)) >= math.ceil(share * len(want))


def score(case):
    spec_path = os.path.join(case, "posts.json")
    if not os.path.exists(spec_path):
        return 0, "no posts.json"
    try:
        spec = json.load(open(spec_path, encoding="utf-8"))
    except ValueError as ex:
        return 0, "posts.json is not valid JSON: %s" % ex
    out = os.path.join(tempfile.mkdtemp(), "posts")
    b = subprocess.run([sys.executable, os.path.join(SCRIPTS, "build_posts.py"), spec_path, "--out", out],
                       capture_output=True, text=True, cwd=case)
    if b.returncode:
        return 0, "build failed: " + ((b.stderr or b.stdout).strip().splitlines() or ["?"])[-1][:200]
    c = subprocess.run([sys.executable, os.path.join(SCRIPTS, "check_posts.py"), spec_path, out],
                       capture_output=True, text=True, cwd=case)
    lines = c.stdout.splitlines()
    fails = [x for x in lines if x.startswith("FAIL")]
    warns = [x for x in lines if x.startswith("WARN")]
    rules = max(0, 40 - 15 * len(fails) - 3 * len(warns))

    brief = open(os.path.join(case, "brief.md"), encoding="utf-8").read()
    posts = spec["posts"]
    platforms = spec.get("platforms", ["instagram"])
    asked = [p.split()[0].lower() for p in field(brief, "platforms").split(",") if p.strip()]
    want = set(asked) | ({"stories"} if field(brief, "stories").lower().startswith("y") else set())
    have = set(platforms) | ({"stories"} if spec.get("stories") else set())
    coverage = 10 * len(want & have) / len(want) + (5 if len(posts) == int(field(brief, "posts") or 0) else 0)

    cta, key = terms(field(brief, "call to action")), terms(field(brief, "key message"))
    caps = [B.caption_for(p, pl) or "" for p in posts for pl in platforms]
    with_cta = sum(has(t, cta, 0.75) for t in caps) / max(1, len(caps))
    on_message = sum(has(" ".join(K.texts(p) + [B.caption_for(p, pl) or "" for pl in platforms]), key, 0.5)
                     for p in posts) / max(1, len(posts))
    message = 15 * with_cta + 10 * on_message

    listed = {t.lower() for t in re.findall(r"#(\w+)", field(brief, "hashtags"))}
    used = {t.lstrip("#").lower() for p in posts for t in p.get("hashtags", [])}
    relevant = len(used & listed) / max(1, len(used))
    sized = sum(3 <= len(p.get("hashtags", [])) <= 8 for p in posts) / max(1, len(posts))
    tags = 5 * relevant + 5 * sized

    variety = 10 * min(1, len({p["layout"] for p in posts}) / min(4, len(posts)))

    pool = C.fact_pool(brief)
    shown = [t for p in posts for t in K.texts(p) + [p.get("alt", "")]] + caps
    invented = sorted({u for t in shown for u in C.untraced(t, pool)})
    penalty = 5 * len(invented)
    total = max(0, round(rules + coverage + message + tags + variety - penalty, 1))
    notes = "rules %d/40, coverage %.1f/15, message %.1f/25 (%d%% of captions with the call to action, %d%% of posts on message), " \
        "hashtags %.1f/10, variety %.1f/10" % (rules, coverage, message, 100 * with_cta, 100 * on_message, tags, variety)
    if invented:
        notes += "; numbers not in the brief -%d: %s" % (penalty, ", ".join(invented)[:200])
    if fails:
        notes += "; " + "; ".join(f[6:80] for f in fails[:3])
    return total, notes


if __name__ == "__main__":
    s, n = score(sys.argv[1])
    print(json.dumps({"score": s, "notes": n}))
