"""Check a built client report before sending it.

Usage: python3 check_report.py analysis.py report
Checks: re-running the analysis gives the same numbers; the spec is valid;
headlines are short takeaways; every number in the writing traces to a
computed value or a fact; there is a plan for next month; the PDF has every
section and a sensible length; no placeholder text.
Prints PASS, WARN or FAIL; exits 1 on any FAIL.
"""
import json
import os
import re
import sys
import unicodedata

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_report as B  # noqa: E402
import checks as C  # noqa: E402
import render  # noqa: E402
import vizlib as V  # noqa: E402

TEXT_KEYS = ("summary", "headline", "body", "wins", "issues", "title", "action")


def main():
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    script, out = sys.argv[1], sys.argv[2]
    rep = C.Report()
    spec = json.load(open(out + ".spec.json", encoding="utf-8"))
    rep.check([] if B.run_analysis(script) == spec else ["rebuild"], "analysis reproduces the report's numbers", "re-running the analysis gives different numbers")
    probs = B.validate(spec, os.path.dirname(os.path.abspath(script)))
    rep.check(probs, "spec is valid", "spec problems")
    heads = [s["headline"] for s in spec.get("sections", [])]
    rep.check([h for h in heads if len(h.split()) > 16], "headlines are short", "headlines over 16 words")
    rep.check([h for h in heads if len(h.split()) < 4], "headlines state takeaways", "headlines that read like labels (write the takeaway as a sentence)", "WARN")

    def texts(o, key=""):
        if isinstance(o, str):
            return [o] if key in TEXT_KEYS else []
        if isinstance(o, dict):
            return [t for k, v in o.items() if k not in ("facts", "chart", "table", "kpis") for t in texts(v, k)]
        if isinstance(o, list):
            return [t for v in o for t in texts(v, key)]
        return []
    # allowed numbers: facts, computed values, and numbers inside the data's own labels (table rows, chart categories)
    data_labels = [(s.get("table") or {}).get("rows", []) for s in spec.get("sections", [])] + [(s.get("chart") or {}).get("x", []) for s in spec.get("sections", [])]
    pool = C.fact_pool(spec.get("facts", {}), data_labels) + V.numbers_in({k: v for k, v in spec.items() if k not in TEXT_KEYS})
    allt = texts(spec)
    rep.check(sorted({u for t in allt for u in C.untraced(t, pool)}), "every number in the writing traces to computed data or facts",
              "numbers not in the computed data (add them to facts or fix the text)")
    ns = len(spec.get("sections", []))
    rep.check([] if 3 <= ns <= 6 else ["%d sections" % ns], "%d sections" % ns, "aim for 3 to 6 sections", "WARN")
    rep.check([] if spec.get("next") else ["none"], "plan for next month included", "no plan for next month: say what happens now", "WARN")
    rep.check(C.first_match(C.PLACEHOLDER, allt), "no placeholder text", "placeholder text left in")
    pdf = out + ".pdf"
    if os.path.exists(pdf):
        flat = lambda s: re.sub(r"\s+", "", unicodedata.normalize("NFKC", s)).lower()  # PDF text splits words and uses ligatures
        txt = flat(render.pdf_text(pdf))
        missing = [h for h in heads if flat(h)[:30] not in txt]
        latin = [h for h in missing if all(unicodedata.name(ch, "").startswith("LATIN") for ch in h if ch.isalpha())]
        rep.check(latin, "every section is in the PDF", "sections missing from the PDF")
        if missing != latin:  # extraction of complex scripts is unreliable, so only ask for a look
            rep.add("WARN", "could not confirm these sections in the PDF text; look at the PDF: " + "; ".join(h for h in missing if h not in latin))
        n = render.pdf_pages(pdf)
        rep.check([] if n <= 6 else ["%d pages" % n], "%d pages" % n, "long for a monthly report; move detail to an appendix", "WARN")
    else:
        rep.add("WARN", "no PDF (no browser engine)")
    rep.finish()


if __name__ == "__main__":
    main()
