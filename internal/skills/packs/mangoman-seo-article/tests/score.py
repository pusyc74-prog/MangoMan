"""Score an SEO article made for one test case, 0 to 100.

Usage: python3 score.py CASE_FOLDER   (prints {"score": n, "notes": "..."})
The folder holds the case inputs and the contender's article.json. The
article is rebuilt here so every contender is judged the same way:
- rules (40): the pack's checker; each FAIL costs 15, each WARN 3;
- keywords (30): share of monthly searches in keywords.csv covered: the
  brief's main keyword in the title and the h1, every other keyword in a
  subheading (half credit when only in one sentence of the text); keywords
  that need a claim the facts and sources do not make ("organic") are left out;
- questions (20): share of the brief's reader questions that have their own
  subheading or FAQ question;
- sources (10): sources from sources.md that the article cites, 3 for full marks.
"""
import csv
import json
import os
import re
import subprocess
import sys
import tempfile

SCRIPTS = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "scripts")
sys.path.insert(0, SCRIPTS)
import build_article as B  # noqa: E402
import check_article as K  # noqa: E402

STOP = set("a an and as at by for from in into of on or the to with vs is are be it do does did can i my me "
           "how what which why when where much many should will".split())
CLAIMS = {"free", "natural", "organic", "pure", "original", "authentic", "genuine", "certified", "best", "top",
          "cheapest", "painless", "guaranteed", "permanent", "safest"}


def terms(text):
    out = set()
    for w in K.words(text):
        if w not in STOP:
            out.add(w[:-1] if len(w) > 4 and w.endswith("s") else w)
    return out


def score(case):
    spec_path = os.path.join(case, "article.json")
    if not os.path.exists(spec_path):
        return 0, "no article.json"
    try:
        spec = json.load(open(spec_path, encoding="utf-8"))
    except ValueError as ex:
        return 0, "article.json is not valid JSON: %s" % ex
    out = os.path.join(tempfile.mkdtemp(), "article")
    b = subprocess.run([sys.executable, os.path.join(SCRIPTS, "build_article.py"), spec_path, "--out", out, "--no-shots"],
                       capture_output=True, text=True, cwd=case)
    if b.returncode:
        return 0, "build failed: " + (b.stderr or b.stdout).strip().splitlines()[-1][:200]
    c = subprocess.run([sys.executable, os.path.join(SCRIPTS, "check_article.py"), spec_path, out],
                       capture_output=True, text=True, cwd=case)
    lines = c.stdout.splitlines()
    fails = [x for x in lines if x.startswith("FAIL")]
    warns = [x for x in lines if x.startswith("WARN")]
    rules = max(0, 40 - 15 * len(fails) - 3 * len(warns))

    read = lambda f: open(os.path.join(case, f), encoding="utf-8").read()
    brief, sources = read("brief.md"), read("sources.md")
    known = terms(brief + read("facts.md") + sources)
    main = re.search(r"^- Main keyword:\s*(.+)$", brief, re.M).group(1).strip()
    a = spec["article"]
    subs = [terms(h) for h in B.headings(spec)[1:] + [f["q"] for f in a.get("faq", [])]]
    sents = [terms(s) for _, t in B.blocks(spec) for s in K.SENT.split(B.plain(t))]
    weight, got = {}, {}
    for r in csv.DictReader(open(os.path.join(case, "keywords.csv"), encoding="utf-8")):
        k = terms(r["keyword"])
        if any(w in CLAIMS and w not in known for w in k):
            continue
        weight[r["keyword"]] = int(r["monthly_searches"])
        if r["keyword"] == main:
            got[r["keyword"]] = (k <= terms(spec["meta"]["title"])) / 2 + (k <= terms(a["h1"])) / 2
        else:
            got[r["keyword"]] = 1 if any(k <= h for h in subs) else 0.5 if any(k <= s for s in sents) else 0
    share = sum(weight[k] * got[k] for k in weight) / sum(weight.values())
    keywords = 30 * share

    asked = re.findall(r"^- (.+\?)\s*$", brief, re.M)
    answered = [q for q in asked if any(len(terms(q) & h) >= 0.5 * len(terms(q)) for h in subs)]
    questions = 20 * len(answered) / len(asked)

    urls = {u.rstrip("/") for u in re.findall(r"https://\S+", sources)}
    cited = {n for _, t in B.blocks(spec) for n in B.CITE.findall(t)}
    used = [x for x in spec.get("sources", []) if str(x.get("id")) in cited and str(x.get("url", "")).rstrip("/") in urls]
    src = 10 * min(1, len(used) / 3)

    total = round(rules + keywords + questions + src, 1)
    notes = "rules %d/40, keywords %.1f/30 (%.0f%% of searches), questions %.1f/20 (%d of %d), sources %.1f/10 (%d cited)" % (
        rules, keywords, 100 * share, questions, len(answered), len(asked), src, len(used))
    if fails:
        notes += "; " + "; ".join(f[6:80] for f in fails[:3])
    return total, notes


if __name__ == "__main__":
    s, n = score(sys.argv[1])
    print(json.dumps({"score": s, "notes": n}))
