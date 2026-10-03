"""Shared pieces of every pack's checker: results, fact tracing, common rules."""
import re
import sys

import tracenum
import vizlib as V

PLACEHOLDER = re.compile(r"lorem ipsum|\bTBD\b|\bTODO\b|xxx+|\[(?:your|client|company|brand|product|name|insert|date|amount|link|size)[^\]]*\]|\{\{\s*\w+\s*\}\}", re.I)
RISKY = re.compile(r"\b(guaranteed?|we guarantee|cures?|risk[- ]free|100% (?:safe|natural|pure|effective|organic|results|success)|best in (?:india|the world|town|class)|number one|cheapest|lowest price ever|miracle|clinically proven|no side effects)\b|(?<!\w)#1\b", re.I)


def strings(o):
    """Every string inside nested dicts and lists."""
    if isinstance(o, str):
        yield o
    elif isinstance(o, dict):
        for v in o.values():
            yield from strings(v)
    elif isinstance(o, list):
        for v in o:
            yield from strings(v)


def fact_pool(*sources):
    """Every number the user gave: numeric values plus numbers written in text."""
    pool = []
    for src in sources:
        pool += V.numbers_in(src)
        for s in strings(src):
            for _, c in tracenum.mentions(s):
                pool += [v for v, _ in c]
    return pool


def untraced(text, pool):
    """Numbers in text that are not in the pool (links and merge tags ignored)."""
    text = re.sub(r"\[([^\]]+)\]\([^)]+\)", r"\1", text)
    text = re.sub(r"https?://\S+|mailto:\S+|tel:\S+|wa\.me/\S+", " ", text)
    return tracenum.untraced(text, pool)


def first_match(rx, texts):
    """Distinct matches of rx across texts (one per text)."""
    return sorted({m.group(0) for t in texts for m in [rx.search(t)] if m})


class Report:
    """PASS / WARN / FAIL lines; exits 1 on any FAIL."""

    def __init__(self):
        self.rows = []

    def add(self, level, msg):
        self.rows.append((level, msg))

    def check(self, problems, ok, bad, level="FAIL", sep="; ", limit=8):
        """PASS with ok when problems is empty, else level with bad and the problems."""
        problems = sorted(set(problems)) if isinstance(problems, (list, set, tuple)) else problems
        if problems:
            self.add(level, bad + (": " + sep.join(list(problems)[:limit]) if isinstance(problems, list) else ""))
        else:
            self.add("PASS", ok)

    def finish(self):
        for level, msg in self.rows:
            print("%s  %s" % (level, msg))
        sys.exit(1 if any(l == "FAIL" for l, _ in self.rows) else 0)
