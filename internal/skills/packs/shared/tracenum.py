"""Trace numbers written in text back to numbers the analysis computed.

A headline such as "Revenue up 18% to ₹4.2 Cr" must only use numbers that
exist in the computed spec (allowing for rounding, percent and unit
suffixes). Untraceable numbers are reported so they can be fixed.
"""
import re

TOKEN = re.compile(r"(?<![\w.])([₹$€£]?\s?[-+−]?\d[\d,]*(?:\.\d+)?)\s*(%|pp|Cr|cr|L|lakh|K|k|M|B|bn|mn)?(?![\w])")
MULT = {"cr": 1e7, "l": 1e5, "lakh": 1e5, "k": 1e3, "m": 1e6, "mn": 1e6, "b": 1e9, "bn": 1e9}
YEARS = range(1990, 2101)


def mentions(text):
    """(raw token, candidate values) for each number in a text."""
    out = []
    for m in TOKEN.finditer(text or ""):
        raw, unit = m.group(1), (m.group(2) or "")
        num = raw.replace("₹", "").replace("$", "").replace("€", "").replace("£", "").replace(",", "").replace("−", "-").strip()
        try:
            v = float(num)
        except ValueError:
            continue
        u = unit.lower()
        if not unit and "." not in num and int(abs(v)) in YEARS:
            continue  # a year, not a measurement
        if not unit and abs(v) <= 10 and "." not in num and not raw.strip()[0] in "₹$€£":
            continue  # small counts ("3 regions", "top 5") are wording, not data
        cands = []
        if u in ("%", "pp"):
            cands = [v / 100, v]
        elif u in MULT:
            cands = [v * MULT[u]]
        else:
            cands = [v]
        out.append((m.group(0).strip(), cands))
    return out


def close(a, b):
    if b == 0:
        return abs(a) < 1e-9
    return abs(a - b) <= max(abs(b) * 0.006, 0.0006)


def untraced(text, computed):
    """Number mentions in text that match nothing computed."""
    bad = []
    pool = [float(x) for x in computed]
    derived = pool + [abs(x) for x in pool]
    for raw, cands in mentions(text):
        if not any(close(c, p) or close(c, round(p, 2)) or close(c, round(p, 1)) for c in cands for p in derived):
            # allow a rounded display: 4.2 Cr for 41,987,000
            if not any(abs(c - p) <= abs(p) * 0.03 for c in cands for p in derived if p):
                bad.append(raw)
    return bad
