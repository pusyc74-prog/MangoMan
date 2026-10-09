"""Shared pieces of every pack's checker: results, fact tracing, common rules."""
import json
import os
import re
import sys

import tracenum
import vizlib as V

PLACEHOLDER = re.compile(r"lorem ipsum|\bTBD\b|\bTODO\b|xxx+|\[(?:your|client|company|brand|product|name|insert|date|amount|link|size)[^\]]*\]|\{\{\s*\w+\s*\}\}", re.I)
RISKY = re.compile(r"\b(guaranteed?|we guarantee|cures?|risk[- ]free|100% (?:safe|natural|pure|effective|organic|results|success)|best in (?:india|the world|town|class)|number one|cheapest|lowest price ever|miracle|clinically proven|no side effects)\b|(?<!\w)#1\b", re.I)


def load_json(path):
    """Read a JSON file the model wrote. If it is broken, stop with the lines
    around the mistake and the exact fix when one is found, so the model can
    mend it in one edit. Measured 8 and 9 Oct: website copy runs lost to a
    broken site.json after up to 113 requests spent hunting for the spot."""
    with open(path, encoding="utf-8") as f:
        text = f.read()
    try:
        return json.loads(text)
    except json.JSONDecodeError as e:
        sys.exit(json_problem(os.path.basename(path), text, e))


def json_problem(name, text, e):
    """Explain a JSON error: where, what is around it, and how to fix it."""
    lines = text.split("\n")
    out = ["%s is not valid JSON: %s (line %d, column %d)." % (name, e.msg, e.lineno, e.colno), ""]
    for n in range(max(1, e.lineno - 3), min(len(lines), e.lineno + 1) + 1):
        out.append("%s%5d | %s" % (">" if n == e.lineno else " ", n, lines[n - 1]))
        if n == e.lineno:
            out.append("      | " + " " * (e.colno - 1) + "^")
    fixes = _json_fixes(text, e)
    out.append("")
    if len(fixes) == 1:
        out += ["This fix makes it valid: %s." % fixes[0], "Change only that spot, then run the same command again."]
    elif fixes:
        out += ["These fixes make it valid:"] + ["- " + f for f in fixes]
        out.append("Change only those spots, then run the same command again.")
    else:
        out.append("The mistake is usually just before the ^: a missing comma at the end of the line "
                   "above, a \" inside text (write \\\" or use ' instead), or a comma before } or ].")
        out.append("Change only that spot, then run the same command again.")
    return "\n".join(out)


def _line_of(text, pos):
    return text.count("\n", 0, pos) + 1


def _json_fixes(text, e, depth=0):
    """Small repairs at the error that make the text parse, described for the
    model (none when no single kind of repair works). Up to three in a row."""
    pos = e.pos
    before = text[:pos].rstrip()
    tries = []
    if e.msg.startswith("Expecting ',' delimiter"):
        line_start = text.rfind("\n", 0, pos) + 1
        if text[line_start:pos].strip() == "":
            # Error at the start of a line: the line above lacks its comma.
            end = len(before)
            tries.append(("line %d needs a comma at its end" % _line_of(text, end - 1),
                          text[:end] + "," + text[end:]))
        else:
            # Mid-line: a quote inside the text ended it early.
            q = text.rfind('"', 0, pos)
            if q > 0:
                tries.append(('line %d: the " at column %d is inside the text; write it as \\" or use \' instead'
                              % (_line_of(text, q), q - text.rfind("\n", 0, q)), text[:q] + '\\"' + text[q + 1:]))
    # A comma with nothing after it (Python 3.13 points at the comma itself).
    c = pos if text[pos:pos + 1] == "," else len(before) - 1 if before.endswith(",") else -1
    if c >= 0 and e.msg.startswith(("Expecting property name", "Expecting value", "Illegal trailing comma")):
        tries.append(("line %d: remove the comma at its end (nothing follows it)" % _line_of(text, c),
                      text[:c] + text[c + 1:]))
    # A line with only a bracket, left over from an edit, near the error.
    starts = [0] + [i + 1 for i, ch in enumerate(text) if ch == "\n"]
    for n in range(max(1, e.lineno - 3), min(len(starts), e.lineno + 3) + 1):
        a, b = starts[n - 1], starts[n] if n < len(starts) else len(text)
        if text[a:b].strip() in ("}", "},", "]", "],"):
            tries.append(("line %d: delete this extra %s line" % (n, text[a:b].strip()), text[:a] + text[b:]))
    if e.msg.startswith("Invalid control character"):
        tries.append(("line %d: the text runs onto the next line; keep it on one line or write \\n" % e.lineno,
                      text[:pos] + "\\n" + text[pos + 1:]))
    later = []  # repairs that get further: tried again only if no single repair works
    for desc, fixed in tries:
        try:
            json.loads(fixed)
            return [desc]
        except json.JSONDecodeError as e2:
            if depth < 2 and e2.pos > pos:
                later.append((desc, fixed, e2))
    for desc, fixed, e2 in later:
        more = _json_fixes(fixed, e2, depth + 1)
        if more:
            return [desc] + more
    return []


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
