"""Fit Google search ads to the mined search terms and report the gain.

Usage: python3 fit.py ads.json mining.json [--facts facts.md]
For every Google ad group in ads.json: adds the mined keywords (each to the
ad group whose copy shares most words with it), adds the mined negatives,
drops your own negatives that would block a search worth keeping or a
keyword, and fills free headline slots (up to 15) with the top searches no
headline covers yet, when the search fits in 30 characters, needs no claim or
number the facts do not give and is not a near copy of another headline.
Then writes report.md: demand covered by headlines before and after, waste
blocked, negatives blocking a converting search (must be 0), headline counts
and claim words in the ads that the facts do not make.
"""
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_ads as B  # noqa: E402
import check_ads as K  # noqa: E402
import checks as C  # noqa: E402
from mine import CLAIMS, known_terms, terms, words  # noqa: E402

MAX = 15
SMALL = set("a an and as at by for in of on or the to with".split())  # kept lower case in headlines


def coverage(groups, wanted):
    heads = [terms(h) for g in groups for h in g["headlines"]]
    total = sum(s["weight"] for s in wanted) or 1
    return sum(s["weight"] for s in wanted if any(terms(s["term"]) <= h for h in heads)) / total


def alike(a, b):
    """Near copies, as the test set counts them: 60% of the words shared."""
    return len(a & b) >= 0.6 * len(a | b)


def headline(term, caps):
    out = [w.upper() if w.upper() in caps else w if (i and w in SMALL) else w[:1].upper() + w[1:] for i, w in enumerate(words(term))]
    return " ".join(out)


def main():
    a = sys.argv[1:]
    if len(a) < 2:
        sys.exit(__doc__)
    spec = json.load(open(a[0], encoding="utf-8"))
    mining = json.load(open(a[1], encoding="utf-8"))
    groups = (spec.get("google") or {}).get("ad_groups") or []
    if not groups or not all(g.get("headlines") for g in groups):
        sys.exit("ads.json has no Google ad groups with headlines yet: write the copy first")
    facts = "\n".join(str(v) for v in spec.get("facts", {}).values())
    if "--facts" in a:
        facts += "\n" + open(a[a.index("--facts") + 1], encoding="utf-8").read()
    known = set(mining["known"]) | known_terms(facts)
    pool = C.fact_pool(spec.get("facts", {}))
    caps = set(spec.get("allowed_caps", []))
    wanted = mining["wanted"]
    keep = [s["term"] for s in mining["searches"] if not s["waste"]]
    converting = [s["term"] for s in mining["searches"] if s.get("conversions")]
    before = coverage(groups, wanted)
    heads_before = [len(g["headlines"]) for g in groups]

    copy = lambda g: terms(" ".join([g["name"]] + g["headlines"] + [k["text"] for k in g.get("keywords", [])]))  # noqa: E731
    home = {}
    for kw in (k for m in mining["groups"] for k in m["keywords"]):
        g = max(groups, key=lambda g: len(terms(kw["text"]) & copy(g)))
        home[kw["text"]] = g
        if kw["text"] not in {k["text"].lower() for k in g.get("keywords", [])}:
            g.setdefault("keywords", []).append(dict(kw))
    dropped = []
    for g in groups:
        own = [k["text"] for k in g.get("keywords", [])]
        negs = list(dict.fromkeys(g.get("negatives", []) + mining["negatives"]))
        bad = [n for n in negs if any(K.blocks(n, t) for t in keep + own)]
        dropped += bad
        g["negatives"] = [n for n in negs if n not in bad]

    added = []
    for s in wanted:
        if any(terms(s["term"]) <= terms(h) for g in groups for h in g["headlines"]):
            continue
        g = home.get(s["term"]) or groups[0]
        h = headline(s["term"], caps)
        t = terms(h)
        if (len(g["headlines"]) < MAX and B.glen(h) <= B.GOOGLE["headline"] and not (t & CLAIMS - known)
                and not C.untraced(h, pool) and not C.RISKY.search(h) and not any(alike(t, terms(x)) for x in g["headlines"])):
            g["headlines"].append(h)
            added.append(h)
    with open(a[0], "w", encoding="utf-8") as f:
        json.dump(spec, f, indent=1, ensure_ascii=False)

    after = coverage(groups, wanted)
    waste = mining["waste"]
    total_waste = sum(w["waste"] for w in waste) or 1
    stopped = sum(w["waste"] for w in waste if all(any(K.blocks(n, w["term"]) for n in g["negatives"]) for g in groups))
    lost = [t for t in converting if any(K.blocks(n, t) for g in groups for n in g["negatives"])]
    missed = [s["term"] for s in wanted if not any(terms(s["term"]) <= terms(h) for g in groups for h in g["headlines"])]
    text = " ".join(x for g in groups for x in g["headlines"] + g["descriptions"])
    unbacked = sorted(terms(text) & CLAIMS - known)
    long_heads = [h for g in groups for h in g["headlines"] if B.glen(h) > B.GOOGLE["headline"]]
    lines = ["# Google ads report", "",
             "| | Before | After |", "| --- | --- | --- |",
             "| Search demand covered by a headline | %.0f%% | %.0f%% |" % (100 * before, 100 * after),
             "| Headlines per ad group (15 at most) | %s | %s |" % (", ".join(map(str, heads_before)), ", ".join(str(len(g["headlines"])) for g in groups)),
             "| Wasted %s blocked in every ad group | | %.0f%% |" % ("spend" if "conversions" in mining["demand"] else "searches", 100 * stopped / total_waste),
             "| Negatives that block a converting search | | %d |" % len(lost), "",
             "Headlines added: %s." % (", ".join(added) or "none"),
             "Searches no headline covers yet: %s." % (", ".join(missed) or "none"),
             "Your negatives removed because they blocked a search worth keeping: %s." % (", ".join(dropped) or "none"),
             "Claim words in the ads that the facts do not make: %s." % (", ".join(unbacked) or "none"),
             "Headlines over 30 characters: %s." % (", ".join(long_heads) or "none"),
             "Work a missed search into a headline only when the facts support it."]
    with open(os.path.join(os.path.dirname(os.path.abspath(a[0])), "report.md"), "w", encoding="utf-8") as f:
        f.write("\n".join(lines) + "\n")
    print("demand covered by headlines: %.0f%% (was %.0f%%); waste blocked: %.0f%%; converting searches blocked: %d; headlines added: %d; claims not in facts: %s" % (
        100 * after, 100 * before, 100 * stopped / total_waste, len(lost), len(added), ", ".join(unbacked) or "none"))


if __name__ == "__main__":
    main()
