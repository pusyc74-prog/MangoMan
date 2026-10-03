"""Collect the facts a code review needs: the diff, test and lint results, secrets.

Usage: python3 collect.py <repo> [--base main] [--no-tests] --out facts.json
Without --base, reviews uncommitted changes, or the last commit when there are
none. Runs the project's own checks when it finds them (Go vet and tests,
npm test, pytest, pyflakes, cargo test), with a time limit.
"""
import json
import os
import re
import shutil
import subprocess
import sys

SECRETS = [("AWS access key", r"AKIA[0-9A-Z]{16}"), ("private key", r"-----BEGIN [A-Z ]*PRIVATE KEY-----"),
           ("GitHub token", r"gh[pousr]_[A-Za-z0-9]{36,}"), ("Slack token", r"xox[baprs]-[A-Za-z0-9-]{10,}"),
           ("Google API key", r"AIza[0-9A-Za-z_-]{35}"), ("OpenAI-style key", r"sk-[A-Za-z0-9_-]{20,}"),
           ("password or key in code", r"(?i)\b(password|passwd|secret|api_?key|token)\b\s*[:=]\s*['\"][^'\"\s]{8,}['\"]")]
DEBUG = r"\bconsole\.log\(|\bdebugger;|\bpdb\.set_trace\(|\bbreakpoint\(\)|\bfmt\.Println\(\"(?:debug|here|xxx)"


def git(repo, *args):
    return subprocess.run(["git", "-C", repo, *args], capture_output=True, text=True).stdout


def diff_range(repo, base):
    if base:
        return [base + "...HEAD"]
    if git(repo, "status", "--porcelain").strip():
        return ["HEAD"]
    return ["HEAD~1", "HEAD"]


def parse_diff(text):
    """{file: {"added": [(line, text)], "removed": n}} from a unified diff with zero context."""
    files, cur, line = {}, None, 0
    for raw in text.splitlines():
        if raw.startswith("+++ "):
            cur = raw[6:] if raw.startswith("+++ b/") else None
            if cur:
                files[cur] = {"added": [], "removed": 0}
        elif raw.startswith("@@") and cur:
            line = int(re.search(r"\+(\d+)", raw).group(1))
        elif cur and raw.startswith("+") and not raw.startswith("+++"):
            files[cur]["added"].append((line, raw[1:]))
            line += 1
        elif cur and raw.startswith("-") and not raw.startswith("---"):
            files[cur]["removed"] += 1
    return files


def checks_for(repo):
    """The project's own check commands, by what files it has."""
    has = lambda p: os.path.exists(os.path.join(repo, p))
    cmds = []
    if has("go.mod") and shutil.which("go"):
        cmds += [("go vet", ["go", "vet", "./..."]), ("go test", ["go", "test", "./..."])]
    if has("package.json") and shutil.which("npm"):
        test = json.load(open(os.path.join(repo, "package.json"))).get("scripts", {}).get("test", "")
        if test and "no test specified" not in test:
            cmds.append(("npm test", ["npm", "test", "--silent"]))
    py = any(f.endswith(".py") for f in os.listdir(repo))
    if py or has("pyproject.toml") or has("setup.py"):
        if subprocess.run([sys.executable, "-m", "pyflakes", "--version"], capture_output=True).returncode == 0:
            cmds.append(("pyflakes", [sys.executable, "-m", "pyflakes", "."]))
        if (has("tests") or has("pytest.ini") or any(f.startswith("test_") for f in os.listdir(repo))) and \
                subprocess.run([sys.executable, "-m", "pytest", "--version"], capture_output=True).returncode == 0:
            cmds.append(("pytest", [sys.executable, "-m", "pytest", "-q"]))
    if has("Cargo.toml") and shutil.which("cargo"):
        cmds.append(("cargo test", ["cargo", "test", "--quiet"]))
    return cmds


def main():
    a = sys.argv[1:]
    if not a:
        sys.exit(__doc__)
    repo = os.path.abspath(a[0])
    base = a[a.index("--base") + 1] if "--base" in a else None
    out = a[a.index("--out") + 1] if "--out" in a else "facts.json"
    rng = diff_range(repo, base)
    files = parse_diff(git(repo, "diff", "--unified=0", "--no-color", *rng))
    secrets, debug, todos, big = [], [], [], []
    for f, d in files.items():
        for ln, t in d["added"]:
            for name, rx in SECRETS:
                if re.search(rx, t):
                    secrets.append({"file": f, "line": ln, "kind": name})
            if re.search(DEBUG, t):
                debug.append({"file": f, "line": ln, "text": t.strip()[:80]})
            if re.search(r"\b(TODO|FIXME|HACK)\b", t):
                todos.append({"file": f, "line": ln, "text": t.strip()[:80]})
        p = os.path.join(repo, f)
        if os.path.exists(p) and os.path.getsize(p) > 1_000_000:
            big.append({"file": f, "mb": round(os.path.getsize(p) / 1e6, 1)})
    tools = []
    if "--no-tests" not in a:
        for name, cmd in checks_for(repo):
            try:
                # no keyboard input and breakpoints off, so a stray breakpoint() cannot hang the review
                r = subprocess.run(cmd, cwd=repo, capture_output=True, text=True, timeout=600, stdin=subprocess.DEVNULL,
                                   env=dict(os.environ, PYTHONBREAKPOINT="0", CI="1"))
                tools.append({"name": name, "ok": r.returncode == 0, "output": "\n".join((r.stdout + r.stderr).strip().splitlines()[-25:])})
            except subprocess.TimeoutExpired:
                tools.append({"name": name, "ok": False, "output": "timed out after 10 minutes"})
    facts = {"repo": os.path.basename(repo), "range": " ".join(rng),
             "files": {f: {"added_lines": [ln for ln, _ in d["added"]], "added": len(d["added"]), "removed": d["removed"]} for f, d in files.items()},
             "tools": tools, "secrets": secrets, "debug": debug, "todos": todos, "large_files": big}
    with open(out, "w", encoding="utf-8") as f:
        json.dump(facts, f, indent=1)
    failed = [t["name"] for t in tools if not t["ok"]]
    print("%d files changed (+%d -%d); checks: %s; secrets: %d" % (
        len(files), sum(d["added"] for d in facts["files"].values()), sum(d["removed"] for d in facts["files"].values()),
        ", ".join("%s %s" % (t["name"], "ok" if t["ok"] else "FAILED") for t in tools) or "none found", len(secrets)))
    if failed:
        print("failing: " + ", ".join(failed))
    print("wrote " + out)


if __name__ == "__main__":
    main()
