"""Fill an Amazon listing's backend search terms and score it against demand.

Usage: python3 optimise.py listing.json research.json [--facts facts.md] [--keep-terms]
Fills amazon.search_terms (249 bytes) with the highest-demand words not
already in the title, then the synonyms already written there, skipping
brands, competitor names, numbers, repeats and claim words (free, natural,
organic, pure...) the product facts do not use. Then reports how much search
demand the title, bullets and backend terms cover against the current
listing, which searches are still missed, and whether the top search leads
the title. Writes report.md.
"""
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_listing as B  # noqa: E402
import check_listing as K  # noqa: E402
from research import wordset  # noqa: E402

LIMIT = 249


def backend(words, title, exclude, facts):
    """Space-separated words in demand order, within the byte limit."""
    taken = wordset(title) | exclude
    allowed = wordset(facts)
    out = []
    for w in words:
        s = w["stem"]
        if s in taken or w["word"].isdigit() or K.PROMO.search(w["word"]) or (s in K.CLAIMS and s not in allowed):
            continue
        cand = " ".join(out + [w["word"]])
        if len(cand.encode("utf-8")) > LIMIT:
            continue
        out.append(w["word"])
        taken.add(s)
    return " ".join(out)


def coverage(terms, indexed):
    total = sum(t["weight"] for t in terms) or 1
    return sum(t["weight"] for t in terms if wordset(t["term"]) <= indexed) / total


def main():
    a = sys.argv[1:]
    if len(a) < 2:
        sys.exit(__doc__)
    spec = json.load(open(a[0], encoding="utf-8"))
    res = json.load(open(a[1], encoding="utf-8"))
    am = spec.get("amazon") or {}
    if not am.get("item_name"):
        sys.exit("listing.json has no amazon.item_name yet: write the copy first")
    title = B.amazon_title(am)
    names = [spec.get("brand", "")] + spec.get("competitors", [])
    exclude = {s for n in names for s in wordset(n)}
    facts = json.dumps(spec.get("facts", {}), ensure_ascii=False) + json.dumps(spec.get("product", {}), ensure_ascii=False)
    if "--facts" in a:
        facts += open(a[a.index("--facts") + 1], encoding="utf-8").read()
    if "--keep-terms" not in a:
        own = [{"word": w, "stem": K.stem(w)} for w in K.words(am.get("search_terms", ""))]
        am["search_terms"] = backend(res["words"] + own, title, exclude, facts)
        spec["amazon"] = am
        with open(a[0], "w", encoding="utf-8") as f:
            json.dump(spec, f, indent=1, ensure_ascii=False)
    indexed = wordset(" ".join([title, am.get("search_terms", "")] + list(am.get("bullets", []))))
    now = coverage(res["terms"], indexed)
    missed = [t["term"] for t in res["terms"] if not wordset(t["term"]) <= indexed][:10]
    lead_ok = wordset(res["title_lead"]) <= wordset(title[:80])
    lines = ["# Listing report", "",
             "| | Before | After |", "| --- | --- | --- |",
             "| Share of search demand covered | %.0f%% | %.0f%% |" % (100 * res["current_coverage"], 100 * now),
             "| Top search (%s) in the first 80 characters | | %s |" % (res["title_lead"], "yes" if lead_ok else "no"),
             "| Backend search terms | | %d of %d bytes |" % (len(am.get("search_terms", "").encode("utf-8")), LIMIT), "",
             "Searches still missed: %s." % (", ".join(missed) or "none"),
             "Add a missed search only when the product facts support it."]
    with open(os.path.join(os.path.dirname(os.path.abspath(a[0])), "report.md"), "w", encoding="utf-8") as f:
        f.write("\n".join(lines) + "\n")
    print("demand covered: %.0f%% (was %.0f%%); top search leads the title: %s; backend %d bytes; still missed: %s" % (
        100 * now, 100 * res["current_coverage"], "yes" if lead_ok else "NO", len(am.get("search_terms", "").encode("utf-8")), ", ".join(missed[:5]) or "none"))


if __name__ == "__main__":
    main()
