"""Check a built proposal before sending it.

Usage: python3 check_proposal.py proposal.json proposal
Checks: the spec is valid (payment milestones add to 100%, dates in order,
validity after the proposal date); the arithmetic is recomputed and matches
what was built; every number in the writing traces to the user's facts or a
computed figure; the PDF exists with the expected pages and every section;
the client's name is used consistently; no placeholder text; risky promises
flagged; the Word file exists. Prints PASS, WARN or FAIL; exits 1 on any FAIL.
"""
import json
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import build_proposal as B  # noqa: E402
import render  # noqa: E402
import tracenum  # noqa: E402
import vizlib as V  # noqa: E402

PLACEHOLDER = re.compile(r"lorem ipsum|\bTBD\b|\bTODO\b|\[(?:client|company|name|insert|date|amount)[^\]]*\]|xxx+|<[A-Z ]+>|\{\{.*?\}\}", re.I)
RISKY = re.compile(r"\b(guaranteed?|we guarantee|100% (?:success|results|uptime)|risk[- ]free|unlimited revisions|no hidden costs ever|best in (?:india|the world|class)|number one|#1|double your|triple your)\b", re.I)
TEXT_KEYS = ("headline", "lede", "body", "points", "goals", "challenges", "included", "excluded", "items", "notes",
             "description", "name", "outcome", "bio", "result", "client", "role", "title", "subtitle", "milestone", "due", "item")


CLAIM_SECTIONS = ("summary", "understanding", "proof", "text")  # statements about the client and results: must be facts


def writing(spec, claims=None):
    """Every piece of writing (not the price numbers themselves). claims=True
    keeps only sections that state facts, False only the offer's own terms."""
    out = []

    def walk(o, key=""):
        if isinstance(o, str):
            if key in TEXT_KEYS:
                out.append(o)
        elif isinstance(o, dict):
            for k, v in o.items():
                if k not in ("facts", "brand", "from", "rate", "qty", "percent", "start", "end", "weeks"):
                    walk(v, k)
        elif isinstance(o, list):
            for v in o:
                walk(v, key)
    for sec in spec.get("sections", []):
        if claims is None or (sec.get("type") in CLAIM_SECTIONS) == claims:
            walk(sec)
    if claims is False:
        return out
    for k in ("title", "subtitle"):
        if spec.get(k):
            out.append(spec[k])
    return out


def fact_pool(spec, nums):
    pool = V.numbers_in(spec.get("facts", {})) + B.figures(nums)

    def strings(o):
        if isinstance(o, str):
            yield o
        elif isinstance(o, dict):
            for v in o.values():
                yield from strings(v)
        elif isinstance(o, list):
            for v in o:
                yield from strings(v)
    for s in strings(spec.get("facts", {})):
        for _, c in tracenum.mentions(s):
            pool += c
    for g in nums["groups"].values():  # totals may be quoted in lakh, crore or K
        pool += [g["total"], g["taxable"]]
    return pool


