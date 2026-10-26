"""Ask several AI models from different makers the same question, then let
each answer the others, so the chair can decide with the disagreement in view.

Usage: python3 council.py question.md [--voices 3] [--rounds 2] [--models a,b,c] [--out council]
question.md holds the question, the options and the facts. Each voice gets a
role (advocate, critic, fact-checker, risk officer, the people affected) and
answers in a fixed shape: position, confidence, reasons, risks, what would
change its mind. In round 2 each voice reads the others' answers (by letter,
not by model) and keeps or changes its position. Models come from MangoMan's
router (MANGOMAN_URL and MANGOMAN_TOKEN, set in every MangoMan session), one
per maker, so they do not share the same blind spots. Writes council.json and
council.md. Prints the number of AI requests before asking.
"""
import concurrent.futures
import datetime as dt
import json
import os
import re
import sys
import urllib.error
import urllib.request

OUT = "council"
MAX_QUESTION = 24000  # characters: every voice reads it every round
MAX_VOICES = 5
TIMEOUT = 180
ROLES = [
    ("Advocate", "Make the strongest honest case for the option you think is best."),
    ("Critic", "Find the weaknesses in the obvious answer and the options others favour."),
    ("Fact-checker", "Test every claim against the facts given; say what is unknown or assumed."),
    ("Risk officer", "Say what could go wrong, how likely, how bad, and how to limit it."),
    ("The people affected", "Judge it as the customers, staff or users affected would."),
]
SHAPE = """Answer in exactly this shape, in plain words:
POSITION: <your answer in one line>
CONFIDENCE: <a whole number from 0 to 100>
REASONS:
- <reason, citing the facts given>
RISKS:
- <risk>
WOULD CHANGE MY MIND:
- <what new fact would make you answer differently>
Use only the facts given and general knowledge; mark anything you assume as an assumption. Do not invent numbers."""

# Who makes a model, from its name, so the council mixes makers. A name not
# listed counts as its own maker.
MAKERS = [
    (r"^(gpt|o\d)", "OpenAI"), (r"^claude", "Anthropic"), (r"^(gemini|gemma)", "Google"),
    (r"^(llama|meta-llama)", "Meta"), (r"^qwen", "Alibaba"), (r"^deepseek", "DeepSeek"),
    (r"^(mistral|mixtral|codestral|devstral|magistral|ministral)", "Mistral"), (r"^nemotron", "NVIDIA"),
    (r"^(glm|chatglm)", "Zhipu"), (r"^kimi", "Moonshot"), (r"^grok", "xAI"), (r"^(command|aya)", "Cohere"),
    (r"^phi", "Microsoft"), (r"^minimax", "MiniMax"), (r"^ernie", "Baidu"),
]
SMALL = re.compile(r"(mini|nano|tiny|lightning|-xs\b|xs-)", re.I)


def maker(model):
    name = model.lower().split("/")[-1]
    for rx, who in MAKERS:
        if re.search(rx, name):
            return who
    return re.split(r"[-_.\d]", name, maxsplit=1)[0] or name


def strength(model):
    """A rough order within one maker: bigger models first, small ones last."""
    if SMALL.search(model):
        return 0
    sizes = [float(x) for x in re.findall(r"(\d+(?:\.\d+)?)b\b", model.lower())]
    return max(sizes) if sizes else 100


def pick(models, n):
    """The strongest model of each maker, strongest makers first, n at most."""
    best = {}
    for m in models:
        k = maker(m)
        if k not in best or strength(m) > strength(best[k]):
            best[k] = m
    return sorted(best.values(), key=lambda m: (-strength(m), m))[:n]


def router():
    url, token = os.environ.get("MANGOMAN_URL", "").rstrip("/"), os.environ.get("MANGOMAN_TOKEN", "")
    if not url or not token:
        sys.exit("MANGOMAN_URL and MANGOMAN_TOKEN are not set: run this inside a MangoMan coding session")
    return url, token


