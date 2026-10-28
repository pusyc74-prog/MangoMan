"""Ask several AI models from different makers the same question, then let
each answer the others, so the chair can decide with the disagreement in view.

Usage: python3 council.py question.md [--voices 3] [--rounds 2] [--models a,b,c] [--out council] [--plan]
question.md holds the question, the options and the facts. Each voice gets a
role (advocate, critic, fact-checker, risk officer, the people affected) and
answers in a fixed shape: position, confidence, reasons, risks, what would
change its mind. In round 2 each voice reads the others' answers (by letter,
not by model) and keeps or changes its position. Models come from where the
MangoMan session gets its AI (MANGOMAN_ENGINE, set in every session): OpenCode's
own free models through the OpenCode program (MANGOMAN_OPENCODE), the default,
or MangoMan's switch (MANGOMAN_URL and MANGOMAN_TOKEN). One per maker, so they
do not share the same blind spots; a model that cannot answer is replaced by
the next maker's. --plan only prints the voices and the number of AI
requests. Progress is saved after every round, so a run that was stopped
picks up where it ended. Writes council.json and council.md.
"""
import concurrent.futures
import datetime as dt
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request

OUT = "council"
MAX_QUESTION = 12000  # characters: every voice reads it every round, and small free models allow few tokens a minute
MAX_VOICES = 5
TIMEOUT = 200  # seconds: just over the router's own 180 s for a whole answer, so its retries can finish
WAIT_MAX = 90  # seconds to wait once when the router says free capacity returns soon
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
    (r"^phi", "Microsoft"), (r"^minimax", "MiniMax"), (r"^ernie", "Baidu"), (r"^mimo", "Xiaomi"), (r"^big-pickle", "OpenCode"),
]
SMALL = re.compile(r"(^|[-_.:])(mini|nano|tiny|lightning|xs|small)([-_.:]|$)", re.I)
HEADS = {"POSITION": "position", "CONFIDENCE": "confidence", "REASONS": "reasons", "RISKS": "risks",
         "WOULD CHANGE MY MIND": "change_mind"}
HEAD = re.compile(r"^[\s#*_>\d.)-]*(POSITION|CONFIDENCE|REASONS|RISKS|WOULD CHANGE MY MIND)\b[*_\s]*(:[*_\s]*(.*))?$", re.I)
BULLET = re.compile(r"^([-*+•]|\d+[.)])\s+")


def maker(model):
    name = model.lower().split("/")[-1]
    for rx, who in MAKERS:
        if re.search(rx, name):
            return who
    return re.split(r"[-_.:\d]", name, maxsplit=1)[0] or name


def strength(model):
    """A rough order: bigger models first, small ones last, unsized in between."""
    if SMALL.search(model.lower().split("/")[-1]):
        return 0
    sizes = [float(x) for x in re.findall(r"(\d+(?:\.\d+)?)b\b", model.lower())]
    return max(sizes) if sizes else 50


def ranked(models):
    """Every model, the strongest of each maker first (makers by their best),
    then the rest, so a model that cannot answer can be swapped for the next."""
    best = {}
    for m in models:
        k = maker(m)
        if k not in best or (strength(m), m) > (strength(best[k]), best[k]):
            best[k] = m
    first = sorted(best.values(), key=lambda m: (-strength(m), m))
    return first + sorted((m for m in models if m not in first), key=lambda m: (-strength(m), m))


FREE_LIMIT = "free limit"  # OpenCode's free daily limit: no other free model can answer either
# OpenCode's own log line at that limit (as MangoMan's opencode.FreeLimitLine reads it).
LIMIT_LINE = re.compile(r"(?im)^timestamp=\S+ level=\S+ .*providerID=opencode\b.*\berror(?:\.error)?=\"AI_\w+: "
                        r"(?:Failed after \d+ attempts\. Last error: )?(?:Rate limit exceeded\. Please try again later|Free usage (?:limit|exceeded))")


def source():
    """(list the models, ask one): OpenCode's own free models (the default),
    or MangoMan's switch when the session's engine is "switch"."""
    if os.environ.get("MANGOMAN_ENGINE", "").strip().lower() != "switch":
        oc = os.environ.get("MANGOMAN_OPENCODE") or shutil.which("opencode")
        if not oc:
            sys.exit("OpenCode is not set up yet: run this inside a MangoMan coding session")
        return (lambda: opencode_models(oc)), (lambda model, messages: ask_opencode(oc, model, messages))
    url, token = os.environ.get("MANGOMAN_URL", "").rstrip("/"), os.environ.get("MANGOMAN_TOKEN", "")
    if not url or not token:
        sys.exit("MANGOMAN_URL and MANGOMAN_TOKEN are not set: run this inside a MangoMan coding session")
    return (lambda: available(url, token)), (lambda model, messages: ask(url, token, model, messages))


