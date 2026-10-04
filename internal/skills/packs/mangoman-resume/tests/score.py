"""Score a resume made for one test case, 0 to 100.

Usage: python3 score.py CASE_FOLDER   (prints {"score": n, "notes": "..."})
The folder holds the case inputs (candidate.md, job_description.md) and the
contender's resume.json. The resume is rebuilt here in the classic design so
every contender is judged the same way:
- rules (40): the pack's checker for one page; each FAIL costs 15, each WARN 3;
- keywords (30): share of the job's key skills that the candidate's facts
  support and the resume shows (what a recruiter's search matches);
- results (20): share of experience bullets with a number, every number in
  them from the candidate's facts;
- target (10): the headline names the job's role (3); the summary is at most
  70 words and names 2 or more supported key skills (3); the first three
  bullets of the latest role each name a supported key skill (4).
Claims cost 5 each: a key skill the facts do not support, or a number that
is not in the facts.
"""
import json
import os
import re
import subprocess
import sys
import tempfile

SCRIPTS = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "scripts")
sys.path.insert(0, SCRIPTS)
import checks as C  # noqa: E402

SHOWN = ("headline", "summary", "experience", "education", "skills", "projects", "certifications", "awards")
STOP = set("a an and as at by for from in into of on or the to with".split())


def stem(w):
    for suf in ("ing", "ed", "es", "s"):
        if len(w) > 4 and w.endswith(suf):
            return w[: -len(suf)]
    return w


def terms(text):
    return {stem(w) for w in re.findall(r"\w+", text.lower()) if w not in STOP}


def line(text, label):
    m = re.search(r"^#*\s*%s\s*:?\s*\n?(.+)$" % label, text, re.I | re.M)
    return m.group(1).strip() if m else ""


def score(case):
    spec_path = os.path.join(case, "resume.json")
    if not os.path.exists(spec_path):
        return 0, "no resume.json"
    try:
        r = json.load(open(spec_path, encoding="utf-8"))
    except ValueError as ex:
        return 0, "resume.json is not valid JSON: %s" % ex
    out = os.path.join(tempfile.mkdtemp(), "resume")
    b = subprocess.run([sys.executable, os.path.join(SCRIPTS, "build.py"), spec_path, "--template", "classic", "--out", out],
                       capture_output=True, text=True, cwd=case)
    if b.returncode or not os.path.exists(out + ".pdf"):
        return 0, "build failed: " + ((b.stderr or b.stdout).strip().splitlines() or ["no PDF"])[-1][:200]
    c = subprocess.run([sys.executable, os.path.join(SCRIPTS, "check.py"), spec_path, out + ".pdf", "--pages", "1"],
                       capture_output=True, text=True, cwd=case)
    lines = c.stdout.splitlines()
    fails = [x for x in lines if x.startswith("FAIL")]
    warns = [x for x in lines if x.startswith("WARN")]
    rules = max(0, 40 - 15 * len(fails) - 3 * len(warns))

    facts_text = open(os.path.join(case, "candidate.md"), encoding="utf-8").read()
    jd = open(os.path.join(case, "job_description.md"), encoding="utf-8").read()
    facts = terms(facts_text)
    shown_text = " ".join(C.strings({k: r[k] for k in SHOWN if k in r}))
    shown = terms(shown_text)
    skills = [s.strip() for s in line(jd, "key skills").split(",") if s.strip()]
    supported = [s for s in skills if terms(s) <= facts]
    claimed = [s for s in skills if s not in supported and terms(s) <= shown]
    covered = [s for s in supported if terms(s) <= shown]
    keywords = 30 * len(covered) / max(1, len(supported))

    pool = C.fact_pool(facts_text)
    bullets = [x for e in r.get("experience", []) for x in e.get("bullets", []) if isinstance(x, str)]
    with_num = [x for x in bullets if re.search(r"\d", x) and not C.untraced(x, pool)]
    share = len(with_num) / max(1, len(bullets))
    results = 20 * share
    invented = sorted({u for s in C.strings({k: r[k] for k in SHOWN if k in r}) for u in C.untraced(s, pool)})

    summary = r.get("summary", "") or ""
    top = [x for x in ((r.get("experience") or [{}])[0].get("bullets") or [])[:3] if isinstance(x, str)]
    target = (3 if terms(line(jd, "role")) <= terms(r.get("headline", "") or "") else 0) + \
        (3 if len(summary.split()) <= 70 and sum(terms(s) <= terms(summary) for s in supported) >= 2 else 0) + \
        4 * sum(any(terms(s) <= terms(x) for s in supported) for x in top) / 3
    penalty = 5 * (len(claimed) + len(invented))
    total = max(0, round(rules + keywords + results + target - penalty, 1))
    notes = "rules %d/40, keywords %.1f/30 (%d of %d supported skills), results %.1f/20 (%d%% of bullets), target %.1f/10" % (
        rules, keywords, len(covered), len(supported), results, 100 * share, target)
    if claimed or invented:
        notes += "; claims -%d: %s" % (penalty, ", ".join(claimed + invented)[:200])
    if fails:
        notes += "; " + "; ".join(f[6:80] for f in fails[:3])
    return total, notes


if __name__ == "__main__":
    s, n = score(sys.argv[1])
    print(json.dumps({"score": s, "notes": n}))