def call(url, token, path, body=None):
    req = urllib.request.Request(url + path, data=json.dumps(body).encode() if body is not None else None,
                                 headers={"Authorization": "Bearer " + token, "Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=TIMEOUT) as r:
        return json.loads(r.read().decode("utf-8", "replace"))


def available(url, token):
    """Models the router can reach now (not its virtual names or groups)."""
    data = call(url, token, "/models").get("data") or []
    return sorted(m["id"] for m in data if isinstance(m, dict) and m.get("providers"))


def why(e):
    if isinstance(e, urllib.error.HTTPError):
        try:
            msg = json.loads(e.read().decode("utf-8", "replace"))["error"]["message"]
        except Exception:
            msg = ""
        return "the model answered %d%s" % (e.code, ": " + msg[:160] if msg else "")
    return "no answer (%s)" % str(getattr(e, "reason", e))[:160]


def ask(url, token, model, messages):
    """(text, tokens, error)."""
    try:
        r = call(url, token, "/chat/completions", {"model": "strict/" + model, "messages": messages, "temperature": 0.4})
        text = (((r.get("choices") or [{}])[0].get("message") or {}).get("content") or "").strip()
        used = (r.get("usage") or {}).get("total_tokens") or 0
        return (text, used, "") if text else ("", used, "the model gave an empty answer")
    except (urllib.error.URLError, OSError, ValueError, KeyError, TypeError, IndexError) as e:
        return "", 0, why(e)


def parse(text):
    """The fixed-shape answer as fields; missing parts stay empty."""
    out = {"position": "", "confidence": None, "reasons": [], "risks": [], "change_mind": []}
    key = None
    heads = {"REASONS": "reasons", "RISKS": "risks", "WOULD CHANGE MY MIND": "change_mind"}
    for line in text.splitlines():
        s = line.strip()
        m = re.match(r"[*_#\s]*(POSITION|CONFIDENCE|REASONS|RISKS|WOULD CHANGE MY MIND)[*_\s]*:[*_\s]*(.*)$", s, re.I)
        if m:
            head, rest = m.group(1).upper(), m.group(2).strip().strip("*").strip()
            if head == "POSITION":
                out["position"], key = rest, None
            elif head == "CONFIDENCE":
                n = re.search(r"\d{1,3}", rest)
                out["confidence"] = min(100, int(n.group(0))) if n else None
                key = None
            else:
                key = heads[head]
                if rest:
                    out[key].append(rest)
        elif key and re.match(r"([-*•]|\d+[.)])\s", s):
            out[key].append(re.sub(r"^([-*•]|\d+[.)])\s*", "", s))
    return out


def brief(question, role, mission):
    return [{"role": "system", "content": "You are one voice on a council that advises a business owner. Your role: %s. %s\n\n%s" % (role, mission, SHAPE)},
            {"role": "user", "content": question}]


def others_text(voices, me, rnd):
    parts = []
    for v in voices:
        if v is me or not v["rounds"][rnd]["text"]:
            continue
        parts.append("Voice %s (%s):\n%s" % (v["label"], v["role"], v["rounds"][rnd]["text"]))
    return "\n\n".join(parts)


def main():
    a = sys.argv[1:]
    if not a or a[0].startswith("--"):
        sys.exit(__doc__)
    try:
        opt = {k: a[a.index(k) + 1] for k in ("--voices", "--rounds", "--models", "--out") if k in a}
        n, rounds = int(opt.get("--voices", 3)), int(opt.get("--rounds", 2))
    except (IndexError, ValueError):
        sys.exit("--voices and --rounds need whole numbers, --models a list and --out a folder")
    if not 2 <= n <= MAX_VOICES or rounds not in (1, 2):
        sys.exit("--voices must be 2 to %d and --rounds 1 or 2" % MAX_VOICES)
    question = open(a[0], encoding="utf-8").read().strip()
    if not question:
        sys.exit("%s is empty: write the question, the options and the facts first" % a[0])
    if len(question) > MAX_QUESTION:
        sys.exit("%s has %d characters; every voice reads it every round, so keep it under %d: summarise the facts"
                 % (a[0], len(question), MAX_QUESTION))
    out = opt.get("--out", OUT)
    url, token = router()
    try:
        models = [m.strip() for m in opt["--models"].split(",") if m.strip()] if "--models" in opt else pick(available(url, token), n)
    except (urllib.error.URLError, OSError, ValueError) as e:
        sys.exit("could not read the models from MangoMan (%s); is MangoMan running?" % why(e))
    if len(models) < 2:
        sys.exit("only %d model can be reached now; a council needs at least 2. Add another free provider in Connections." % len(models))
    voices = [{"label": chr(65 + i), "model": m, "maker": maker(m), "role": ROLES[i][0], "mission": ROLES[i][1], "rounds": []}
              for i, m in enumerate(models[:n])]
    print("Council: %d voices (%s), %d round(s), %d AI requests." % (
        len(voices), ", ".join("%s %s" % (v["label"], v["maker"]) for v in voices), rounds, len(voices) * rounds))
    used = 0
    with concurrent.futures.ThreadPoolExecutor(max_workers=len(voices)) as pool:
        for rnd in range(rounds):
            jobs = {}
            for v in voices:
                msgs = brief(question, v["role"], v["mission"])
                if rnd == 1:
                    if not v["rounds"][0]["text"]:
                        v["rounds"].append({"text": "", "ok": False, "why": "no first answer", **parse("")})
                        continue
                    msgs += [{"role": "assistant", "content": v["rounds"][0]["text"]},
                             {"role": "user", "content": "The other voices answered:\n\n%s\n\nReply in the same shape. Keep or change your position; "
                                                         "in REASONS say which voice's point moved you or why it did not." % others_text(voices, v, 0)}]
                jobs[pool.submit(ask, url, token, v["model"], msgs)] = v
            for f in concurrent.futures.as_completed(jobs):
                v = jobs[f]
                text, tokens, err = f.result()
                used += tokens
                v["rounds"].append({"text": text, "ok": not err, "why": err, **parse(text)})
            for v in voices:  # as_completed may finish in any order; rounds stay in order per voice
                assert len(v["rounds"]) == rnd + 1
            answered = [v for v in voices if v["rounds"][rnd]["ok"]]
            if rnd == 0 and len(answered) < 2:
                sys.exit("only %d voice answered (%s); try again in a few minutes"
                         % (len(answered), "; ".join("%s: %s" % (v["model"], v["rounds"][0]["why"]) for v in voices if not v["rounds"][0]["ok"])))
    record = {"question_file": os.path.basename(a[0]), "asked_at": dt.datetime.now(dt.timezone.utc).isoformat(timespec="seconds"),
              "rounds": rounds, "tokens": used, "voices": [{k: v[k] for k in ("label", "model", "maker", "role", "rounds")} for v in voices]}
    os.makedirs(out, exist_ok=True)
    with open(os.path.join(out, OUT + ".json"), "w", encoding="utf-8") as f:
        json.dump(record, f, ensure_ascii=False, indent=1)
    with open(os.path.join(out, OUT + ".md"), "w", encoding="utf-8") as f:
        f.write(markdown(record))
    print("wrote %s.json and %s.md: %d of %d voices answered, %s tokens" % (
        OUT, OUT, sum(v["rounds"][-1]["ok"] for v in voices), len(voices), "{:,}".format(used)))


def markdown(r):
    md = ["# Council", "", "Asked %s. %d voices, %d round(s)." % (r["asked_at"][:10], len(r["voices"]), r["rounds"]), ""]
    for v in r["voices"]:
        md += ["## Voice %s: %s (%s, %s)" % (v["label"], v["role"], v["model"], v["maker"]), ""]
        for i, x in enumerate(v["rounds"], 1):
            md += ["### Round %d" % i, ""]
            md += [x["text"] if x["ok"] else "No answer: %s" % x["why"], ""]
    return "\n".join(md)


if __name__ == "__main__":
    main()
