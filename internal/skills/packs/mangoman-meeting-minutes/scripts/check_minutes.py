"""Check meeting minutes before sending them.

Usage: python3 check_minutes.py minutes.json minutes
Checks: the spec is valid; every action has an owner and a due date on or
after the meeting; owners attended (or are marked external); decisions are
recorded; and, with a transcript, every number, quote and name in the minutes
appears in it (nothing invented). Prints PASS, WARN or FAIL; exits 1 on FAIL.
"""
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_minutes as B  # noqa: E402
import checks as C  # noqa: E402

QUOTE = re.compile(r"[\"“]([^\"”]{8,})[\"”]")


def written(spec):
    """Everything the minutes state, with where it is."""
    out = [("summary", spec["summary"])]
    out += [("decision", d["text"] if isinstance(d, dict) else d) for d in spec.get("decisions", [])]
    out += [("action", a["action"]) for a in spec.get("actions", [])]
    out += [("topic " + t["title"], x) for t in spec.get("topics", []) for x in t.get("points", [])]
    out += [("open question", q) for q in spec.get("open_questions", [])]
    return out


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
    when = B.day(spec["date"])
    acts = spec.get("actions", [])
    rep.check([a["action"][:50] for a in acts if not a.get("owner")], "every action has an owner", "actions without an owner")
    rep.check([a["action"][:50] for a in acts if not a.get("due")], "every action has a due date", "actions without a due date", "WARN")
    rep.check([a["action"][:50] for a in acts if a.get("due") and B.day(a["due"]) < when], "due dates are after the meeting", "due dates before the meeting")
    people = {n.lower() for n in B.names(spec)} | {x.lower() for x in spec.get("external", [])}
    rep.check(sorted({a["owner"] for a in acts if a.get("owner") and a["owner"].lower() not in people}), "action owners attended the meeting",
              "owners not in attendees (add them, or list them under external)", "WARN", ", ")
    rep.check([] if spec.get("decisions") else ["none"], "decisions recorded", "no decisions recorded: say what was agreed, or that nothing was", "WARN")
    texts = written(spec)
    if spec.get("transcript"):
        src_text = B.transcript_text(os.path.join(bdir, spec["transcript"]))
        flat = re.sub(r"\s+", " ", src_text).lower()
        pool = C.fact_pool(src_text)
        rep.check(sorted({"%s: %s" % (w, u) for w, t in texts for u in C.untraced(t, pool)}), "every number appears in the transcript",
                  "numbers not in the transcript (check them or remove them)")
        rep.check(sorted({q for _, t in texts for q in QUOTE.findall(t) if re.sub(r"\s+", " ", q).lower() not in flat}),
                  "every quote appears word for word in the transcript", "quotes not found in the transcript")
        rep.check(sorted({a["owner"] for a in acts if a.get("owner") and a["owner"].split()[0].lower() not in flat}),
                  "every action owner is named in the transcript", "owners never mentioned in the transcript", "WARN", ", ")
    else:
        rep.add("WARN", "no transcript given: numbers and quotes were not traced; ask the user to confirm them")
    rep.check(C.first_match(C.PLACEHOLDER, [t for _, t in texts]), "no placeholder text", "placeholder text left in")
    for f in (out + ".md", out + "-actions.csv"):
        if not os.path.exists(f):
            rep.add("FAIL", "%s not built" % f)
    rep.finish()


if __name__ == "__main__":
    main()
