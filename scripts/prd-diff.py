"""Check that docs/PRD.md matches the Claude Docs PRD.

    python3 scripts/prd-diff.py <export file>

The export file is what the Claude Docs export tool returns for the PRD
(format markdown): either its JSON (data.bytes_b64) or a saved tool result
that wraps that JSON in a list of text blocks. Prints the differing lines and
"diff lines (0 = identical): N". Mermaid diagrams, embedded content and the
backslashes and backticks the two sides escape differently are ignored.
"""
import base64
import difflib
import json
import re
import sys

raw = json.load(open(sys.argv[1]))
if isinstance(raw, list):  # a saved tool result: [{type, text}, {type, text: <export JSON>}]
    raw = next(json.loads(b["text"]) for b in raw if b.get("text", "").lstrip().startswith("{"))
doc = base64.b64decode(raw["data"]["bytes_b64"]).decode()
repo = open("docs/PRD.md", encoding="utf-8").read()


def norm(text):
    out, skip = [], False
    for line in text.splitlines():
        if line.startswith("```mermaid"):
            skip = True
            continue
        if skip:
            skip = not line.startswith("```")
            continue
        if "embedded content" in line:
            continue
        out.append(re.sub(r"[`\\]", "", line))
    return out


n = 0
for line in difflib.unified_diff(norm(repo), norm(doc), "repo", "doc", lineterm="", n=0):
    print(line[:300])
    n += 1
print("diff lines (0 = identical):", n)
