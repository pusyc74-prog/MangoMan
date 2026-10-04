"""Mine a Google Ads search term report (or Keyword Planner ideas) for ads.

Usage: python3 mine.py [--terms REPORT.csv] [--ideas IDEAS.csv] [--facts facts.md] [--landing landing.md] --out mining
Reads the search term report (Google Ads export: "Search term", "Clicks",
"Cost", "Conversions"; currency signs and commas are fine) and/or keyword
ideas ("Keyword", "Avg. monthly searches"). Writes mining.json (used by
fit.py) and mining.md: searches ranked by demand, words by demand, wasted
searches, negatives that block the waste without blocking any search worth
keeping, and ad groups by theme with keywords and match types.
Demand = conversions x 10 + clicks; for ideas, monthly searches (divided by
100 when a report is given too). Waste = no conversions and either spend
above the account's cost per conversion or a junk word (jobs, pdf, used...)
for something the facts do not offer. Ideas only have junk-word waste.
"""
import csv
import json
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import check_ads as K  # noqa: E402

STOP = set("a an and as at by for from in into of on or the to with is are your you our near me".split())
# Words that make a claim; ads may use them only when the facts do.
CLAIMS = {"free", "natural", "organic", "pure", "original", "authentic", "genuine", "certified", "best", "top", "cheapest",
          "lowest", "guaranteed", "premium", "fresh", "healthy", "herbal", "ayurvedic", "vegan", "cheap", "no1"}
# Searches for things most businesses do not sell.
JUNK = {"job", "jobs", "salary", "vacancy", "career", "hiring", "internship", "recipe", "machine", "wholesale", "franchise",
        "distributor", "dealership", "pdf", "download", "app", "free", "course", "training", "second", "used", "olx",
        "rent", "meaning", "diy"}


def words(text):
    return re.findall(r"[\wऀ-෿]+", text.lower())


def stem(w):
    return w[:-1] if len(w) > 4 and w.endswith("s") else w


def terms(text):
    """Words that carry meaning, with a plural s dropped (as the pack's test set counts them)."""
    return {stem(w) for w in words(text) if w not in STOP}


def known_terms(text):
    """Terms the business says about itself, leaving out lines that say what it is not."""
    return terms(" ".join(x for x in text.splitlines() if not re.search(r"\b(not|no)\b", x, re.I)))


def number(x):
    x = re.sub(r"[^\d.]", "", str(x or ""))
    return float(x) if x.strip(".") else 0.0


def read_rows(path):
    """Rows as dicts with plain header names, from comma or tab CSV in UTF-8 or UTF-16, skipping title lines."""
    raw = open(path, "rb").read()
    text = raw.decode("utf-16") if raw[:2] in (b"\xff\xfe", b"\xfe\xff") else raw.decode("utf-8-sig")
    rows = list(csv.reader(text.splitlines(), delimiter="\t" if text.count("\t") > text.count(",") else ","))
    names = ("search term", "keyword", "query")
    for i, r in enumerate(rows):
        h = [re.sub(r"[^a-z ]", "", c.lower().replace("_", " ")).strip() for c in r]
        if any(n in h for n in names):
            return [dict(zip(h, x)) for x in rows[i + 1:] if x and not x[0].lower().startswith("total")]
    sys.exit("cannot find a Search term or Keyword column in %s" % path)


def pick(row, *names):
    return next((n for n in names if n in row), None)


def read_report(path):
    rows = read_rows(path)
    if not rows:
        sys.exit("%s has no rows" % path)
    term = pick(rows[0], "search term", "query", "keyword")
    conv = pick(rows[0], "conversions", "conv", "all conv")
    if not conv or not pick(rows[0], "clicks"):
        sys.exit("%s needs Clicks and Conversions columns (has: %s)" % (path, ", ".join(rows[0])))
    out = {}
    for r in rows:
        t = " ".join(words(r[term]))
        if t:
            s = out.setdefault(t, {"term": t, "clicks": 0, "cost": 0.0, "conversions": 0.0})
            s["clicks"] += number(r["clicks"])
            s["cost"] += number(r.get("cost"))
            s["conversions"] += number(r[conv])
    return out


def read_ideas(path):
    rows = read_rows(path)
    vol = rows and pick(rows[0], "avg monthly searches", "searches", "volume")
    if not vol:
        sys.exit("%s needs Keyword and Avg. monthly searches columns" % path)
    return {" ".join(words(r["keyword"])): number(r[vol]) for r in rows if words(r["keyword"])}


def mine(report, ideas, known):
    spend = lambda s: s["cost"] or s["clicks"]  # noqa: E731
    conv = sum(s["conversions"] for s in report.values())
    cpa = sum(spend(s) for s in report.values()) / conv if conv else None
    junk = JUNK - known
    searches = []
    for t, s in report.items():
        bad = s["conversions"] == 0 and (terms(t) & junk or (cpa and spend(s) >= cpa))
        searches.append(dict(s, weight=s["conversions"] * 10 + s["clicks"], waste=spend(s) if bad else 0))
    for t, v in ideas.items():
        if t not in report:
            searches.append({"term": t, "weight": v / 100 if report else v, "waste": (v / 100 if report else v) if terms(t) & junk else 0})
    searches.sort(key=lambda s: -s["weight"])
    for s in searches:
        s["claims"] = sorted(w for w in terms(s["term"]) if w in CLAIMS and w not in known)
    return searches


