#!/usr/bin/env python3
"""Tests the web search MangoMan's coding helper uses, against free models.

For each question in search-test.json it asks two keyless search services
(Exa, which MangoMan turns on, and Parallel, OpenCode's other choice), notes
whether the right answer is in what came back and how long it took, then asks
each model three ways: from memory, with the Exa results, with the Parallel
results. An answer is right, "don't know", or wrong. Writes report.md.

  NVIDIA_API_KEY=... python3 scripts/search-test.py [models...]
"""
import concurrent.futures as cf
import json
import os
import re
import sys
import time
import unicodedata
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
NVIDIA = "https://integrate.api.nvidia.com/v1/chat/completions"
MODELS = ["moonshotai/kimi-k3", "deepseek-ai/deepseek-v4.1-flash", "nvidia/nemotron-3-ultra-550b-a55b",
          "z-ai/glm-5.3-flash", "google/gemma-4-31b-it"]
SEARCH = {
    "exa": ("https://mcp.exa.ai/mcp", "web_search_exa",
            lambda q: {"query": q, "type": "auto", "numResults": 8, "livecrawl": "fallback"}),
    "parallel": ("https://search.parallel.ai/mcp", "web_search",
                 lambda q: {"objective": q, "search_queries": [q], "session_id": "mangoman-search-test"}),
}
TODAY = time.strftime("%d %B %Y")


def norm(s):
    s = unicodedata.normalize("NFKD", s.lower())
    return "".join(c for c in s if not unicodedata.combining(c))


def right(text, answers):
    t = norm(text)
    return any(re.search(norm(a), t) for a in answers)


def post(url, body, headers, timeout):
    req = urllib.request.Request(url, json.dumps(body).encode(), {"content-type": "application/json", **headers})
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return r.read().decode("utf-8", "replace")


def search(name, q):
    url, tool, args = SEARCH[name]
    body = {"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": tool, "arguments": args(q)}}
    start = time.time()
    try:
        raw = post(url, body, {"accept": "application/json, text/event-stream", "user-agent": "mangoman-search-test"}, 40)
    except Exception as e:  # noqa: BLE001 - every failure is a result here
        return "", time.time() - start, type(e).__name__ + ": " + str(e)[:120]
    # The reply is JSON or server-sent events; the text is in result.content.
    for chunk in [raw] + [ln[6:] for ln in raw.splitlines() if ln.startswith("data: ")]:
        try:
            msg = json.loads(chunk)
        except ValueError:
            continue
        if "error" in msg:
            return "", time.time() - start, str(msg["error"])[:160]
        for c in msg.get("result", {}).get("content", []):
            if c.get("text"):
                return c["text"], time.time() - start, ""
    return "", time.time() - start, "no text in reply"


def ask(model, q, context):
    if context:
        prompt = (f"Today is {TODAY}. Answer the question from these web search results. "
                  f"Give the answer in one short sentence. If the results do not say, reply exactly: I don't know.\n\n"
                  f"Search results:\n{context[:12000]}\n\nQuestion: {q}")
    else:
        prompt = (f"Today is {TODAY}. Answer in one short sentence. "
                  f"If you are not sure, reply exactly: I don't know.\n\nQuestion: {q}")
    body = {"model": model, "messages": [{"role": "user", "content": prompt}], "max_tokens": 4000, "temperature": 0}
    for attempt in range(3):
        try:
            raw = post(NVIDIA, body, {"authorization": "Bearer " + os.environ["NVIDIA_API_KEY"]}, 180)
            msg = json.loads(raw)["choices"][0]["message"]
            return (msg.get("content") or "").strip(), ""
        except Exception as e:  # noqa: BLE001
            err = type(e).__name__ + ": " + str(e)[:120]
            time.sleep(10 * (attempt + 1))
    return "", err


def grade(answer, err, answers):
    if err or not answer:
        return "failed"
    if "don't know" in answer.lower() or "do not know" in answer.lower():
        return "dont_know"  # a guess after "I don't know" is not counted as right
    if right(answer, answers):
        return "right"
    return "wrong"


def main():
    models = sys.argv[1:] or MODELS
    qs = json.load(open(os.path.join(HERE, "search-test.json")))["questions"]
    found = {}
    lines = [f"# Search test, {TODAY}", "", f"{len(qs)} questions, {len(models)} models.", "",
             "## 1. Search services on their own", "",
             "| Service | Answer in results | Failed | Median seconds | Slowest |", "| --- | --- | --- | --- | --- |"]
    detail = []
    for name in SEARCH:
        hits, fails, times = 0, 0, []
        for q in qs:
            text, secs, err = search(name, q["q"])
            found[(name, q["q"])] = text
            hit = bool(text) and right(text, q["a"])
            hits += hit
            fails += bool(err)
            times.append(secs)
            detail.append(f"| {name} | {q['q']} | {'yes' if hit else 'no'} | {secs:.1f} | {err.replace('|', '/')} |")
            time.sleep(1)
        times.sort()
        lines.append(f"| {name} | {hits} of {len(qs)} | {fails} | {times[len(times) // 2]:.1f} | {times[-1]:.1f} |")

    def run(model):
        out = []
        for q in qs:
            for mode in ("memory", "exa", "parallel"):
                ctx = "" if mode == "memory" else found[(mode, q["q"])]
                if mode != "memory" and not ctx:
                    out.append((model, mode, q, "", "no search results"))
                    continue
                ans, err = ask(model, q["q"], ctx)
                out.append((model, mode, q, ans, err))
                time.sleep(2)
        return out

    with cf.ThreadPoolExecutor(len(models)) as pool:
        results = [r for rs in pool.map(run, models) for r in rs]

    lines += ["", "## 2. Models: right / don't know / wrong / failed", "",
              "Recent questions only (the two control questions are counted apart).", "",
              "| Model | From memory | With Exa | With Parallel | Controls from memory |", "| --- | --- | --- | --- | --- |"]
    for model in models:
        row = [model.split("/")[-1]]
        for mode in ("memory", "exa", "parallel"):
            g = [grade(a, e, q["a"]) for m, md, q, a, e in results if m == model and md == mode and not q.get("control")]
            row.append(" / ".join(str(g.count(k)) for k in ("right", "dont_know", "wrong", "failed")))
        c = [grade(a, e, q["a"]) for m, md, q, a, e in results if m == model and md == "memory" and q.get("control")]
        row.append(f"{c.count('right')} of {len(c)}")
        lines.append("| " + " | ".join(row) + " |")
    lines += ["", "## 3. Search detail", "", "| Service | Question | Answer in results | Seconds | Error |",
              "| --- | --- | --- | --- | --- |"] + detail
    lines += ["", "## 4. Every answer", "", "| Model | Mode | Question | Grade | Answer |", "| --- | --- | --- | --- | --- |"]
    for m, md, q, a, e in results:
        a1 = (a or e).replace("|", "/").replace("\n", " ")[:200]
        lines.append(f"| {m.split('/')[-1]} | {md} | {q['q']} | {grade(a, e, q['a'])} | {a1} |")
    open("report.md", "w").write("\n".join(lines) + "\n")
    print("\n".join(lines[:30]))


if __name__ == "__main__":
    main()
