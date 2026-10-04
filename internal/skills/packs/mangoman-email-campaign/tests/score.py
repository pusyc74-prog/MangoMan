"""Score an email campaign made for one test case, 0 to 100.

Usage: python3 score.py CASE_FOLDER   (prints {"score": n, "notes": "..."})
The folder holds the case inputs and the contender's campaign.json. The
emails are rebuilt here so every contender is judged the same way:
- rules (40): the pack's checker; each FAIL costs 15, each WARN 3;
- action (20): share of emails with a button to the brief's main link;
- subjects (20): 10 for subjects of 15 to 50 characters (what phones show),
  10 when two or more of them carry the brief's key benefit within that length;
- sequence (20): 10 for the number of emails asked for, 5 for send days in
  order within the period asked for, 5 when no two emails say the same thing.
Subjects and the email count only count emails with a button to the main link;
the send-day and repeat points need the count to be within one of the brief.
Each claim word ("organic", "pure") the facts do not make costs 5.
"""
import json
import os
import re
import subprocess
import sys
import tempfile

SCRIPTS = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "scripts")
sys.path.insert(0, SCRIPTS)
import check_emails as K  # noqa: E402

STOP = set("a an and as at by for from in into of on or the to with is are be it this that your you we our i my".split())
CLAIMS = {"free", "natural", "organic", "pure", "original", "authentic", "genuine", "fresh", "healthy", "herbal", "ayurvedic",
          "premium", "certified", "handmade", "best", "guaranteed", "award", "cheapest", "lowest"}


def terms(text):
    out = set()
    for w in re.findall(r"[\w\u0900-\u0dff]+", text.lower()):
        if w not in STOP:
            out.add(w[:-1] if len(w) > 4 and w.endswith("s") else w)
    return out


def brief_line(brief, key):
    m = re.search(r"^- %s:\s*(.+)$" % key, brief, re.M | re.I)
    return m.group(1).strip() if m else ""


def score(case):
    spec_path = os.path.join(case, "campaign.json")
    if not os.path.exists(spec_path):
        return 0, "no campaign.json"
    try:
        spec = json.load(open(spec_path, encoding="utf-8"))
    except ValueError as ex:
        return 0, "campaign.json is not valid JSON: %s" % ex
    out = os.path.join(tempfile.mkdtemp(), "emails")
    b = subprocess.run([sys.executable, os.path.join(SCRIPTS, "build_emails.py"), spec_path, "--out", out, "--no-shots"],
                       capture_output=True, text=True, cwd=case)
    if b.returncode:
        return 0, "build failed: " + (b.stderr or b.stdout).strip().splitlines()[-1][:200]
    c = subprocess.run([sys.executable, os.path.join(SCRIPTS, "check_emails.py"), spec_path, out],
                       capture_output=True, text=True, cwd=case)
    lines = c.stdout.splitlines()
    fails = [x for x in lines if x.startswith("FAIL")]
    warns = [x for x in lines if x.startswith("WARN")]
    rules = max(0, 40 - 15 * len(fails) - 3 * len(warns))

    brief = open(os.path.join(case, "brief.md"), encoding="utf-8").read()
    facts = terms(brief + open(os.path.join(case, "facts.md"), encoding="utf-8").read())
    emails = spec["emails"]
    n = len(emails)

    link = re.search(r"https://\S+", brief_line(brief, "Main link")).group(0)
    base = lambda u: u.split("?")[0].split("#")[0].rstrip("/")
    acting = [m for m in emails if any(bl["type"] == "button" and base(bl["href"]) == base(link) for bl in m["blocks"])]
    action = 20 * len(acting) / n

    # subjects and the sequence count only for emails that lead to the brief's link
    benefit = terms(brief_line(brief, "Key benefit"))
    fit = [m["subject"] for m in acting if 15 <= len(m["subject"]) <= 50]
    carry = sum(1 for s in fit if benefit <= terms(s))
    subjects = 10 * len(fit) / n + (10 if carry >= 2 else 5 if carry else 0)

    want = int(re.match(r"\d+", brief_line(brief, "Emails")).group(0))
    days = int(re.search(r"(\d+) days", brief_line(brief, "Emails")).group(1))
    sequence = 10 if len(acting) == want == n else 5 if abs(len(acting) - want) == 1 else 0
    sends = [m.get("send_day") for m in emails]
    if sequence and all(isinstance(d, (int, float)) for d in sends) and sends == sorted(set(sends)) and days / 2 <= sends[-1] <= days + 2:
        sequence += 5
    bodies = [terms(" ".join(K.email_texts(m))) for m in emails]
    same = [(i, j) for i in range(n) for j in range(i + 1, n) if len(bodies[i] & bodies[j]) > 0.5 * len(bodies[i] | bodies[j])]
    if sequence and not same:
        sequence += 5

    said = terms(" ".join(t for m in emails for t in [m["subject"], m["preview"]] + m.get("subject_alternatives", []) + K.email_texts(m)))
    unbacked = sorted(w for w in said & CLAIMS if w not in facts)
    quality = max(0, subjects + action + sequence - 5 * len(unbacked))
    total = round(rules + quality, 1)
    notes = "rules %d/40, action %.1f/20 (%d of %d emails), subjects %.1f/20 (%d fit, %d with the benefit), sequence %d/20 (%d asked for)" % (
        rules, action, len(acting), n, subjects, len(fit), carry, sequence, want)
    if unbacked:
        notes += "; claims not in the facts: " + ", ".join(unbacked)
    if same:
        notes += "; emails %s repeat each other" % ", ".join("%d and %d" % (i + 1, j + 1) for i, j in same)
    if fails:
        notes += "; " + "; ".join(f[6:80] for f in fails[:3])
    return total, notes


if __name__ == "__main__":
    s, n = score(sys.argv[1])
    print(json.dumps({"score": s, "notes": n}))