def negatives(searches):
    """Single words (else the whole search) that block each wasted search and no search worth keeping."""
    keep = [s["term"] for s in searches if not s["waste"]]
    waste = [s for s in searches if s["waste"]]
    safe = lambda n: not any(K.blocks(n, k) for k in keep)  # noqa: E731
    chosen = []
    for s in sorted(waste, key=lambda s: -s["waste"]):
        if any(K.blocks(n, s["term"]) for n in chosen):
            continue
        cands = [w for w in dict.fromkeys(words(s["term"])) if w not in STOP and not w.isdigit() and safe(w)]
        cands.sort(key=lambda w: (w not in JUNK, -sum(x["waste"] for x in waste if K.blocks(w, x["term"]))))
        n = cands[0] if cands else s["term"] if safe(s["term"]) else None
        if n:
            chosen.append(n)
    return chosen


def groups(wanted):
    """Ad groups by theme: each search goes under its highest-demand word that is not in most searches."""
    weight, count = {}, {}
    for s in wanted:
        for w in terms(s["term"]):
            weight[w] = weight.get(w, 0) + s["weight"]
            count[w] = count.get(w, 0) + 1
    total = sum(s["weight"] for s in wanted) or 1
    out = {}
    for s in wanted:
        ws = sorted((w for w in terms(s["term"]) if not w.isdigit()), key=lambda w: -weight[w])
        theme = next((w for w in ws if count[w] <= len(wanted) / 2), ws[0] if ws else s["term"])
        match = "exact" if s["weight"] >= 0.1 * total else "phrase"
        out.setdefault(theme, []).append({"text": s["term"], "match": match})
    spelled = {}
    for s in wanted:
        for w in words(s["term"]):
            spelled.setdefault(stem(w), w)
    ranked = sorted(out.items(), key=lambda x: -len(x[1]))
    for t, k in ranked[1:]:
        if len(k) == 1:  # a theme of one search joins the biggest group
            ranked[0][1].extend(k)
    return [{"theme": spelled.get(t, t), "keywords": k[:20]} for t, k in ranked if k is ranked[0][1] or len(k) > 1]


def main():
    a = sys.argv[1:]
    opt = lambda k: a[a.index(k) + 1] if k in a else None  # noqa: E731
    if not (opt("--terms") or opt("--ideas")):
        sys.exit(__doc__)
    out = opt("--out") or "mining"
    about = "".join(open(opt(k), encoding="utf-8").read() + "\n" for k in ("--facts", "--landing") if opt(k))
    known = known_terms(about)
    report = read_report(opt("--terms")) if opt("--terms") else {}
    searches = mine(report, read_ideas(opt("--ideas")) if opt("--ideas") else {}, known)
    negs = negatives(searches)
    keep = [s["term"] for s in searches if not s["waste"]]
    clash = [k for k in keep if any(K.blocks(n, k) for n in negs)]
    if clash:
        sys.exit("bug: negatives block searches worth keeping: %s" % ", ".join(clash))
    waste = [s for s in searches if s["waste"]]
    blocked = [s["term"] for s in waste if any(K.blocks(n, s["term"]) for n in negs)]
    wanted = [s for s in searches if not s["waste"] and not s["claims"]]
    total = sum(s["weight"] for s in wanted) or 1
    weights = {}
    for s in wanted:
        for w in dict.fromkeys(words(s["term"])):
            if w not in STOP:
                weights[w] = weights.get(w, 0) + s["weight"]
    res = {
        "demand": "conversions x 10 + clicks" if report else "average monthly searches",
        "known": sorted(known),
        "searches": searches,
        "wanted": [{"term": s["term"], "weight": s["weight"], "share": round(s["weight"] / total, 4)} for s in wanted],
        "words": [w for w, _ in sorted(weights.items(), key=lambda x: -x[1])],
        "waste": [{"term": s["term"], "waste": s["waste"], "blocked": s["term"] in blocked} for s in waste],
        "claim_searches": [s["term"] for s in searches if not s["waste"] and s["claims"]],
        "negatives": negs,
        "groups": groups(wanted),
    }
    with open(out + ".json", "w", encoding="utf-8") as f:
        json.dump(res, f, indent=1, ensure_ascii=False)
    unit = "spend" if report else "monthly searches"
    md = ["# Search term mining", "", "Demand = %s." % res["demand"], "",
          "## Searches to win", "", "| Search | Share of demand |" + (" Conversions |" if report else ""), "| --- | --- |" + (" --- |" if report else "")]
    md += ["| %s | %.1f%% |%s" % (s["term"], 100 * s["weight"] / total, " %g |" % s["conversions"] if report else "") for s in wanted[:20]]
    md += ["", "Words by demand: %s." % ", ".join(res["words"][:25]), ""]
    if res["claim_searches"]:
        md += ["Searches that need a claim the facts do not make (do not write the claim): %s." % ", ".join(res["claim_searches"]), ""]
    md += ["## Wasted searches (%s)" % unit, ""] + ["- %s: %g%s" % (w["term"], w["waste"], "" if w["blocked"] else " (not blocked)") for w in res["waste"]]
    md += ["", "## Negative keywords", "", ", ".join(negs) or "none", "",
           "These block %d of %d wasted searches and none of the searches worth keeping." % (len(blocked), len(waste)), "",
           "## Ad groups by theme", ""]
    md += ["- **%s**: %s" % (g["theme"], ", ".join(("[%s]" if k["match"] == "exact" else '"%s"') % k["text"] for k in g["keywords"])) for g in res["groups"]]
    with open(out + ".md", "w", encoding="utf-8") as f:
        f.write("\n".join(md) + "\n")
    print("wrote %s.json and %s.md: %d searches, %d to win, %d wasted (%d blocked by %d negatives, 0 searches worth keeping blocked), %d ad group themes" % (
        out, out, len(searches), len(wanted), len(waste), len(blocked), len(negs), len(res["groups"])))


if __name__ == "__main__":
    main()
