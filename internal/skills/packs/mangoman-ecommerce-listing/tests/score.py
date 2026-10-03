"""Score an Amazon listing made for one test case, 0 to 100.

Usage: python3 score.py CASE_FOLDER   (prints {"score": n, "notes": "..."})
The folder holds the case inputs and the contender's listing.json. The
listing is rebuilt here so every contender is judged the same way:
- rules (40): the pack's checker; each FAIL costs 15, each WARN 3;
- keywords (30): share of shopper demand covered, from search_terms.csv
  (weight = orders x 10 + clicks), counting a term when all its words are in
  the title, bullets or backend search terms (what Amazon indexes); searches
  that need a claim the facts do not make ("sugar free") are left out;
- title (15): the top search term in the first 80 characters (what phones
  show), and an item name of at least 40 characters;
- complete (15): 5 bullets of 150 to 500 characters, backend terms of 150 to
  249 bytes, an image plan of 6 or more images, each with alt text.
"""
import csv
import json
import os
import subprocess
import sys
import tempfile

SCRIPTS = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "scripts")
sys.path.insert(0, SCRIPTS)
import build_listing as B  # noqa: E402
import check_listing as K  # noqa: E402


def terms(text):
    return {K.stem(w) for w in K.words(text) if w not in K.STOP}


def score(case):
    spec_path = os.path.join(case, "listing.json")
    if not os.path.exists(spec_path):
        return 0, "no listing.json"
    try:
        spec = json.load(open(spec_path, encoding="utf-8"))
    except ValueError as ex:
        return 0, "listing.json is not valid JSON: %s" % ex
    tmp = tempfile.mkdtemp()
    out = os.path.join(tmp, "listing")
    b = subprocess.run([sys.executable, os.path.join(SCRIPTS, "build_listing.py"), spec_path, "--out", out],
                       capture_output=True, text=True, cwd=case)
    if b.returncode:
        return 0, "build failed: " + (b.stderr or b.stdout).strip().splitlines()[-1][:200]
    c = subprocess.run([sys.executable, os.path.join(SCRIPTS, "check_listing.py"), spec_path, out],
                       capture_output=True, text=True, cwd=case)
    lines = c.stdout.splitlines()
    fails = [x for x in lines if x.startswith("FAIL")]
    warns = [x for x in lines if x.startswith("WARN")]
    rules = max(0, 40 - 15 * len(fails) - 3 * len(warns))

    a = spec.get("amazon") or {}
    title = B.amazon_title(a)
    bullets = [x for x in a.get("bullets", []) if isinstance(x, str)]
    backend = a.get("search_terms", "") or ""
    indexed = terms(" ".join([title, backend] + bullets))
    rows = list(csv.DictReader(open(os.path.join(case, "search_terms.csv"), encoding="utf-8")))
    facts = terms(open(os.path.join(case, "facts.md"), encoding="utf-8").read())
    weight = {r["search_term"]: int(r["orders"]) * 10 + int(r["clicks"]) for r in rows
              if not any(w in K.CLAIMS and w not in facts for w in terms(r["search_term"]))}
    total = sum(weight.values()) or 1
    covered = sum(w for t, w in weight.items() if terms(t) <= indexed)
    keywords = 30 * covered / total

    top = max(weight, key=weight.get)
    title_pts = (10 if terms(top) <= terms(title[:80]) else 0) + (5 if len(a.get("item_name", "")) >= 40 else 0)
    images = spec.get("images", [])
    complete = (5 if len(bullets) == 5 and all(150 <= len(x) <= 500 for x in bullets) else 0) + \
        (5 if 150 <= len(backend.encode("utf-8")) <= 249 else 0) + \
        (5 if len(images) >= 6 and all(i.get("alt") for i in images) else 0)
    total_score = round(rules + keywords + title_pts + complete, 1)
    notes = "rules %d/40, keywords %.1f/30 (%.0f%% of demand), title %d/15, complete %d/15" % (
        rules, keywords, 100 * covered / total, title_pts, complete)
    if fails:
        notes += "; " + "; ".join(f[6:80] for f in fails[:3])
    return total_score, notes


if __name__ == "__main__":
    s, n = score(sys.argv[1])
    print(json.dumps({"score": s, "notes": n}))
