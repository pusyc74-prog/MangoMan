"""Keyword research for an Amazon listing from the seller's own data.

Usage: python3 research.py --terms REPORT.csv [--competitors FILE] [--current FILE] [--brand NAME] --out research
Reads an Amazon search term report (Sponsored Products "Customer Search Term",
Brand Analytics "Search Query", or any CSV with a term, clicks and orders or
purchases column), competitor listings and the current listing. Writes
research.json (used by optimise.py) and research.md: the searches that sell,
the words shoppers use weighted by orders and clicks, what must lead the
title, and the gaps in the current listing and against competitors.
Demand weight = orders x 10 + clicks (impressions / 100 when there are no clicks).
"""
import csv
import json
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import check_listing as K  # noqa: E402


def column(header, *needles):
    """The first header containing any needle (case-insensitive)."""
    for n in needles:
        for h in header:
            if n in h.lower():
                return h
    return None


def number(x):
    x = re.sub(r"[^\d.]", "", str(x or ""))
    return float(x) if x else 0.0


def read_terms(path):
    with open(path, encoding="utf-8-sig", newline="") as f:
        rows = list(csv.DictReader(f))
    if not rows:
        sys.exit("%s has no rows" % path)
    h = list(rows[0])
    term = column(h, "customer search term", "search query", "search term", "keyword", "query", "term")
    clicks = column(h, "clicks: total", "click")
    orders = column(h, "purchases: total", "total orders", "order", "purchase", "conversion")
    impr = column(h, "search query volume", "impression", "volume")
    if not term or not (clicks or orders or impr):
        sys.exit("cannot find the search term and clicks or orders columns in %s (columns: %s)" % (path, ", ".join(h)))
    demand = {}
    for r in rows:
        t = " ".join(K.words(r[term]))
        if not t:
            continue
        w = number(r.get(orders)) * 10 + number(r.get(clicks)) if (clicks or orders) else number(r.get(impr)) / 100
        demand[t] = demand.get(t, 0) + w
    return demand


def wordset(text):
    return {K.stem(w) for w in K.words(text) if w not in K.STOP and not w.isdigit()}


def listing_text(path):
    """Title, bullets and backend terms from a pasted listing (Title:, Bullets:, Search terms: lines)."""
    if not path or not os.path.exists(path):
        return ""
    return open(path, encoding="utf-8").read()


def competitor_titles(path):
    if not path or not os.path.exists(path):
        return []
    return [m.strip() for m in re.findall(r"(?im)^\s*title\s*:\s*(.+)$", open(path, encoding="utf-8").read())]


def main():
    a = sys.argv[1:]
    opt = lambda k: a[a.index(k) + 1] if k in a else None  # noqa: E731
    if not opt("--terms"):
        sys.exit(__doc__)
    out = opt("--out") or "research"
    demand = read_terms(opt("--terms"))
    total = sum(demand.values()) or 1
    ranked = sorted(demand, key=demand.get, reverse=True)
    words = {}
    for t, w in demand.items():
        for s in wordset(t):
            words[s] = words.get(s, 0) + w
    spelled = {}
    for t in ranked:
        for w in K.words(t):
            spelled.setdefault(K.stem(w), w)
    current = wordset(listing_text(opt("--current")))
    covered = sum(w for t, w in demand.items() if wordset(t) <= current)
    comp = competitor_titles(opt("--competitors"))
    comp_words = {s for c in comp for s in wordset(c)}
    brand = (opt("--brand") or "").lower()
    res = {
        "terms": [{"term": t, "weight": round(demand[t], 1), "share": round(demand[t] / total, 4)} for t in ranked],
        "words": [{"word": spelled.get(s, s), "stem": s, "weight": round(w, 1)} for s, w in sorted(words.items(), key=lambda x: -x[1])
                  if s != K.stem(brand)],
        "title_lead": ranked[0],
        "current_coverage": round(covered / total, 4),
        "missing_now": [t for t in ranked if not wordset(t) <= current][:15],
        "competitor_words": sorted((spelled.get(s, s) for s in comp_words if s in words and s not in current), key=lambda w: -words[K.stem(w)]),
        "brand": opt("--brand") or "",
    }
    with open(out + ".json", "w", encoding="utf-8") as f:
        json.dump(res, f, indent=1, ensure_ascii=False)
    md = ["# Keyword research", "",
          "Demand weight = orders x 10 + clicks, from %s." % os.path.basename(opt("--terms")), "",
          "**Lead the title with:** %s (the top search; phones show about the first 80 characters)." % res["title_lead"], "",
          "## Searches that sell", "", "| Search | Share of demand |", "| --- | --- |"]
    md += ["| %s | %.1f%% |" % (t["term"], 100 * t["share"]) for t in res["terms"][:15]]
    md += ["", "## Words shoppers use, by demand", "", ", ".join(w["word"] for w in res["words"][:30]), "",
           "## Current listing", "", "Covers %.0f%% of demand. Searches it misses: %s." % (100 * res["current_coverage"], ", ".join(res["missing_now"][:10]) or "none"), ""]
    if comp:
        md += ["## Competitors", "", "Words competitors use that shoppers search and the current listing lacks: %s." % (", ".join(res["competitor_words"][:15]) or "none"), ""]
    md += ["Only use a word in the copy when the product facts support it."]
    with open(out + ".md", "w", encoding="utf-8") as f:
        f.write("\n".join(md) + "\n")
    print("wrote %s.json and %s.md: %d searches, top: %s; current listing covers %.0f%% of demand" % (
        out, out, len(ranked), ranked[0], 100 * res["current_coverage"]))


if __name__ == "__main__":
    main()
