"""Check a strategy report before handing it over.

Usage: python3 check_report.py report.json report
Checks: every part the kind of report needs is there and holds its block;
research parts cite listed web sources (or the report says plainly it was
researched without the web); every citation points at a source; every number
in the writing comes from the user's facts or a source's findings (targets,
dates and pillar shares excepted); pillars add up to 100%; the calendar uses
real dates and every pillar; targets say how they are measured; no
placeholder text, risky promises or machine-sounding phrases; the files
were built. Prints PASS, WARN or FAIL; exits 1 on any FAIL.
"""
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_report as B  # noqa: E402
import checks as C  # noqa: E402
import render  # noqa: E402

RESEARCH = ("research", "competitors", "audience", "market", "keywords", "audit")
AI_TELLS = re.compile(r"\bit'?s not (?:just )?\w+[^.]{0,40}, it'?s\b|\bunlock the power\b|\btake it to the next level\b|\bsay goodbye to\b|"
                      r"\bin today'?s (?:fast-paced|digital)\b|\bgame[- ]changer\b|\bleverage synergies\b|\bdelve\b|\bhere'?s the thing\b", re.I)
# Words that are the plan's own, not claims about the world: numbers in them are recommendations.
OWN = ("target", "when", "share", "date", "period")


def texts(o, key=""):
    """(key, text) for every string the reader sees, with the key it sits under."""
    if isinstance(o, str):
        yield key, o
    elif isinstance(o, dict):
        for k, v in o.items():
            if k not in ("facts", "brand", "sources", "url", "id", "type", "kind", "logo", "language", "total", "currency"):
                yield from texts(v, k)
    elif isinstance(o, list):
        for v in o:
            yield from texts(v, key)


