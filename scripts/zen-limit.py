"""Measure OpenCode's free-model allowance: how many requests one internet
address gets before OpenCode answers FreeUsageLimitError, and which free
models share that count. Uses the OpenCode release MangoMan ships, logged
out, exactly as a user's computer would; each message is one tiny request
("reply OK"), with chat titles off so no hidden second request is sent.

Usage: python3 zen-limit.py [model] [max requests] [parallel]
Writes report.md. Runs until the limit or max requests, whichever is first.
"""
import concurrent.futures
import datetime as dt
import json
import os
import statistics
import subprocess
import sys
import threading
import time

PROMPT = "Reply with exactly the word OK and nothing else. Do not use any tools."
CONFIG = json.dumps({"agent": {"title": {"disable": True}}, "share": "disabled", "autoupdate": False})
PER_RUN = 90  # seconds: OpenCode retries the free limit for hours, so a run that long is blocked
LIMIT_WORDS = ("FreeUsageLimitError", "Free usage exceeded", "Free usage limit", "Free limit reached")
IST = dt.timezone(dt.timedelta(hours=5, minutes=30))


def env():
    e = dict(os.environ, OPENCODE_CONFIG_CONTENT=CONFIG, OPENCODE_DISABLE_AUTOUPDATE="1")
    e.pop("OPENCODE_API_KEY", None)
    return e


def free_models():
    out = subprocess.run(["opencode", "models", "opencode"], capture_output=True, text=True, env=env(), timeout=120).stdout
    return [line.strip() for line in out.splitlines() if line.strip().startswith("opencode/")]


def ping(model, work):
    """(outcome, requests, seconds, note): outcome ok, limit or failed."""
    start = time.time()
    try:
        p = subprocess.run(["opencode", "run", "--format", "json", "--print-logs", "--log-level", "INFO",
                            "-m", model, PROMPT], cwd=work, capture_output=True, text=True, env=env(), timeout=PER_RUN)
        out, err = p.stdout, p.stderr
    except subprocess.TimeoutExpired as e:
        out = e.stdout.decode("utf-8", "replace") if isinstance(e.stdout, bytes) else (e.stdout or "")
        err = e.stderr.decode("utf-8", "replace") if isinstance(e.stderr, bytes) else (e.stderr or "")
        err += "\n[timed out]"
    took = time.time() - start
    steps, text, errors = 0, "", []
    for line in out.splitlines():
        try:
            ev = json.loads(line)
        except ValueError:
            continue
        if ev.get("type") == "step_finish":
            steps += 1
        elif ev.get("type") == "text":
            text += ((ev.get("part") or {}).get("text") or "")
        elif ev.get("type") == "error":
            errors.append(json.dumps(ev.get("error"))[:300])
    blob = out + err
    if any(w in blob for w in LIMIT_WORDS):
        after = ""
        for line in blob.splitlines():
            if "retry-after" in line.lower():
                after = line.strip()[:200]
                break
        return "limit", steps, took, after
    if text.strip():
        return "ok", steps, took, ""
    note = (errors[0] if errors else err.strip().splitlines()[-1] if err.strip() else "no answer")[:300]
    return "failed", steps, took, note


def main():
    a = sys.argv[1:]
    models = free_models()
    if not models:
        sys.exit("OpenCode lists no free models (logged out); is it installed and online?")
    model = a[0] if a and a[0] else next((m for m in models if "deepseek" in m), models[0])
    most = int(a[1]) if len(a) > 1 else 3000
    par = int(a[2]) if len(a) > 2 else 4
    work = os.path.abspath("zen-work")
    os.makedirs(work, exist_ok=True)
    began = dt.datetime.now(IST)
    print("free models: %s\nmeasuring %s, up to %d requests, %d at a time" % (", ".join(models), model, most, par), flush=True)

    lock = threading.Lock()
    s = {"sent": 0, "ok": 0, "requests": 0, "failed": 0, "limit": None, "secs": [], "fails": []}

    def worker():
        while True:
            with lock:
                if s["limit"] or s["sent"] >= most:
                    return
                s["sent"] += 1
            outcome, steps, took, note = ping(model, work)
            with lock:
                s["requests"] += steps
                if outcome == "ok":
                    s["ok"] += 1
                    s["secs"].append(took)
                    if s["ok"] % 25 == 0:
                        print("%s  %d answered, %d requests" % (dt.datetime.now(IST).strftime("%H:%M:%S"), s["ok"], s["requests"]), flush=True)
                elif outcome == "limit":
                    if not s["limit"]:
                        s["limit"] = {"at": dt.datetime.now(IST), "note": note}
                        print("limit reached after %d answers (%d requests): %s" % (s["ok"], s["requests"], note), flush=True)
                else:
                    s["failed"] += 1
                    if len(s["fails"]) < 5:
                        s["fails"].append(note)

    with concurrent.futures.ThreadPoolExecutor(par) as ex:
        for f in [ex.submit(worker) for _ in range(par)]:
            f.result()

    others = []
    if s["limit"]:
        for m in models:
            if m != model:
                outcome, _, took, note = ping(m, work)
                others.append((m, outcome, took, note))
                print("after the limit, %s: %s" % (m, outcome), flush=True)

    ended = dt.datetime.now(IST)
    r = ["# OpenCode free-model allowance", "",
         "Run %s to %s IST on a GitHub runner (one internet address), OpenCode %s, logged out, chat titles off. "
         "Each message is one tiny request." % (began.strftime("%d %b %H:%M"), ended.strftime("%H:%M"),
                                                 subprocess.run(["opencode", "--version"], capture_output=True, text=True, env=env()).stdout.strip()),
         "", "| Measure | Result |", "| --- | --- |",
         "| Model measured | %s |" % model,
         "| Messages answered before the limit | %s |" % (s["ok"] if s["limit"] else "%d (no limit reached within %d)" % (s["ok"], most)),
         "| Model requests counted (steps) | %d |" % s["requests"],
         "| Limit reached at | %s |" % (s["limit"]["at"].strftime("%d %b %H:%M:%S IST") if s["limit"] else "not reached"),
         "| What OpenCode said | %s |" % ((s["limit"] or {}).get("note") or "").replace("|", "/"),
         "| Other failures (not the limit) | %d |" % s["failed"],
         "| Typical answer time | %s |" % ("%.1f s (median)" % statistics.median(s["secs"]) if s["secs"] else "none"),
         "", "Free models offered logged out: %s" % ", ".join(models)]
    if others:
        r += ["", "## After the limit: the other free models", "", "| Model | Result | Note |", "| --- | --- | --- |"]
        r += ["| %s | %s | %s |" % (m, o, n.replace("|", "/")) for m, o, _, n in others]
    if s["fails"]:
        r += ["", "## Sample failures", ""] + ["- " + f.replace("\n", " ") for f in s["fails"]]
    r += ["", "Caveats: GitHub runners share addresses with other users' jobs, so the count may start part used. "
          "OpenCode says the allowance changes with demand, and new addresses get more for about a week."]
    with open("report.md", "w", encoding="utf-8") as f:
        f.write("\n".join(r) + "\n")
    print("\n".join(r))


if __name__ == "__main__":
    main()
