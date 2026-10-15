#!/usr/bin/env python3
"""Checks NVIDIA's free models live: which are listed, and how fast each
answers a short coding question. Writes report.md.

  NVIDIA_API_KEY=... python3 scripts/nvidia-check.py
"""
import concurrent.futures as cf
import json
import os
import re
import time
import urllib.request

BASE = "https://integrate.api.nvidia.com/v1"
HERE = os.path.dirname(os.path.abspath(__file__))
# Chat model families worth a look for coding, besides the catalogue's own.
FAMILIES = re.compile(r"(kimi|deepseek|glm|qwen.*coder|qwen3|nemotron-3|gpt-oss|gemma-4|laguna|minimax|mistral-(medium|large)-3|devstral)", re.I)


def call(path, body=None, timeout=90):
    req = urllib.request.Request(BASE + path, json.dumps(body).encode() if body else None,
                                 {"authorization": "Bearer " + os.environ["NVIDIA_API_KEY"], "content-type": "application/json"})
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return json.loads(r.read())


def ask(model):
    body = {"model": model, "max_tokens": 400, "temperature": 0.2, "stream": False,
            "messages": [{"role": "user", "content": "Write a Python function that returns the sum of a list. Reply with only the code."}]}
    start = time.time()
    try:
        out = call("/chat/completions", body)
        text = (out["choices"][0]["message"].get("content") or "").strip()
        return model, time.time() - start, "ok" if "def " in text else "answered without code", text[:80].replace("\n", " ")
    except Exception as e:  # noqa: BLE001 - every failure is a result here
        return model, time.time() - start, type(e).__name__, str(e)[:120]


def main():
    listed = sorted(m["id"] for m in call("/models")["data"])
    seed = json.load(open(os.path.join(HERE, "..", "internal", "catalogue", "seed.json")))
    ours = [m["upstream"] for m in seed["models"] if m["provider"] == "nvidia"]
    picks = sorted(set(ours) | {m for m in listed if FAMILIES.search(m)})
    with cf.ThreadPoolExecutor(6) as pool:
        results = list(pool.map(ask, picks))
    lines = [f"# NVIDIA check, {time.strftime('%Y-%m-%d %H:%M UTC', time.gmtime())}", "",
             "| Model | In our catalogue | Listed by NVIDIA | Result | Seconds | Start of answer or error |", "| --- | --- | --- | --- | --- | --- |"]
    for model, secs, res, text in sorted(results, key=lambda r: (r[2] != "ok", r[1])):
        lines.append(f"| {model} | {'yes' if model in ours else ''} | {'yes' if model in listed else 'no'} | {res} | {secs:.1f} | {text.replace('|', '/')} |")
    lines += ["", f"All {len(listed)} listed models:", "", ", ".join(listed)]
    open("report.md", "w").write("\n".join(lines) + "\n")
    print("\n".join(lines[:60]))


if __name__ == "__main__":
    main()