def main():
    if len(sys.argv) < 3:
        print(__doc__)
        sys.exit(2)
    out = sys.argv[2]
    R = C.Report()
    spec = C.load_json(sys.argv[1])
    kind = spec.get("kind")
    if kind not in B.KINDS:
        R.add("FAIL", "kind must be one of %s" % ", ".join(B.KINDS))
        R.finish()
    secs = {s.get("id"): s for s in spec.get("sections", [])}
    need = B.KINDS[kind][1]
    R.check([s for s in need if s not in secs], "every part of a %s report is there" % kind, "parts missing (add a section with this id)")
    empty = [i for i, s in secs.items() if not any(b.get("type") in B.BLOCKS for b in s.get("blocks", []))]
    R.check(empty, "every part has content", "parts with no block of a known type")
    R.check(["%s needs a %s block" % (i, t) for i, t in B.NEEDS.items() if i in secs and not any(b.get("type") == t for b in secs[i].get("blocks", []))],
            "each part holds what it is for", "a part lacks its block")
    R.check([] if 3 <= len(spec.get("summary", [])) <= 6 else ["has %d points" % len(spec.get("summary", []))], "the summary has 3 to 6 points", "the summary needs 3 to 6 points")

    # sources and citations
    srcs = spec.get("sources", [])
    ids = [s.get("id") for s in srcs]
    bad_src = [str(s.get("id")) for s in srcs if not re.match(r"^s\d+$", str(s.get("id"))) or not str(s.get("url", "")).startswith(("https://", "http://")) or not s.get("found") or not s.get("title")]
    R.check(bad_src + ["%s twice" % i for i in set(ids) if ids.count(i) > 1], "sources have an id, a title, a link and what was found there", "sources incomplete")
    allt = " ".join(t for _, t in texts(spec))
    R.check(sorted(set(B.CITE.findall(allt)) - set(ids)), "every citation points at a listed source", "citations to sources that are not listed")
    uncited = [i for i in RESEARCH if i in secs and not B.CITE.search(" ".join(t for _, t in texts(secs[i])))]
    if spec.get("no_web"):
        R.add("WARN", "researched without the web: %s (the report says so on top)" % spec["no_web"])
    else:
        R.check(uncited, "research parts cite their web sources", "research parts with no citation (cite [s1] and so on, or set no_web with the reason)")

    # numbers: facts, findings, or the plan's own targets and dates
    pool = C.fact_pool(spec.get("facts", {}), [s.get("found", "") for s in srcs])
    loose = []
    for k, t in texts({"summary": spec.get("summary", []), "sections": spec.get("sections", [])}):
        if k in OWN or k == "date":
            continue
        loose += C.untraced(B.CITE.sub(" ", t), pool)
    R.check(loose, "every number comes from your facts or a source", "numbers not in the facts or any source's findings (add the fact, cite a source that says it, or remove it)")

    # pillars and calendar
    pillars = [p for s in secs.values() for b in s.get("blocks", []) if b.get("type") == "pillars" for p in b.get("items", [])]
    if pillars:
        total = sum(float(p.get("share", 0) or 0) for p in pillars)
        R.check([] if 3 <= len(pillars) <= 6 else ["%d pillars" % len(pillars)], "3 to 6 content pillars", "use 3 to 6 content pillars")
        R.check([] if abs(total - 100) <= 1 else ["they add to %g%%" % total], "pillar shares add up to 100%", "pillar shares must add up to 100%")
        R.check([p.get("name", "?") for p in pillars if len(p.get("ideas", [])) < 2], "every pillar has post ideas", "pillars with fewer than 2 ideas")
    cal = B.calendar_rows(spec)
    if cal:
        dates = [B.day(p.get("date")) for p in cal]
        R.check([str(p.get("date")) for p, d in zip(cal, dates) if not d], "calendar dates are real dates", "calendar dates must be YYYY-MM-DD")
        good = sorted(d for d in dates if d)
        if good:
            R.check([] if (good[-1] - good[0]).days <= 92 else ["spans %d days" % (good[-1] - good[0]).days], "the calendar covers up to 3 months", "keep the calendar to 3 months")
        names = {p.get("name") for p in pillars}
        if names:
            R.check(sorted({str(p.get("pillar")) for p in cal} - names), "every post belongs to a pillar", "posts with a pillar that is not in the pillars")
            R.check(sorted(names - {p.get("pillar") for p in cal}), "every pillar is in the calendar", "pillars with no post", level="WARN")
        R.check([str(p.get("date")) for p in cal if not p.get("idea") or not p.get("platform")], "every post says where and what", "posts without a platform or an idea")
    for b in (b for s in secs.values() for b in s.get("blocks", []) if b.get("type") == "budget"):
        total = sum(float(x.get("share", 0) or 0) for x in b.get("items", []))
        R.check([] if abs(total - 100) <= 1 else ["they add to %g%%" % total], "budget shares add up to 100%", "budget shares must add up to 100%")
        R.check(C.untraced(str(b.get("total", "")), pool), "the budget total is the one you gave", "the budget total must be a fact the user gave (put it in facts)")
    kpis = [k for s in secs.values() for b in s.get("blocks", []) if b.get("type") == "kpis" for k in b.get("items", [])]
    R.check([k.get("metric", "?") for k in kpis if not k.get("target") or not k.get("how")], "every target says how it is measured", "targets without a target or a way to measure")

    # writing
    R.check(C.first_match(C.PLACEHOLDER, [allt]), "no placeholder text", "placeholder text left in")
    R.check(C.first_match(C.RISKY, [allt]), "no risky promises", "risky promises (keep only what the facts prove)", level="WARN")
    R.check(C.first_match(AI_TELLS, [allt]) + (["em dashes"] if "—" in allt else []), "no machine-sounding phrases", "phrases that read as machine-written", level="WARN")

    # files
    R.check([f for f in (out + ".html", out + ".md") if not os.path.exists(f)], "the report files were built", "files missing: run build_report.py")
    if os.path.exists(out + ".pdf"):
        R.check([] if render.pdf_pages(out + ".pdf") else ["the PDF has no pages"], "the PDF opens", "the PDF is broken")
    else:
        R.add("WARN", "no PDF (no browser found): the HTML report is complete; open it and print to PDF")
    R.finish()


if __name__ == "__main__":
    main()
