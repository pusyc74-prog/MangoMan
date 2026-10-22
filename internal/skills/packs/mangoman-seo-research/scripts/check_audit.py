"""Check the site audit before it is used.

Usage: python3 check_audit.py seo
seo-audit.json is complete, and its findings and seo-audit.md are exactly
what audit_site.py computes from the pages it read (nothing typed in by
hand). Warns when the home page could not be read, when few pages were
read and when the audit is old. Prints PASS, WARN or FAIL; exits 1 on any
FAIL.
"""
import datetime as dt
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import audit_site as A  # noqa: E402
import checks as C  # noqa: E402

PAGE = ("url", "ok", "status", "why", "final_url", "seconds", "kb")
READ = ("title", "description", "robots", "x_robots", "canonical", "viewport", "lang", "h1", "h2", "schema",
        "schema_broken", "images", "no_alt", "og_image", "words", "internal_links")


def main():
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    folder = sys.argv[1]
    rep = C.Report()
    path = os.path.join(folder, A.OUT + ".json")
    if not os.path.exists(path):
        rep.add("FAIL", "%s.json not found: run audit_site.py first" % A.OUT)
        rep.finish()
    a = C.load_json(path)
    bad = ["missing %s" % k for k in ("site", "read_at", "robots", "sitemap", "pages", "skipped", "findings") if k not in a]
    if not bad:
        bad = ["robots: missing %s" % k for k in ("found", "status", "why", "blocked", "text") if k not in a["robots"]]
        bad += ["sitemap: missing %s" % k for k in ("found", "count") if k not in a["sitemap"]]
    if not bad:
        bad = ["page %d: missing %s" % (i, ", ".join(k for k in PAGE + (READ if p.get("ok") else ()) if k not in p))
               for i, p in enumerate(a["pages"], 1) if any(k not in p for k in PAGE + (READ if p.get("ok") else ()))]
    rep.check(bad, "the audit file is complete", "file")
    if bad:
        rep.finish()
    same = json.dumps(A.findings(a), sort_keys=True) == json.dumps(a["findings"], sort_keys=True)
    rep.add("PASS" if same else "FAIL", "the findings are computed from the pages read" if same else
            "the findings do not match the pages read (edited by hand): run audit_site.py again")
    md = os.path.join(folder, A.OUT + ".md")
    if not os.path.exists(md) or open(md, encoding="utf-8").read() != A.markdown(a):
        rep.add("FAIL", "%s.md is missing or does not match %s.json: run audit_site.py again" % (A.OUT, A.OUT))
    soft = []
    if not a["pages"] or not a["pages"][0]["ok"]:
        soft.append("the home page could not be read (%s); check the address, or say plainly that the site could not be audited"
                    % (a["pages"][0]["why"] if a["pages"] else "no pages"))
    elif sum(p["ok"] for p in a["pages"]) < 3:
        soft.append("only %d page(s) read; name the pages that matter most after the site" % sum(p["ok"] for p in a["pages"]))
    try:
        if A.now() - dt.datetime.fromisoformat(a["read_at"]) > dt.timedelta(days=7):
            soft.append("the audit is from %s; run it again for the site as it is now" % a["read_at"][:10])
    except (TypeError, ValueError):
        rep.add("FAIL", "read_at is not a date and time")
    rep.check(soft, "the audit read the site recently and fully", "note", "WARN")
    rep.finish()


if __name__ == "__main__":
    main()
