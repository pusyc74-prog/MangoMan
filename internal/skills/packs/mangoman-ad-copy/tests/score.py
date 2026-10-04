"""Score Google search ads made for one test case, 0 to 100.

Usage: python3 score.py CASE_FOLDER   (prints {"score": n, "notes": "..."})
The folder holds the case inputs and the contender's ads.json. The ads are
rebuilt here so every contender is judged the same way:
- rules (40): the pack's checker; each FAIL costs 15, each WARN 3;
- keywords (30): share of demand covered, counting a search when all its words
  are in one headline. Demand comes from search_terms.csv (conversions x 10 +
  clicks) or keyword_ideas.csv (monthly searches). Searches for things the
  business does not offer (jobs, pdf, used, wholesale: see JUNK) and searches
  that need a claim the facts do not make ("organic") are left out;
- headlines (15): 7 for 15 headlines in each ad group, 8 for variety (share
  of headlines that do not repeat the words of another in the group); only ad
  groups that lead to the landing page address count;
- negatives (15): share of wasted demand (cost, or monthly searches) that the
  negatives block in every ad group; each wanted search that converted and is
  blocked costs 5.
Each claim word in the ads that the facts do not make costs 5.
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
import check_ads as K  # noqa: E402

STOP = set("a an and as at by for from in into of on or the to with is are your you our near me".split())
CLAIMS = {"free", "natural", "organic", "pure", "original", "authentic", "genuine", "certified", "best", "top",
          "cheapest", "lowest", "guaranteed", "premium"}
JUNK = {"job", "jobs", "salary", "vacancy", "recipe", "machine", "wholesale", "franchise", "distributor", "pdf",
        "download", "app", "free", "course", "training", "second", "used", "olx", "rent", "meaning"}


def terms(text):
    out = set()
    for w in re.findall(r"[\wऀ-෿]+", text.lower()):
        if w not in STOP:
            out.add(w[:-1] if len(w) > 4 and w.endswith("s") else w)
    return out


def score(case):
    spec_path = os.path.join(case, "ads.json")
    if not os.path.exists(spec_path):
        return 0, "no ads.json"
    try:
        spec = json.load(open(spec_path, encoding="utf-8"))
    except ValueError as ex:
        return 0, "ads.json is not valid JSON: %s" % ex
    out = os.path.join(tempfile.mkdtemp(), "ads")
    b = subprocess.run([sys.executable, os.path.join(SCRIPTS, "build_ads.py"), spec_path, "--out", out],
                       capture_output=True, text=True, cwd=case)
    if b.returncode:
        return 0, "build failed: " + (b.stderr or b.stdout).strip().splitlines()[-1][:200]
    c = subprocess.run([sys.executable, os.path.join(SCRIPTS, "check_ads.py"), spec_path, out],
                       capture_output=True, text=True, cwd=case)
    lines = c.stdout.splitlines()
    fails = [x for x in lines if x.startswith("FAIL")]
    warns = [x for x in lines if x.startswith("WARN")]
    rules = max(0, 40 - 15 * len(fails) - 3 * len(warns))

    # what the business says about itself, leaving out lines that say what it is not
    said = "".join(open(os.path.join(case, f), encoding="utf-8").read() for f in ("landing.md", "facts.md"))
    known = terms(" ".join(l for l in said.splitlines() if not re.search(r"\b(not|no)\b", l, re.I)))
    report = os.path.exists(os.path.join(case, "search_terms.csv"))
    rows = list(csv.DictReader(open(os.path.join(case, "search_terms.csv" if report else "keyword_ideas.csv"), encoding="utf-8")))
    groups = (spec.get("google") or {}).get("ad_groups", [])
    heads = [terms(h) for g in groups for h in g["headlines"]]
    want, waste = {}, {}
    for r in rows:
        t = r["search_term" if report else "keyword"]
        if terms(t) & JUNK and (not report or int(r["conversions"]) == 0):
            waste[t] = float(r["cost"]) if report else int(r["avg_monthly_searches"])
        elif not any(w in CLAIMS and w not in known for w in terms(t)):
            want[t] = int(r["conversions"]) * 10 + int(r["clicks"]) if report else int(r["avg_monthly_searches"])
    covered = sum(w for t, w in want.items() if any(terms(t) <= h for h in heads))
    keywords = 30 * covered / sum(want.values())

    page = re.search(r"https://\S+", said).group(0).rstrip("/")
    home = [g for g in groups if g["final_url"].split("?")[0].rstrip("/") == page]
    count = sum(7 * min(1, len(g["headlines"]) / 15) for g in home) / max(1, len(groups))
    alike = lambda a, b: len(a & b) >= 0.6 * len(a | b)
    fresh = sum(1 for g in home for i, h in enumerate(g["headlines"])
                if not any(alike(terms(h), terms(x)) for j, x in enumerate(g["headlines"]) if j != i))
    headlines = count + 8 * fresh / max(1, len(heads))

    stopped = lambda t: groups and all(any(K.blocks(n, t) for n in g.get("negatives", [])) for g in groups)
    blocked = sum(w for t, w in waste.items() if stopped(t))
    lost = [r["search_term"] for r in rows if report and r["search_term"] in want and int(r["conversions"]) > 0 and stopped(r["search_term"])]
    negatives = max(0, 15 * blocked / sum(waste.values()) - 5 * len(lost))

    text = " ".join(x for g in groups for x in g["headlines"] + g["descriptions"])
    unbacked = sorted(w for w in terms(text) & CLAIMS if w not in known)
    quality = max(0, keywords + headlines + negatives - 5 * len(unbacked))
    total = round(rules + quality, 1)
    notes = "rules %d/40, keywords %.1f/30 (%.0f%% of demand), headlines %.1f/15 (%d in %d groups, %d varied), negatives %.1f/15 (%.0f%% of waste blocked)" % (
        rules, keywords, 100 * covered / sum(want.values()), headlines, len(heads), len(groups), fresh, negatives, 100 * blocked / sum(waste.values()))
    if lost:
        notes += "; negatives block searches that convert: " + ", ".join(lost)
    if unbacked:
        notes += "; claims not in the facts: " + ", ".join(unbacked)
    if fails:
        notes += "; " + "; ".join(f[6:80] for f in fails[:3])
    return total, notes


if __name__ == "__main__":
    s, n = score(sys.argv[1])
    print(json.dumps({"score": s, "notes": n}))
