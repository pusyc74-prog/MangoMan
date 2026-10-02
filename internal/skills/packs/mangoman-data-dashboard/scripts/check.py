"""Check a built dashboard before delivering it.

Usage: python3 check.py analysis.py dashboard
  (dashboard = the --out name used with build_dashboard.py)
Checks: the analysis reproduces the same numbers; the spec is valid; every
number written in the headline, titles and notes traces back to a computed
value; the page renders. Prints PASS or FAIL lines; exits 1 on any FAIL.
"""
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_dashboard as B  # noqa: E402
import tracenum as trace  # noqa: E402
import vizlib as V  # noqa: E402
import render  # noqa: E402


def main():
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    script, out = sys.argv[1], sys.argv[2]
    results = []

    def res(ok, msg):
        results.append(("PASS" if ok else "FAIL", msg))

    with open(out + ".spec.json", encoding="utf-8") as f:
        spec = json.load(f)
    again = B.run_analysis(script)
    res(again == spec, "analysis reproduces the dashboard's numbers" if again == spec else
        "re-running the analysis gives different numbers: the dashboard must be rebuilt from analysis.py")
    probs = B.validate(spec)
    res(not probs, "spec is valid" if not probs else "spec problems: " + "; ".join(probs))
    computed = V.numbers_in({k: v for k, v in spec.items() if k not in ("title", "subtitle", "headline", "notes", "period", "source")})
    texts = [("headline", spec.get("headline", ""))] + [("chart title", c.get("title", "")) for c in spec.get("charts", [])] + \
        [("note", n) for n in spec.get("notes", [])] + [("chart note", c.get("note", "")) for c in spec.get("charts", [])]
    bad = [(w, t, trace.untraced(t, computed)) for w, t in texts]
    bad = [b for b in bad if b[2]]
    res(not bad, "every number in the text traces to a computed value" if not bad else
        "numbers in text not found in the computed data: " + "; ".join("%s %r: %s" % (w, t[:60], ", ".join(n)) for w, t, n in bad))
    has_html = os.path.exists(out + ".html")
    res(has_html, "dashboard page exists" if has_html else "dashboard page missing: run build_dashboard.py")
    if has_html and render.engine():
        ok = render.screenshot(out + ".html", out + ".png", 1280, 900)
        res(ok, "page renders (screenshot %s.png: look at it before delivering)" % out)
    for status, msg in results:
        print("%s  %s" % (status, msg))
    sys.exit(0 if all(s == "PASS" for s, _ in results) else 1)


if __name__ == "__main__":
    main()
