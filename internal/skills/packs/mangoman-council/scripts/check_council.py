"""Check a council's decision before it reaches the owner.

Usage: python3 check_council.py <task folder>
The folder holds question.md, council/council.json and council.md (written by
council.py) and decision.md (written by the chair). Fails when the council
did not really happen (fewer than two voices answered), when council.md was
edited by hand, when decision.md misses a part, cites no voice for a voice
that answered, or uses a number found neither in question.md nor in the
voices' answers. Dates, durations ("within 14 days") and a worked result
shown after "=" ("(149 - 61) / 149 = 59%") are not facts to trace. Warns
when every voice came from one maker. Prints PASS, WARN or FAIL; exits 1 on
any FAIL.
"""
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import checks as C  # noqa: E402
import council as K  # noqa: E402

PARTS = ("## Decision", "## Why", "## Where the council disagreed", "## Risks", "## What would change this decision", "## Next steps")
MONTH = r"(jan|feb|mar|apr|may|jun|jul|aug|sep|sept|oct|nov|dec)[a-z]*\.?"
NOT_FACTS = re.compile(
    r"(?i)\b\d{1,2}(st|nd|rd|th)?\s+" + MONTH + r"(,?\s+\d{4})?\b"     # 15 January 2027
    r"|\b" + MONTH + r"\s+\d{1,2}(st|nd|rd|th)?(,?\s+\d{4})?\b"         # January 15, 2027
    r"|\b\d{4}-\d{2}-\d{2}\b"                                           # 2027-01-15
    r"|\b\d+\s+(minutes?|hours?|days?|weeks?|months?|years?)\b"          # within 14 days
    r"|\bQ[1-4]\b"                                                      # Q1
    r"|[=≈]\s*[^\s,;]+")                                                # a worked result
CITE = re.compile(r"(?i)\bvoices?\s+([A-E](?:\s*(?:,|and|&|/)\s*[A-E])*)\b")


def cited(text):
    """The voice letters a text cites: Voice A, voices A and C, Voices A, B and D."""
    return {x.upper() for m in CITE.finditer(text) for x in re.findall(r"[A-Ea-e]", m.group(1))}


def main():
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    folder = sys.argv[1]
    rep = C.Report()

    def read(*p):
        with open(os.path.join(folder, *p), encoding="utf-8", newline="") as f:
            return f.read()
    try:
        question = read("question.md")
        record = C.load_json(os.path.join(folder, K.OUT, K.OUT + ".json"))
    except OSError as e:
        rep.add("FAIL", "missing %s: write question.md, then run council.py" % os.path.basename(e.filename or ""))
        rep.finish()
    voices = record.get("voices") if isinstance(record, dict) else None
    if not isinstance(voices, list) or not all(isinstance(v, dict) and isinstance(v.get("rounds"), list) and v.get("label") for v in voices):
        rep.add("FAIL", "council.json is not what council.py writes: run council.py again")
        rep.finish()
    if record.get("done", record.get("rounds")) != record.get("rounds"):
        rep.add("FAIL", "the council stopped after round %s of %s: run council.py again to finish it" % (record.get("done"), record.get("rounds")))
    first = [v for v in voices if v["rounds"] and isinstance(v["rounds"][0], dict) and v["rounds"][0].get("ok")]
    rep.check([] if len(first) >= 2 else ["%d answered" % len(first)], "%d voices answered" % len(first),
              "a council needs at least two voices that answered")
    makers = {v.get("maker") for v in first}
    rep.check([] if len(makers) >= 2 else ["all from %s" % ", ".join(sorted(map(str, makers)))], "voices from %d makers" % len(makers),
              "every voice came from one maker, so they may share blind spots", "WARN")
    try:
        same = read(K.OUT, K.OUT + ".md") == K.markdown(record)
    except (OSError, KeyError, TypeError):
        same = False
    rep.add("PASS" if same else "FAIL", "council.md is what the voices said" if same else
            "council.md is missing or was edited by hand: run council.py again")
    try:
        decision = read("decision.md")
    except OSError:
        rep.add("FAIL", "decision.md not written")
        rep.finish()
    heads = {h.strip().lower() for h in re.findall(r"(?m)^##[^#].*$", decision)}
    rep.check([p for p in PARTS if p.lower() not in heads], "decision.md has every part", "decision.md lacks")
    rep.check([] if re.search(r"(?im)^\W*confidence\W*:\W*(high|medium|low)\b", decision) else ["no line"],
              "the confidence is stated", "say Confidence: high, medium or low")
    have = cited(decision)
    rep.check(["Voice %s" % v["label"] for v in first if v["label"] not in have],
              "every voice that answered is cited", "decision.md never cites")
    pool = C.fact_pool(question, [x.get("text", "") for v in voices for x in v["rounds"] if isinstance(x, dict)])
    body = re.sub(r"(?m)^#.*$", " ", decision)  # headings are not facts
    body = NOT_FACTS.sub(" ", CITE.sub(" ", body))
    rep.check(C.untraced(body, pool), "every number comes from question.md or the voices",
              "numbers found neither in question.md nor in the voices' answers (show the working for a computed one: (a - b) / a = c)")
    rep.finish()


if __name__ == "__main__":
    main()
