"""Check the social research before it is used.

Usage: python3 check_social.py social
Every account and page file is complete (a read account has dated posts, a
read page has its text and screenshot, an unread one says why), and
social-research.json is exactly what summarise_social.py computes from them
(nothing typed in by hand, nothing stale). Warns about old reads, quiet
accounts and a research with nothing read. Prints PASS, WARN or FAIL; exits
1 on any FAIL.
"""
import datetime as dt
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import checks as C  # noqa: E402
import summarise_social as S  # noqa: E402


def main():
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    folder = sys.argv[1]
    rep = C.Report()
    recs = S.load_records(folder)
    if not recs:
        rep.add("FAIL", "no account or page files: run read_social.py first")
        rep.finish()
    bad = [x for r in recs for x in S.problems(r, folder)]
    soft = []
    today = dt.datetime.now(dt.timezone.utc)
    for r in recs:
        if S.problems(r, folder):
            continue
        if today - dt.datetime.fromisoformat(r["read_at"]) > dt.timedelta(days=7):
            soft.append("%s was read on %s; read it again for current posts" % (r["account"], r["read_at"][:10]))
        dates = [p["date"] for p in r["posts"] if p.get("date")]
        if r["ok"] and dates and today.date() - dt.date.fromisoformat(max(dates)) > dt.timedelta(days=60):
            soft.append("%s has not posted since %s; it may be inactive" % (r["account"], max(dates)))
    rep.check(bad, "every account and page file is complete", "files")
    path = os.path.join(folder, S.OUT + ".json")
    if bad:
        pass  # the summary cannot be judged until the files are fixed
    elif not os.path.exists(path):
        rep.add("FAIL", "%s.json not built: run summarise_social.py" % S.OUT)
    elif json.dumps(C.load_json(path), sort_keys=True) != json.dumps(S.summary(recs), sort_keys=True):
        rep.add("FAIL", "%s.json does not match the files (edited by hand, or files changed since): run summarise_social.py again" % S.OUT)
    else:
        rep.add("PASS", "the summary's numbers are computed from the files")
    if not os.path.exists(os.path.join(folder, S.OUT + ".md")):
        rep.add("FAIL", "%s.md not built" % S.OUT)
    if not any(r.get("ok") for r in recs):
        soft.append("nothing could be read; ask the owner for links and screenshots of competitors' posts and ads")
    rep.check(soft, "reads are recent and accounts are active", "note", "WARN")
    rep.finish()


if __name__ == "__main__":
    main()
