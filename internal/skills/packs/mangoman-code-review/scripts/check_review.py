"""Check a code review against the facts, then write review.md.

Usage: python3 check_review.py review.json facts.json --out review.md
Checks: every finding points at a file in the change and at a line that
exists; failing checks and leaked secrets are raised as blockers and block
approval; no duplicate findings; verdict and summary present. Writes
review.md (summary, check results, findings by severity) when the review is
consistent. Prints PASS, WARN or FAIL; exits 1 on any FAIL.
"""
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import checks as C  # noqa: E402

SEVERITIES = ("blocker", "major", "minor", "nit")
VERDICTS = ("approve", "changes", "blocked")


def review_md(rv, facts):
    out = ["# Code review: %s (%s)" % (facts["repo"], facts["range"]), "", "**Verdict: %s**" % {"approve": "Approve", "changes": "Changes requested",
                                                                                            "blocked": "Blocked"}[rv["verdict"]], "", rv["summary"], ""]
    if facts["tools"]:
        out += ["## Checks run", "", "| Check | Result |", "| --- | --- |"] + ["| %s | %s |" % (t["name"], "passed" if t["ok"] else "**failed**") for t in facts["tools"]] + [""]
    for sev in SEVERITIES:
        fs = [f for f in rv["findings"] if f["severity"] == sev]
        if fs:
            out += ["## %s (%d)" % ({"blocker": "Blockers", "major": "Major", "minor": "Minor", "nit": "Nits"}[sev], len(fs)), ""]
            for f in sorted(fs, key=lambda x: (x["file"], x.get("line") or 0)):
                out += ["- **%s%s** %s" % (f["file"], ":%d" % f["line"] if f.get("line") else "", f["title"]), "  " + f.get("detail", "")]
                if f.get("suggestion"):
                    out += ["  Suggestion: " + f["suggestion"]]
            out.append("")
    notes = ["%s:%s adds %s" % (t["file"], t["line"], t["text"]) for t in facts.get("todos", [])]
    notes += ["%s is %s MB; large files bloat the repository (consider Git LFS or leaving it out)" % (b["file"], b["mb"]) for b in facts.get("large_files", [])]
    if notes:
        out += ["## Also in the change", ""] + ["- " + n for n in notes] + [""]
    if rv.get("praise"):
        out += ["## Done well", ""] + ["- " + p for p in rv["praise"]] + [""]
    return "\n".join(out)


def main():
    a = sys.argv[1:]
    if len(a) < 2:
        sys.exit(__doc__)
    rv = json.load(open(a[0], encoding="utf-8"))
    facts = json.load(open(a[1], encoding="utf-8"))
    out = a[a.index("--out") + 1] if "--out" in a else "review.md"
    rep = C.Report()
    prob = []
    if rv.get("verdict") not in VERDICTS:
        prob.append("verdict must be approve, changes or blocked")
    if not rv.get("summary"):
        prob.append("summary is required")
    fs = rv.get("findings", [])
    for i, f in enumerate(fs, 1):
        if f.get("severity") not in SEVERITIES:
            prob.append("finding %d: severity must be blocker, major, minor or nit" % i)
        if not f.get("file") or not f.get("title"):
            prob.append("finding %d: needs file and title" % i)
        if f.get("line") is not None and (not isinstance(f["line"], int) or isinstance(f["line"], bool) or f["line"] < 1):
            prob.append("finding %d: line must be one line number (for a range, give the first line)" % i)
    rep.check(prob, "review is complete", "review problems")
    if prob:
        rep.finish()
    changed = facts["files"]
    rep.check(sorted({f["file"] for f in fs if f["file"] not in changed}), "every finding is about a changed file", "findings about files not in the change")
    far = []
    for f in fs:
        lines = changed.get(f["file"], {}).get("added_lines", [])
        if f.get("line") and lines and min(abs(f["line"] - l) for l in lines) > 15:
            far.append("%s:%s" % (f["file"], f["line"]))
    rep.check(far, "findings point at changed lines", "findings far from any changed line (check the line numbers)", "WARN")
    failed = [t["name"] for t in facts["tools"] if not t["ok"]]
    blockers = [f for f in fs if f["severity"] == "blocker"]
    if failed:
        rep.check([] if blockers and rv["verdict"] != "approve" else failed, "failing checks are raised as blockers",
                  "checks failed but the review does not block on them")
    if facts["secrets"]:
        missed = ["%s:%s (%s)" % (s["file"], s["line"], s["kind"]) for s in facts["secrets"]
                  if not any(f["file"] == s["file"] and f["severity"] == "blocker" for f in fs)]
        rep.check(missed + (["approved with a secret in the change"] if rv["verdict"] == "approve" else []),
                  "leaked secrets are blockers", "secrets in the change not raised as blockers")
    if rv["verdict"] == "approve" and any(f["severity"] in ("blocker", "major") for f in fs):
        rep.add("FAIL", "approved with blocker or major findings; use changes or blocked")
    dup = sorted({"%s:%s" % (f["file"], f.get("line")) for f in fs if sum(1 for g in fs if (g["file"], g.get("line"), g["title"]) == (f["file"], f.get("line"), f["title"])) > 1})
    rep.check(dup, "no duplicate findings", "duplicate findings", "WARN")
    if not facts["tools"]:
        rep.add("WARN", "no tests or linters were run; say so in the summary and suggest how to test")
    if facts["debug"] and not any(f["file"] == d["file"] for d in facts["debug"] for f in fs):
        rep.add("WARN", "debug code was added (%s) and the review does not mention it" % ", ".join("%s:%s" % (d["file"], d["line"]) for d in facts["debug"][:3]))
    rep.check(C.first_match(C.PLACEHOLDER, [rv["summary"]] + [f.get("detail", "") for f in fs]), "no placeholder text", "placeholder text left in")
    if not any(l == "FAIL" for l, _ in rep.rows):
        with open(out, "w", encoding="utf-8") as fh:
            fh.write(review_md(rv, facts))
        rep.add("PASS", "wrote " + out)
    rep.finish()


if __name__ == "__main__":
    main()
