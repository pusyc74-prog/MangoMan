"""Check brand guidelines before sharing them.

Usage: python3 check_brand.py brand.json brand
Checks: the spec is valid; every text and background pair the packs use is
readable (WCAG AA contrast); extra colours are not too close to the core
ones; the logo is large enough; the voice section is complete; exports and
the PDF exist with every page. Prints PASS, WARN or FAIL; exits 1 on FAIL.
"""
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import brandkit as BK  # noqa: E402
import build_brand as B  # noqa: E402
import checks as C  # noqa: E402
import render  # noqa: E402


def main():
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    src, out = sys.argv[1], sys.argv[2]
    spec = B.load(src)
    bdir = os.path.dirname(os.path.abspath(src))
    rep = C.Report()
    probs = B.validate(spec, bdir)
    rep.check(probs, "spec is valid", "spec problems")
    if probs:
        rep.finish()
    T, _ = B.tokens(spec, bdir)
    bad = ["%s %.1f:1 (needs %.1f)" % (lab, BK.contrast(fg, bg), need) for lab, fg, bg, need in B.pairs(T) if BK.contrast(fg, bg) < need]
    rep.check(bad, "every text and background pair is readable (WCAG AA)", "combinations that are hard to read")
    core = [T[k] for k in ("dark", "accent_fill", "text", "tint")]
    close = [c["name"] for c in spec.get("extra_colors", []) if any(BK.contrast(BK.hexc(c["hex"]), k) < 1.15 for k in core)]
    rep.check(close, "extra colours are distinct from the core palette", "extra colours almost identical to a core colour", "WARN", ", ")
    if spec.get("logo"):
        w, h = BK.logo_size(os.path.join(bdir, spec["logo"]))
        rep.check([] if max(w, h) >= 500 else ["%dx%d px" % (w, h)], "logo file is large enough (%dx%d px)" % (w, h),
                  "logo is small; ask for a PNG at least 1,000 px wide (or the original vector)", "WARN")
    else:
        rep.add("WARN", "no logo: the guide shows the name as a wordmark")
    v = spec.get("voice", {})
    rep.check([k for k in ("personality", "we_are", "we_are_not", "do", "dont", "sample") if not v.get(k)], "voice section complete",
              "voice section missing", "WARN", ", ")
    texts = [spec.get("tagline", "")] + [x for k in ("personality", "we_are", "we_are_not", "do", "dont") for x in v.get(k, [])] + list((v.get("sample") or {}).values())
    rep.check(C.first_match(C.PLACEHOLDER, texts), "no placeholder text", "placeholder text left in")
    for f in (out + "-tokens.json", out + ".css"):
        if not os.path.exists(f):
            rep.add("FAIL", "%s not built" % f)
    if os.path.exists(out + "-tokens.json"):
        blk = json.load(open(out + "-tokens.json", encoding="utf-8")).get("use_in_packs", {})
        rep.check([] if blk.get("brand", {}).get("primary") else ["missing"], "brand block ready for every pack", "tokens file has no use_in_packs block")
    pdf = out + "-guide.pdf"
    if os.path.exists(pdf):
        n = render.pdf_pages(pdf)
        rep.check([] if n == 5 else ["%d pages" % n], "guide has its 5 pages", "guide should have 5 pages (cover, logo, colour, type, voice)")
    else:
        rep.add("WARN", "no PDF (no browser engine)")
    rep.finish()


if __name__ == "__main__":
    main()