def opencode_models(oc):
    """OpenCode's own free models, as OpenCode lists them logged out."""
    r = subprocess.run([oc, "models", "opencode"], capture_output=True, text=True, timeout=120)
    return sorted(line.strip() for line in r.stdout.splitlines() if line.strip().startswith("opencode/"))


def ask_opencode(oc, model, messages):
    """(text, tokens, error) from one OpenCode run: no tools, the voice's role
    as its instructions, the question (and in round 2 the first answer and the
    others') on standard input, from an empty folder."""
    prompt = messages[1]["content"]
    if len(messages) > 2:
        prompt += "\n\nYour first answer was:\n\n%s\n\n%s" % (messages[2]["content"], messages[3]["content"])
    config = json.dumps({"enabled_providers": ["opencode"], "share": "disabled", "autoupdate": False, "agent": {
        "title": {"disable": True},
        "mangoman-voice": {"mode": "primary", "description": "One voice of MangoMan's council",
                           "prompt": messages[0]["content"], "permission": {"*": "deny"}}}})
    env = dict(os.environ, OPENCODE_CONFIG_CONTENT=config, OPENCODE_PRINT_LOGS="1", OPENCODE_LOG_LEVEL="ERROR",
               OPENCODE_DISABLE_AUTOUPDATE="1")
    # At the limit OpenCode keeps waiting for hours: its log line ends the run.
    with tempfile.TemporaryDirectory(prefix="mangoman-voice-") as work:
        try:
            p = subprocess.Popen([oc, "run", "--format", "json", "--agent", "mangoman-voice", "-m", model], stdin=subprocess.PIPE,
                                 stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, encoding="utf-8", errors="replace",
                                 cwd=work, env=env)
        except OSError as e:
            return "", 0, "no answer (%s)" % type(e).__name__
        out, logs, limit = [], [], threading.Event()

        def read_logs():
            for line in p.stderr:
                logs.append(line)
                if LIMIT_LINE.search(line):
                    limit.set()
                    p.kill()
        readers = [threading.Thread(target=read_logs, daemon=True),
                   threading.Thread(target=lambda: out.append(p.stdout.read()), daemon=True)]
        for t in readers:
            t.start()
        try:
            p.stdin.write(prompt)
            p.stdin.close()
        except OSError:
            pass  # OpenCode ended early; its output says why
        try:
            p.wait(timeout=TIMEOUT)
        except subprocess.TimeoutExpired:
            p.kill()
            p.wait()
            return "", 0, "no answer (TimeoutExpired)"
        for t in readers:
            t.join(5)
    if limit.is_set():
        return "", 0, FREE_LIMIT
    text, used, failed = "", 0, ""
    for line in "".join(out).splitlines():
        try:
            ev = json.loads(line)
        except ValueError:
            continue
        part = ev.get("part") or {}
        if ev.get("type") == "text":
            text += part.get("text") or ""
        elif ev.get("type") == "step_finish":
            used += sum(v for v in (part.get("tokens") or {}).values() if isinstance(v, int))
        elif ev.get("type") == "error" and not failed:
            e = ev.get("error") or {}
            failed = ((e.get("data") or {}).get("message") or e.get("name") or "an error")[:160]
    text = clean(text)
    if text:
        return text, used, ""
    return "", used, ("the model failed: " + failed) if failed else "the model gave an empty answer"


def free_limit_message():
    reset = (dt.datetime.now(dt.timezone.utc) + dt.timedelta(days=1)).replace(hour=0, minute=0, second=0, microsecond=0)
    return ("Today's free AI messages are used up on this internet connection; they come back at %s. "
            "Run the council again then: it picks up where it stopped." % reset.astimezone().strftime("%H:%M"))


