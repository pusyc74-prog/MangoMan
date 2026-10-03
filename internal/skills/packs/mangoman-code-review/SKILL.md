---
name: mangoman-code-review
description: Review a code change (uncommitted work, the last commit, or a branch against main) grounded in facts: the real diff, the project's own tests and linters, leaked secrets and leftover debug code; produces a review with a verdict and findings by severity, checked so every finding points at changed code. Use when the user asks for a code review, to review a pull request or branch, to check their changes before committing, or whether code is safe to merge.
license: Apache-2.0
metadata:
  pack: mangoman
  version: "1.0"
---

# Code review

You review a change the way a careful senior engineer would: correctness
first, then security, then clarity. Facts come from scripts, not memory: the
diff, test and lint results, secrets. The checker makes sure the review
matches those facts.

## 1. Collect the facts

```
python3 <skill dir>/scripts/collect.py <repo> [--base main] --out facts.json
```

Without `--base` it reviews uncommitted changes, or the last commit if there
are none. It runs the project's own checks when it finds them (Go vet and
tests, npm test, pytest, pyflakes, cargo test), finds secrets, debug code,
TODOs and large files in the added lines, and prints a one-line summary.

## 2. Read the change

Read every changed file around the changed lines (the diff alone hides
context). For each change ask: is it correct for all inputs (empty, zero,
missing, large, unicode)? Does it break callers? Is it safe (secrets,
injection, unsafe input handling, permissions)? Is there a simpler way? Are
the tests meaningful? Is anything left behind (debug code, dead code)?

## 3. Write `review.json`

```json
{
  "verdict": "approve | changes | blocked",
  "summary": "Two or three sentences: what the change does and what must happen before it merges.",
  "findings": [{"file": "pricing.py", "line": 11, "severity": "blocker", "title": "with_gst returns the tax, not the total",
                "detail": "Why it is wrong, with the input that breaks it.", "suggestion": "The fix, as code where possible."}],
  "praise": ["What was done well (optional, specific)."]
}
```

Severities: `blocker` (wrong results, security, failing checks, data loss),
`major` (likely bugs, missing tests for risky logic), `minor` (clarity,
naming, small refactors), `nit` (style). Every failing check and every leaked
secret is a blocker. Do not approve with blockers or majors. Be specific and
kind; point at the line, explain the risk, offer the fix. Do not pad the
review with nits.

## 4. Check and write the report

```
python3 <skill dir>/scripts/check_review.py review.json facts.json --out review.md
```

Fix every FAIL in `review.json`. When it passes, `review.md` is written.

## 5. Deliver

Give the user the verdict and the blockers first, then `review.md`. If they
want, fix the blockers and run the review again.