def main():
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    src, out = sys.argv[1], sys.argv[2]
    spec = B.load(src)
    bdir = os.path.dirname(os.path.abspath(src))
    rs = []

    def r(level, msg):
        rs.append((level, msg))

    probs = B.validate(spec, bdir)
    r("PASS" if not probs else "FAIL", "spec is valid" if not probs else "; ".join(probs))
    if probs:
        return finish(rs)
    nums = B.compute(spec)
    npath = out + ".numbers.json"
    if os.path.exists(npath):
        built = json.load(open(npath, encoding="utf-8"))
        same = json.dumps(built, sort_keys=True) == json.dumps(json.loads(json.dumps(nums)), sort_keys=True)
        r("PASS" if same else "FAIL", "prices recomputed and match the built proposal" if same else
          "proposal.json changed since the build: rebuild before sending")
    one = nums["groups"].get("one-time")
    if one:
        line_sum = round(sum(x["amount"] for x in nums["lines"] if x["billing"] == "one-time"), 2)
        ok = abs(line_sum - one["subtotal"]) < 0.01 and abs(one["taxable"] + one["tax"] - one["total"]) < 0.01
        if nums.get("payments"):
            ok = ok and abs(sum(m["amount"] for m in nums["payments"]) - one["total"]) < 0.01
        r("PASS" if ok else "FAIL", "line items, tax, total and payment schedule add up (total %s)" % B.money(one["total"], nums["currency"])
          if ok else "arithmetic does not add up; report this as a bug")
        if not nums.get("payments"):
            r("WARN", "no payment schedule: say when the client pays (for example 50% to start, 50% on delivery)")
    if nums["currency"] == "INR" and one and not one["tax"]:
        r("WARN", "no GST on an INR proposal: set pricing.tax, or say in a note that prices exclude GST")
    if not spec.get("valid_until"):
        r("WARN", "no valid_until: prices without an expiry can be held against you")

    texts = writing(spec)
    pool = fact_pool(spec, nums)
    bad = sorted({u for t in writing(spec, True) for u in tracenum.untraced(t, pool)})
    r("PASS" if not bad else "FAIL", "every number in the summary, background and results comes from the facts or the pricing" if not bad else
      "numbers not in facts or pricing (ask the user, or remove them): " + ", ".join(bad[:8]))
    soft = sorted({u for t in writing(spec, False) for u in tracenum.untraced(t, pool)})
    if soft:
        r("WARN", "numbers in scope, timeline or terms the user should confirm (notice periods, counts, rounds): " + ", ".join(soft[:8]))
    ph = sorted({m.group(0) for t in texts + [spec.get("client", "")] for m in [PLACEHOLDER.search(t)] if m})
    r("PASS" if not ph else "FAIL", "no placeholder text" if not ph else "placeholder text left in: " + ", ".join(ph))
    risky = sorted({m.group(0) for t in texts for m in [RISKY.search(t)] if m})
    r("PASS" if not risky else "WARN", "no risky promises" if not risky else "promises that can become disputes: " + ", ".join(risky))

    client = spec["client"]
    first = client.split()[0].lower()
    others = sorted({w for t in texts for w in re.findall(r"\b[A-Z][\w&]+(?: [A-Z][\w&]+)*\b", t)
                     if w.lower().startswith(first) and w != client and not client.startswith(w)})
    r("PASS" if not others else "WARN", "client name used consistently" if not others else
      "client named differently in places (%s vs %s): check none is left from another proposal" % (", ".join(others[:3]), client))
    types = [s["type"] for s in spec["sections"]]
    for want, why in (("scope", "what is and is not included prevents scope disputes"), ("timeline", "the client will ask when"),
                      ("acceptance", "make it easy to say yes: add a sign-off section")):
        if want not in types:
            r("WARN", "no %s section: %s" % (want, why))

    pdf = out + ".pdf"
    if os.path.exists(pdf):
        n = render.pdf_pages(pdf)
        r("PASS" if n <= 12 else "WARN", "%d pages" % n if n <= 12 else "%d pages: decision makers read short proposals; move detail to an appendix" % n)
        txt = render.pdf_text(pdf)
        if txt:
            norm = re.sub(r"\s+", " ", txt).lower()
            missing = [s["headline"] for s in spec["sections"] if re.sub(r"\s+", " ", s["headline"]).lower()[:30] not in norm]
            r("PASS" if not missing else "FAIL", "every section is in the PDF" if not missing else "sections missing from the PDF: " + "; ".join(missing))
            if one:
                tot = B.money(one["total"], nums["currency"])
                r("PASS" if tot.replace(" ", "") in txt.replace(" ", "") else "FAIL", "the total appears in the PDF" if tot.replace(" ", "") in txt.replace(" ", "")
                  else "the total %s is not in the PDF" % tot)
    else:
        r("WARN", "no PDF (no browser engine): open the HTML and print to PDF")
    r("PASS" if os.path.exists(out + ".docx") else "WARN", "Word file built" if os.path.exists(out + ".docx") else "no Word file: pip install python-docx and rebuild")
    return finish(rs)


def finish(rs):
    for level, msg in rs:
        print("%s  %s" % (level, msg))
    sys.exit(1 if any(l == "FAIL" for l, _ in rs) else 0)


if __name__ == "__main__":
    main()