def call(url, token, path, body=None):
    req = urllib.request.Request(url + path, data=json.dumps(body).encode() if body is not None else None,
                                 headers={"Authorization": "Bearer " + token, "Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=TIMEOUT) as r:
        return json.loads(r.read().decode("utf-8", "replace"))


def available(url, token):
    """Models the router has a key for (not its virtual names or groups)."""
    data = call(url, token, "/models").get("data") or []
    return sorted(m["id"] for m in data if isinstance(m, dict) and m.get("providers"))


def why(e):
    if isinstance(e, urllib.error.HTTPError):
        try:
            msg = json.loads(e.read().decode("utf-8", "replace"))["error"]["message"]
        except Exception:
            msg = ""
        return "the model answered %d%s" % (e.code, ": " + msg[:160] if msg else "")
    return "no answer (%s: %s)" % (type(e).__name__, str(getattr(e, "reason", e))[:160])


def clean(text):
    """The answer without a model's private thinking, with plain line ends."""
    text = re.sub(r"(?is)<think>.*?</think>", "", text.replace("\r\n", "\n").replace("\r", "\n"))
    return text.strip()


def ask(url, token, model, messages):
    """(text, tokens, error); waits once when the router says capacity returns soon."""
    for attempt in (0, 1):
        try:
            r = call(url, token, "/chat/completions", {"model": "strict/" + model, "messages": messages, "temperature": 0.4})
            text = clean(((r.get("choices") or [{}])[0].get("message") or {}).get("content") or "")
            used = (r.get("usage") or {}).get("total_tokens") or 0
            return (text, used, "") if text else ("", used, "the model gave an empty answer")
        except urllib.error.HTTPError as e:
            wait = e.headers.get("Retry-After") if e.headers else None
            if attempt == 0 and e.code == 429 and wait and wait.isdigit() and int(wait) <= WAIT_MAX:
                time.sleep(int(wait))
                continue
            return "", 0, why(e)
        except Exception as e:  # a cut-off or odd answer must not lose the other voices
            return "", 0, why(e)
    return "", 0, "no answer"


def confidence(rest):
    m = re.search(r"(\d+(?:\.\d+)?)\s*(/\s*10\b|%)?", rest)
    if not m:
        return None
    n = float(m.group(1))
    if m.group(2) and m.group(2).replace(" ", "") == "/10":
        n *= 10
    elif "." in m.group(1) and n <= 1:
        n *= 100
    return int(min(100, round(n)))


def parse(text):
    """The fixed-shape answer as fields; missing parts stay empty. A head
    without a colon counts only when it stands alone on its line."""
    out = {"position": "", "confidence": None, "reasons": [], "risks": [], "change_mind": []}
    key = None
    for line in text.splitlines():
        s = line.strip()
        m = HEAD.match(s)
        if m and (m.group(2) is not None or s.strip("#*_ :").upper() == m.group(1).upper()):
            key = HEADS[m.group(1).upper()]
            rest = (m.group(3) or "").strip().strip("*_").strip()
            if key == "position":
                out["position"] = out["position"] or rest
            elif key == "confidence":
                out["confidence"] = confidence(rest) if rest else None
            elif rest:
                out[key].append(rest)
            continue
        if key == "position" and s and not out["position"]:
            out["position"] = s.strip("*_ ")
        elif key == "confidence" and s and out["confidence"] is None:
            out["confidence"] = confidence(s)
        elif key in ("reasons", "risks", "change_mind") and BULLET.match(s):
            out[key].append(BULLET.sub("", s))
    return out


def brief(question, role, mission):
    return [{"role": "system", "content": "You are one voice on a council that advises a business owner. Your role: %s. %s\n\n%s" % (role, mission, SHAPE)},
            {"role": "user", "content": question}]


def others_text(voices, me):
    return "\n\n".join("Voice %s (%s):\n%s" % (v["label"], v["role"], v["rounds"][0]["text"])
                       for v in voices if v is not me and v["rounds"] and v["rounds"][0]["ok"])


def answer(text, tokens, err):
    return {"text": text, "ok": not err, "why": err, "tokens": tokens, **parse(text)}


def save(out, record):
    os.makedirs(out, exist_ok=True)
    with open(os.path.join(out, OUT + ".json"), "w", encoding="utf-8", newline="\n") as f:
        json.dump(record, f, ensure_ascii=False, indent=1)
    with open(os.path.join(out, OUT + ".md"), "w", encoding="utf-8", newline="\n") as f:
        f.write(markdown(record))


def resumable(out, digest, rounds):
    """A saved council for the same question that is not finished yet."""
    try:
        with open(os.path.join(out, OUT + ".json"), encoding="utf-8") as f:
            r = json.load(f)
        return r if r.get("question_sha") == digest and r.get("rounds") == rounds and r.get("done", 0) < rounds else None
    except (OSError, ValueError, AttributeError):
        return None


def main():
    a = sys.argv[1:]
    if not a or a[0].startswith("--"):
        sys.exit(__doc__)
    try:
        opt = {k: a[a.index(k) + 1] for k in ("--voices", "--rounds", "--models", "--out") if k in a}
        named = [m.strip() for m in opt["--models"].split(",") if m.strip()] if "--models" in opt else []
        n, rounds = int(opt.get("--voices", len(named) or 3)), int(opt.get("--rounds", 2))
    except (IndexError, ValueError):
        sys.exit("--voices and --rounds need whole numbers, --models a list and --out a folder")
    if not 2 <= n <= MAX_VOICES or rounds not in (1, 2):
        sys.exit("--voices must be 2 to %d and --rounds 1 or 2" % MAX_VOICES)
    with open(a[0], encoding="utf-8") as f:
        question = f.read().strip()
    if not question:
        sys.exit("%s is empty: write the question, the options and the facts first" % a[0])
    if len(question) > MAX_QUESTION:
        sys.exit("%s has %d characters; every voice reads it every round, so keep it under %d: summarise the facts"
                 % (a[0], len(question), MAX_QUESTION))
    out = opt.get("--out", OUT)
    digest = hashlib.sha1(question.encode("utf-8")).hexdigest()
    models, ask_model = source()
    record = resumable(out, digest, rounds)
    if record:
        voices = record["voices"]
        print("Picking up the council saved in %s after round %d." % (out, record["done"]))
    else:
        try:
            pool = named or ranked(models())
        except (urllib.error.URLError, OSError, ValueError, subprocess.SubprocessError) as e:
            sys.exit("could not read the models (%s); is MangoMan running?" % why(e))
        if len(pool) < 2:
            sys.exit("only %d model can be reached now; a council needs at least 2. Try again in a few minutes." % len(pool))
        makers = len({maker(m) for m in pool})
        voices = [{"label": chr(65 + i), "model": m, "maker": maker(m), "role": ROLES[i][0], "rounds": []}
                  for i, m in enumerate(pool[:n])]
        record = {"question_file": os.path.basename(a[0]), "question_sha": digest, "rounds": rounds, "done": 0, "tokens": 0,
                  "spare": pool[n:], "voices": voices}
        print("Council: %d voices (%s), %d round(s), about %d AI requests%s." % (
            len(voices), ", ".join("%s %s" % (v["label"], v["maker"]) for v in voices), rounds, len(voices) * rounds,
            "" if makers >= len(voices) else "; only %d makers can be reached, so some share a maker" % makers))
        if "--plan" in a:
            return
    mission, lock = dict(ROLES), threading.Lock()
    with concurrent.futures.ThreadPoolExecutor(max_workers=len(voices)) as ex:
        for rnd in range(record["done"], rounds):
            def job(v):
                msgs = brief(question, v["role"], mission[v["role"]])
                if rnd == 1:
                    if not v["rounds"][0]["ok"]:
                        return "", 0, "no first answer"
                    msgs += [{"role": "assistant", "content": v["rounds"][0]["text"]},
                             {"role": "user", "content": "The other voices answered:\n\n%s\n\nReply in the same shape. Keep or change your position; "
                                                         "in REASONS say which voice's point moved you or why it did not." % others_text(voices, v)}]
                got = ask_model(v["model"], msgs)
                # Round 1: a model that cannot answer is swapped for a spare one
                # (not at OpenCode's free limit: every free model shares it).
                while rnd == 0 and got[2] and got[2] != FREE_LIMIT:
                    with lock:
                        if not record["spare"]:
                            break
                        v["model"] = record["spare"].pop(0)
                    v["maker"] = maker(v["model"])
                    got = ask_model(v["model"], msgs)
                return got
            got_all = list(ex.map(job, voices))
            if any(got[2] == FREE_LIMIT for got in got_all):
                sys.exit(free_limit_message())  # this round is asked again next time
            for v, got in zip(voices, got_all):
                v["rounds"].append(answer(*got))
                record["tokens"] += got[1]
            record["done"] = rnd + 1
            record["asked_at"] = dt.datetime.now(dt.timezone.utc).isoformat(timespec="seconds")
            save(out, record)
            if rnd == 0 and sum(v["rounds"][0]["ok"] for v in voices) < 2:
                sys.exit("only %d voice answered (%s); try again in a few minutes"
                         % (sum(v["rounds"][0]["ok"] for v in voices),
                            "; ".join("%s: %s" % (v["model"], v["rounds"][0]["why"]) for v in voices if not v["rounds"][0]["ok"])))
    print("wrote %s and %s: %d of %d voices answered, %s tokens" % (
        os.path.join(out, OUT + ".json"), os.path.join(out, OUT + ".md"), sum(v["rounds"][-1]["ok"] for v in voices),
        len(voices), "{:,}".format(record["tokens"])))


def markdown(r):
    md = ["# Council", "", "Asked %s. %d voices, %d of %d round(s) done." % (
        (r.get("asked_at") or "")[:10], len(r["voices"]), r.get("done", 0), r["rounds"]), ""]
    for v in r["voices"]:
        md += ["## Voice %s: %s (%s, %s)" % (v["label"], v["role"], v["model"], v["maker"]), ""]
        for i, x in enumerate(v["rounds"], 1):
            md += ["### Round %d" % i, "", x["text"] if x["ok"] else "No answer: %s" % x["why"], ""]
    return "\n".join(md)


if __name__ == "__main__":
    main()
