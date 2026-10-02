"""Check a built deck before delivering it.

Usage: python3 check_deck.py analysis.py deck
Checks: the analysis reproduces the same numbers; the story structure (title,
then the answer, then evidence, ending in next steps); headline and bullet
lengths; every number in the text traces to a computed value; no text
overflows a slide; the PDF has one page per slide; the PowerPoint exists.
Prints PASS, WARN or FAIL lines; exits 1 on any FAIL.
"""
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_deck as B  # noqa: E402
import tracenum  # noqa: E402
import vizlib as V  # noqa: E402
import render  # noqa: E402

MAX_HEADLINE_WORDS = 16
MAX_BULLETS = 5
MAX_BULLET_WORDS = 16


def words(s):
    return len(str(s).split())


def main():
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    script, out = sys.argv[1], sys.argv[2]
    rs = []

    def r(level, msg):
        rs.append((level, msg))

    with open(out + ".spec.json", encoding="utf-8") as f:
        spec = json.load(f)
    again = B.run_analysis(script)
    r("PASS" if again == spec else "FAIL", "analysis reproduces the deck's numbers" if again == spec else
      "re-running the analysis gives different numbers: rebuild the deck from analysis.py")
    probs = B.validate(spec)
    r("PASS" if not probs else "FAIL", "spec is valid" if not probs else "; ".join(probs))

    slides = spec.get("slides", [])
    types = [s.get("type") for s in slides]
    r("PASS" if types[:1] == ["title"] else "FAIL", "opens with a title slide" if types[:1] == ["title"] else "slide 1 must be the title slide")
    ans_ok = len(types) > 1 and types[1] in ("answer", "kpis", "stat")
    r("PASS" if ans_ok else "FAIL", "the answer comes first (slide 2)" if ans_ok else
      "slide 2 must give the answer (type answer, kpis or stat): executives read the conclusion first")
    r("PASS" if "next_steps" in types else "WARN", "ends with next steps" if "next_steps" in types else "no next-steps slide: say what should happen now")
    n = len(slides)
    r("PASS" if 5 <= n <= 15 else "WARN", "%d slides" % n + ("" if 5 <= n <= 15 else ": aim for 5 to 15; move detail to an appendix"))

    long_heads = [(i, s["headline"]) for i, s in enumerate(slides, 1) if s.get("headline") and words(s["headline"]) > MAX_HEADLINE_WORDS]
    r("PASS" if not long_heads else "FAIL", "headlines are short sentences" if not long_heads else
      "headlines over %d words: %s" % (MAX_HEADLINE_WORDS, "; ".join("slide %d (%d words)" % (i, words(h)) for i, h in long_heads)))
    bl = []
    for i, s in enumerate(slides, 1):
        items = s.get("bullets", []) + [B.point_text(p) for p in s.get("points", [])]
        if len(s.get("bullets", [])) > MAX_BULLETS or len(s.get("points", [])) > 3:
            bl.append("slide %d has too many points (bullets at most %d, answer points at most 3)" % (i, MAX_BULLETS))
        bl += ["slide %d bullet over %d words" % (i, MAX_BULLET_WORDS) for b in items if words(b) > MAX_BULLET_WORDS]
    r("PASS" if not bl else "FAIL", "bullets are tight" if not bl else "; ".join(bl))
    noverb = [i for i, s in enumerate(slides, 1) if s.get("type") not in ("title", "section") and s.get("headline") and words(s["headline"]) < 4]
    r("PASS" if not noverb else "WARN", "headlines state takeaways" if not noverb else
      "slides %s have topic labels as headlines; write the takeaway as a sentence" % ", ".join(map(str, noverb)))

    computed = V.numbers_in({k: v for k, v in spec.items() if k != "slides"}) + V.numbers_in(
        [{k: v for k, v in s.items() if k not in ("headline", "points", "bullets", "takeaway", "title", "subtitle", "note")} for s in slides])
    bad = []
    for i, s in enumerate(slides, 1):
        pts = [B.point_text(p) + " " + str(p.get("stat", "") if isinstance(p, dict) else "") for p in s.get("points", [])]
        for txt in [s.get("headline", ""), s.get("takeaway", ""), s.get("note", "")] + pts + s.get("bullets", []):
            u = tracenum.untraced(txt, computed)
            if u:
                bad.append("slide %d: %s" % (i, ", ".join(u)))
    r("PASS" if not bad else "FAIL", "every number in the text traces to a computed value" if not bad else
      "numbers not found in the computed data (add them to facts or fix the text): " + "; ".join(bad))

    if os.path.exists(out + ".html"):
        ov = render.overflow(out + ".html", "[data-check]", 1400, 900)
        if ov is None:
            r("WARN", "overflow not measured (no Playwright); look at every slide in the PDF")
        else:
            r("PASS" if not ov else "FAIL", "no text overflows its slide" if not ov else
              "content overflows: " + ", ".join("%s (+%dpx)" % (o["id"], o["extra_px"]) for o in ov))
    if os.path.exists(out + ".pdf"):
        pages = render.pdf_pages(out + ".pdf")
        r("PASS" if pages == n else "FAIL", "PDF has one page per slide" if pages == n else "PDF has %d pages for %d slides" % (pages, n))
    else:
        r("WARN", "no PDF (no browser engine)")
    r("PASS" if os.path.exists(out + ".pptx") else "WARN", "PowerPoint file built" if os.path.exists(out + ".pptx") else
      "no PowerPoint file: pip install python-pptx and rebuild")
    for level, msg in rs:
        print("%s  %s" % (level, msg))
    sys.exit(1 if any(l == "FAIL" for l, _ in rs) else 0)


if __name__ == "__main__":
    main()
