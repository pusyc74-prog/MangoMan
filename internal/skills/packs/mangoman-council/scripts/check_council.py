"""Check a council's decision before it reaches the owner.

Usage: python3 check_council.py <task folder>
The folder holds question.md, council/council.json and council.md (written by
council.py) and decision.md (written by the chair). Fails when the council
did not really happen (fewer than two voices answered), when council.md was
edited by hand, when decision.md misses a part, cites no voice for a voice
that answered, or uses a number found neither in question.md nor in the
voices' answers. Warns when every voice came from one maker. Prints PASS,
WARN or FAIL; exits 1 on any FAIL.
"""
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import checks as C  # noqa: E402
import council as K  # noqa: E402

PARTS = ("## Decision", "## Why", "## Where the council disagreed", "## Risks", "## What would change this decision", "## Next steps")


def main():
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    folder = sys.argv[1]
    rep = C.Report()
    read = lambda *p: open(os.path.join(folder, *p), encoding="utf-8").read()
    try:
        question = read("question.md")
        record = C.load_json(os.path.join(folder, K.OUT, K.OUT + ".json"))
    except OSError as e:
        rep.add("FAIL", "missing %s: write question.md, then run council.py" % os.path.basename(e.filename or ""))
        rep.finish()
    voices = record.get("voices") or []
    first = [v for v in voices if v.get("rounds") and v["rounds"][0].get("ok")]
    rep.check([] if len(first) >= 2 else ["%d answered" % len(first)], "%d voices answered" % len(first),
              "a council needs at least two voices that answered")
    makers = {v.get("maker") for v in first}
    rep.check([] if len(makers) >= 2 else ["all from %s" % ", ".join(sorted(makers))], "voices from %d makers" % len(makers),
              "every voice came from one maker, so they may share blind spots", "WARN")
    try:
        same = read(K.OUT, K.OUT + ".md") == K.markdown(record)
    except OSError:
        same = False
    rep.add("PASS" if same else "FAIL", "council.md is what the voices said" if same else
            "council.md is missing or was edited by hand: run council.py again")
    try:
        decision = read("decision.md")
    except OSError:
        rep.add("FAIL", "decision.md not written")
        rep.finish()
    rep.check([p for p in PARTS if p not in decision], "decision.md has every part", "decision.md lacks")
    rep.check([] if re.search(r"(?im)^confidence:\s*(high|medium|low)\b", decision) else ["no line"],
              "the confidence is stated", "say Confidence: high, medium or low")
    rep.check(["Voice %s" % v["label"] for v in first if not re.search(r"\bVoice %s\b" % v["label"], decision)],
              "every voice that answered is cited", "decision.md never cites")
    pool = C.fact_pool(question, [x.get("text", "") for v in voices for x in v.get("rounds") or []])
    body = re.sub(r"(?im)^#.*$|\bVoice [A-E]\b", " ", decision)  # headings and voice letters are not facts
    rep.check(C.untraced(body, pool), "every number comes from question.md or the voices",
              "numbers found neither in question.md nor in the voices' answers")
    rep.finish()


if __name__ == "__main__":
    main()
