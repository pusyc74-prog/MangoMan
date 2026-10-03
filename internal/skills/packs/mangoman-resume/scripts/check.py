"""Check a built resume before delivering it.

Usage: python3 check.py resume.json resume.pdf [--pages 1]
Checks: page count; the page is well filled (not half empty); text in the
PDF is selectable (applicant tracking systems read it); required sections are
filled; bullets start with an action, carry results and avoid "I"; dates use
one format. Prints PASS, WARN or FAIL lines; exits 1 on any FAIL.
"""
import json
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import checks as C  # noqa: E402
import render  # noqa: E402

WEAK_STARTS = ("responsible for", "worked on", "helped", "assisted", "involved in", "tasked with", "duties included", "in charge of")
DATE_STYLES = [("Mon YYYY", re.compile(r"^[A-Z][a-z]{2} \d{4}$")), ("Month YYYY", re.compile(r"^[A-Z][a-z]{3,8} \d{4}$")),
               ("MM/YYYY", re.compile(r"^\d{2}/\d{4}$")), ("YYYY", re.compile(r"^\d{4}$")), ("YYYY-MM", re.compile(r"^\d{4}-\d{2}$"))]
FIRST_PERSON = re.compile(r"\bI\b|\b(?i:my|me|mine|myself)\b")
PRESENT = {"present", "current", "now", "today"}


def date_style(s):
    s = str(s or "").strip()
    if s.lower() in PRESENT or not s:
        return None
    for name, rx in DATE_STYLES:
        if rx.match(s):
            return name
    return "other"


def main():
    a = sys.argv[1:]
    if len(a) < 2:
        sys.exit(__doc__)
    with open(a[0], encoding="utf-8") as f:
        r = json.load(f)
    pdf = a[1]
    want = int(a[a.index("--pages") + 1]) if "--pages" in a else 1
    rs = []

    def res(level, msg):
        rs.append((level, msg))

    pages = render.pdf_pages(pdf)
    res("PASS" if pages == want else "FAIL", "%d page%s as planned" % (pages, "" if pages == 1 else "s") if pages == want else
        "%d pages, target %d: tighten wording, drop the oldest or weakest bullets, or reduce the summary" % (pages, want))
    text = render.pdf_text(pdf)
    if text:
        ok = (r.get("name", "").split() or ["?"])[0].lower() in text.lower()
        res("PASS" if ok else "FAIL", "text is selectable (applicant tracking systems can read it)" if ok else "PDF text is not readable")
        # Page fill: estimate by text lines on the last page.
        try:
            from pypdf import PdfReader
            last = PdfReader(pdf).pages[-1].extract_text() or ""
            lines = [l for l in last.splitlines() if l.strip()]
            full = 52 if r.get("page_size", "A4") == "A4" else 48
            ratio = len(lines) / full
            if pages == want:
                res("PASS" if ratio >= 0.6 else "WARN", "the page is well filled" if ratio >= 0.6 else
                    "the last page looks about %d%% full: add results to bullets or a projects section" % int(ratio * 100))
        except Exception:
            pass
    else:
        res("WARN", "could not read the PDF text to verify it is selectable")

    missing = []
    c = r.get("contact", {})
    if not r.get("name"):
        missing.append("name")
    if not (c.get("email") or c.get("phone")):
        missing.append("an email or phone")
    if not (r.get("experience") or r.get("projects")):
        missing.append("experience or projects")
    if not r.get("skills"):
        missing.append("skills")
    for x in r.get("experience", []):
        if not x.get("bullets"):
            missing.append("bullets for %s" % x.get("role", "a role"))
        if not x.get("start"):
            missing.append("dates for %s" % x.get("role", "a role"))
    res("PASS" if not missing else "FAIL", "all sections filled" if not missing else "missing: " + ", ".join(missing))

    bl = [b for x in r.get("experience", []) for b in x.get("bullets", [])]
    weak = [b for b in bl if b.lower().startswith(WEAK_STARTS)]
    first = [b for b in bl + [r.get("summary", "")] if FIRST_PERSON.search(b or "")]
    longb = [b for b in bl if len(b.split()) > 30]
    nums = [b for b in bl if re.search(r"\d", b)]
    res("PASS" if not weak else "FAIL", "bullets start with an action" if not weak else
        "weak openings (start with what you did, e.g. Led, Built, Cut): " + "; ".join(w[:50] for w in weak[:3]))
    res("PASS" if not first else "FAIL", "no first person" if not first else "remove I / my / me: " + "; ".join(f[:50] for f in first[:3]))
    res("PASS" if not longb else "WARN", "bullets are concise" if not longb else "%d bullets over 30 words; split or trim" % len(longb))
    if bl:
        share = len(nums) / len(bl)
        res("PASS" if share >= 0.5 else "WARN", "%d%% of bullets show a measurable result" % int(share * 100) if share >= 0.5 else
            "only %d%% of bullets show a number; ask the user for results (%%, ₹, time saved, users) rather than inventing them" % int(share * 100))
    styles = {date_style(x.get(k)) for x in r.get("experience", []) for k in ("start", "end")} - {None}
    res("PASS" if len(styles) <= 1 else "WARN", "dates use one format" if len(styles) <= 1 else "dates mix formats: %s" % ", ".join(sorted(styles)))
    if r.get("cover_letter"):
        check_letter(r, pdf.replace(".pdf", "-letter.pdf"), res)
    for level, msg in rs:
        print("%s  %s" % (level, msg))
    sys.exit(1 if any(l == "FAIL" for l, _ in rs) else 0)


def check_letter(r, pdf, res):
    """Cover letter: one page, short, about this job, every number from the resume."""
    c = r["cover_letter"]
    text = " ".join(c.get("paragraphs", []))
    n = len(text.split())
    res("PASS" if c.get("company") and c.get("role") else "FAIL", "letter names the company and role" if c.get("company") and c.get("role") else
        "cover_letter needs company and role")
    if c.get("company") and c["company"].lower() not in text.lower():
        res("WARN", "the letter never mentions %s; say why this company" % c["company"])
    res("PASS" if 200 <= n <= 400 else ("FAIL" if n > 450 else "WARN"), "letter is %d words" % n if 200 <= n <= 400 else
        "letter is %d words; aim for 200 to 400" % n)
    pool = C.fact_pool({k: v for k, v in r.items() if k != "cover_letter"})
    bad = sorted({u for p in c.get("paragraphs", []) for u in C.untraced(p, pool)})
    res("PASS" if not bad else "FAIL", "every number in the letter is in the resume" if not bad else
        "numbers in the letter that the resume does not support: " + ", ".join(bad))
    if re.match(r"\s*(i am writing to|to whom it may concern)", text, re.I) or re.search(r"to whom it may concern", c.get("greeting", ""), re.I):
        res("WARN", "open with why you fit this role, not 'I am writing to apply' or 'To whom it may concern'")
    ph = C.first_match(C.PLACEHOLDER, c.get("paragraphs", []) + [c.get("greeting", "")])
    res("PASS" if not ph else "FAIL", "no placeholder text in the letter" if not ph else "placeholder text in the letter: " + ", ".join(ph))
    if not os.path.exists(pdf):
        res("WARN", "no letter PDF to measure (no browser engine)")
    else:
        pages = render.pdf_pages(pdf)
        res("PASS" if pages == 1 else "FAIL", "letter fits one page" if pages == 1 else "letter runs to %d pages; cut it to one" % pages)


if __name__ == "__main__":
    main()
