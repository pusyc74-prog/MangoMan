"""Score website copy made for one test case, 0 to 100.

Usage: python3 score.py CASE_FOLDER   (prints {"score": n, "notes": "..."})
The folder holds the case inputs and the contender's site.json. The copy is
rebuilt here so every contender is judged the same way. What the copy must
contain lives in expect.json next to this file, outside the case folder, keyed
by the business name in brief.md:
- rules (40): the pack's checker; each FAIL costs 15, each WARN 3;
- fact (15): the one fact that must appear is in the copy;
- competitor (15): the competitor claim is not copied (no five-word run) and
  the figure it boasts of is not used;
- pages (20): every page the brief asked for is in the site;
- voice (10): 5 when no word the brief says to avoid is used, 5 when sentences
  average 20 words or fewer.
"""
import json
import os
import re
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPTS = os.path.join(HERE, "..", "scripts")
sys.path.insert(0, SCRIPTS)
import build_copy as B  # noqa: E402
import check_copy as K  # noqa: E402


def score(case):
    spec_path = os.path.join(case, "site.json")
    if not os.path.exists(spec_path):
        return 0, "no site.json"
    try:
        spec = json.load(open(spec_path, encoding="utf-8"))
    except ValueError as ex:
        return 0, "site.json is not valid JSON: %s" % ex
    out = os.path.join(tempfile.mkdtemp(), "site")
    b = subprocess.run([sys.executable, os.path.join(SCRIPTS, "build_copy.py"), spec_path, "--out", out], capture_output=True, text=True, cwd=case)
    if b.returncode:
        return 0, "build failed: " + (b.stderr or b.stdout).strip().splitlines()[-1][:200]
    c = subprocess.run([sys.executable, os.path.join(SCRIPTS, "check_copy.py"), spec_path, out], capture_output=True, text=True, cwd=case,
                       env=dict(os.environ, PYTHONPATH=os.pathsep.join([SCRIPTS, os.environ.get("PYTHONPATH", "")])))
    lines = c.stdout.splitlines()
    fails = [x for x in lines if x.startswith("FAIL")]
    warns = [x for x in lines if x.startswith("WARN")]
    rules = max(0, 40 - 15 * len(fails) - 3 * len(warns))

    brief = open(os.path.join(case, "brief.md"), encoding="utf-8").read()
    name = re.search(r"^- Business:\s*([^,(]+)", brief, re.M).group(1).strip()
    want = json.load(open(os.path.join(HERE, "expect.json"), encoding="utf-8"))[name]
    texts = [K.plain(t) for pg in spec["pages"] for t in list(B.texts(pg["sections"])) + [pg["meta"]["title"], pg["meta"]["description"]]]
    joined = " ".join(texts)

    fact = 15 if K.has_phrase(joined, want["must_appear"]) else 0
    taken = any(K.copied(t, want["competitor_claim"]) for t in texts) or any(m in joined for m in want["must_not"])
    comp = 0 if taken else 15
    have = {pg["slug"] for pg in spec["pages"]} | {K.words(pg["name"])[0] for pg in spec["pages"] if K.words(pg["name"])}
    got = [p for p in want["pages"] if p in have]
    pages = 20 * len(got) / len(want["pages"])
    avoid = [w.strip() for w in re.search(r"^- Avoid words:\s*(.+)$", brief, re.M).group(1).split(",")]
    sents = [s for pg in spec["pages"] for s in K.sentences(pg)]
    avg = sum(len(K.words(s)) for s in sents) / max(1, len(sents))
    voice = (5 if not any(K.has_phrase(joined, w) for w in avoid) else 0) + (5 if avg <= 20 else 0)

    total = round(rules + fact + comp + pages + voice, 1)
    notes = "rules %d/40, fact %d/15 (%s), competitor %d/15%s, pages %.0f/20 (%d of %d), voice %d/10" % (
        rules, fact, want["must_appear"], comp, " (copied)" if taken else "", pages, len(got), len(want["pages"]), voice)
    if fails:
        notes += "; " + "; ".join(f[6:80] for f in fails[:3])
    return total, notes


if __name__ == "__main__":
    s, n = score(sys.argv[1])
    print(json.dumps({"score": s, "notes": n}))
