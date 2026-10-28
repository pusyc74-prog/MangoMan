"""Offline test of council.py: a stand-in for MangoMan's router (the switch)
and one for the OpenCode program (the default engine), so choosing models,
both rounds, a model that fails, OpenCode's free limit and the files written
run as they do for real. Run: python3 selftest.py <the pack's scripts folder> [out folder].
It leaves council/council.json and council.md in the out folder (default .)
for check_council.py."""
import http.server
import json
import os
import sys
import tempfile
import threading

sys.path.insert(0, sys.argv[1])
import council as K  # noqa: E402

out = sys.argv[2] if len(sys.argv) > 2 else "."
here = os.path.dirname(os.path.abspath(__file__))
MODELS = ["deepseek-v4.1-flash", "gpt-oss-120b", "gpt-oss-20b", "nemotron-3-ultra-550b", "nemotron-3.5-lightning",
          "qwen3.8-27b", "laguna-xs-2.1"]
seen = []  # (model, number of messages) for each chat request
busy = []
ANSWER = {
    "nemotron-3-ultra-550b": "POSITION: Launch the 200 g trial jar at 149 rupees.\nCONFIDENCE: 72\nREASONS:\n- 62% of reviews come from first-time buyers.\nRISKS:\n- Regular buyers may trade down.\nWOULD CHANGE MY MIND:\n- 500 g sales falling.",
    "deepseek-v4.1-flash": "**POSITION:** Keep only the 500 g jar.\n**CONFIDENCE:** 55\nREASONS:\n1. Margin is 38% on the 500 g jar.\nRISKS:\n* Fewer new buyers.\nWOULD CHANGE MY MIND:\n- Evidence of trade-down being small.",
    "gpt-oss-120b": "POSITION: Launch it, for first orders only.\nCONFIDENCE: 80\nREASONS:\n- Cost of 61 rupees leaves room at 149 rupees.\nRISKS:\n- Label budget of 25,000 rupees.\nWOULD CHANGE MY MIND:\n- A higher cost.",
}


class Router(http.server.BaseHTTPRequestHandler):
    def reply(self, code, body):
        raw = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self):
        assert self.headers["Authorization"] == "Bearer test-token"
        data = [{"id": "free/auto", "object": "model"}, {"id": "group/team", "object": "model", "models": ["x"]}]
        data += [{"id": m, "object": "model", "providers": ["nvidia"]} for m in MODELS]
        self.reply(200, {"object": "list", "data": data})

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        model = body["model"].removeprefix("strict/")
        seen.append((model, len(body["messages"])))
        if model == "broken-model":
            return self.reply(503, {"error": {"message": "the model is paused after errors", "type": "unavailable"}})
        if model == "busy-model" and not busy:
            busy.append(1)  # the first try finds the minute's quota used, with when it returns
            raw = json.dumps({"error": {"message": "free capacity used", "type": "free_capacity_exhausted"}}).encode()
            self.send_response(429)
            self.send_header("Retry-After", "1")
            self.end_headers()
            return self.wfile.write(raw)
        text = ANSWER.get(model, "POSITION: Launch it.\r\nCONFIDENCE: 7/10\r\n")
        if len(body["messages"]) > 2:  # round 2: it has read the others
            assert "Voice" in body["messages"][-1]["content"]
            text = text.replace("** 55", "** 60")
        self.reply(200, {"choices": [{"message": {"role": "assistant", "content": text}}], "usage": {"total_tokens": 1000}})

    def log_message(self, *a):
        pass


srv = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Router)
threading.Thread(target=srv.serve_forever, daemon=True).start()
os.environ["MANGOMAN_URL"] = "http://127.0.0.1:%d/v1" % srv.server_port
os.environ["MANGOMAN_TOKEN"] = "test-token"
os.environ["MANGOMAN_ENGINE"] = "switch"  # the switch first; OpenCode's own models further down
fails, done = [], []


def expect(cond, what):
    done.append(what)
    if not cond:
        fails.append(what)


expect(K.maker("gpt-oss-120b") == "OpenAI" and K.maker("gemma-4-31b") == "Google" and K.maker("laguna-s-2.1") == "laguna",
       "makers come from model names; an unknown name is its own maker")
picked = K.ranked(MODELS)[:3]
expect(picked == ["nemotron-3-ultra-550b", "gpt-oss-120b", "deepseek-v4.1-flash"],
       "one model per maker, the strongest of each, strongest first: %s" % picked)
p = K.parse(ANSWER["deepseek-v4.1-flash"])
expect(p["position"] == "Keep only the 500 g jar." and p["confidence"] == 55 and p["reasons"] == ["Margin is 38% on the 500 g jar."]
       and p["risks"] == ["Fewer new buyers."], "answers in bold or numbered lists are read: %s" % p)
expect(K.parse("no shape at all")["position"] == "", "an answer out of shape keeps its text but no fields")
q = K.parse("1. **POSITION:** Launch\n## Confidence\n7/10\n## Reasons\n+ cheaper first try\nReasons I gave above are not a head\n")
expect(q["position"] == "Launch" and q["confidence"] == 70 and q["reasons"] == ["cheaper first try"], "heads without a colon, 7/10 and + bullets: %s" % q)
expect(K.parse("CONFIDENCE: 0.7")["confidence"] == 70 and K.parse("CONFIDENCE: 85%")["confidence"] == 85, "0.7 and 85% read as percent")
expect(K.clean("<think>draft</think>\r\nPOSITION: x") == "POSITION: x", "private thinking and Windows line ends are dropped")
expect(K.strength("gemini-3-pro") > 0 and K.strength("minimax-m2") > 0 and K.strength("gpt-4o-mini") == 0, "gemini and minimax are not small models")

sys.argv = ["council.py", os.path.join(here, "question.md"), "--out", os.path.join(out, K.OUT)]
K.main()
r = json.load(open(os.path.join(out, K.OUT, K.OUT + ".json")))
expect([v["model"] for v in r["voices"]] == picked and [v["role"] for v in r["voices"]] == ["Advocate", "Critic", "Fact-checker"],
       "three voices with their roles")
expect(all(len(v["rounds"]) == 2 and v["rounds"][1]["ok"] for v in r["voices"]), "two rounds each")
expect(r["voices"][2]["rounds"][0]["confidence"] == 55 and r["voices"][2]["rounds"][1]["confidence"] == 60, "round 2 is kept apart from round 1")
expect(sorted(n for _, n in seen) == [2, 2, 2, 4, 4, 4], "round 2 carries the voice's own answer and the others': %s" % seen)
expect(r["tokens"] == 6000, "tokens are added up")

with tempfile.TemporaryDirectory() as d:  # a model that fails: the council goes on with the others
    sys.argv = ["council.py", os.path.join(here, "question.md"), "--models", "gpt-oss-120b,broken-model,deepseek-v4.1-flash",
                "--rounds", "1", "--out", d]
    K.main()
    b = json.load(open(os.path.join(d, K.OUT + ".json")))
    expect([x["rounds"][0]["ok"] for x in b["voices"]] == [True, False, True] and "503" in b["voices"][1]["rounds"][0]["why"],
           "a failed voice says why and the rest go on")
    sys.argv = ["council.py", os.path.join(here, "question.md"), "--models", "gpt-oss-120b,broken-model,deepseek-v4.1-flash",
                "--voices", "2", "--rounds", "1", "--out", os.path.join(d, "swap")]
    K.main()
    b = json.load(open(os.path.join(d, "swap", K.OUT + ".json")))
    expect([x["model"] for x in b["voices"]] == ["gpt-oss-120b", "deepseek-v4.1-flash"] and all(x["rounds"][0]["ok"] for x in b["voices"]),
           "a model that cannot answer is swapped for a spare one: %s" % [x["model"] for x in b["voices"]])
    sys.argv = ["council.py", os.path.join(here, "question.md"), "--models", "gpt-oss-120b,busy-model", "--rounds", "1", "--out", os.path.join(d, "busy")]
    K.main()
    b = json.load(open(os.path.join(d, "busy", K.OUT + ".json")))
    expect(b["voices"][1]["rounds"][0]["ok"] and b["voices"][1]["rounds"][0]["confidence"] == 70, "a busy model is asked again after the wait it gives")
    # A run stopped after round 1 picks up there: only round 2 is asked.
    stopped = os.path.join(d, "stopped")
    sys.argv = ["council.py", os.path.join(here, "question.md"), "--out", stopped]
    K.main()
    path = os.path.join(stopped, K.OUT + ".json")
    r2 = json.load(open(path))
    r2["done"] = 1
    for v in r2["voices"]:
        v["rounds"] = v["rounds"][:1]
    json.dump(r2, open(path, "w"))
    before = len(seen)
    K.main()
    r3 = json.load(open(path))
    expect(len(seen) - before == 3 and r3["done"] == 2 and all(len(v["rounds"]) == 2 for v in r3["voices"]),
           "a stopped council picks up after its last round (%d requests)" % (len(seen) - before))
    before = len(seen)
    sys.argv = ["council.py", os.path.join(here, "question.md"), "--plan", "--out", os.path.join(d, "plan")]
    K.main()
    expect(len(seen) == before and not os.path.exists(os.path.join(d, "plan")), "--plan asks no model and writes nothing")
    try:  # only one voice can answer: stop with a plain message
        sys.argv = ["council.py", os.path.join(here, "question.md"), "--models", "gpt-oss-120b,broken-model", "--out", d]
        K.main()
        expect(False, "one answer is not a council")
    except SystemExit as e:
        expect("only 1 voice answered" in str(e), "one answer is not a council: %s" % e)
    big = os.path.join(d, "big.md")
    open(big, "w").write("x" * (K.MAX_QUESTION + 1))
    try:
        sys.argv = ["council.py", big]
        K.main()
        expect(False, "a question too long for every voice is refused")
    except SystemExit as e:
        expect("summarise" in str(e), "a question too long for every voice is refused")
srv.shutdown()

# ---------- On OpenCode's own models (the default engine) ----------

FAKE = r'''import json, os, sys
ctl = os.environ["FAKE_OPENCODE_DIR"]
a = sys.argv[1:]
if a[:2] == ["models", "opencode"]:
    print("opencode/big-pickle\nopencode/mimo-v2.6-flash-free\nopencode/nemotron-3.5-lightning-free\nopencode/space-bunny-free")
    sys.exit()
prompt = sys.stdin.read()
cfg = json.loads(os.environ["OPENCODE_CONFIG_CONTENT"])
agent = cfg["agent"]["mangoman-voice"]
model = a[a.index("-m") + 1]
ok = a[:5] == ["run", "--format", "json", "--agent", "mangoman-voice"] and cfg["enabled_providers"] == ["opencode"] \
    and agent["permission"] == {"*": "deny"} and cfg["agent"]["title"]["disable"] and "Your role" in agent["prompt"] \
    and not os.listdir(".")
with open(os.path.join(ctl, "calls.jsonl"), "a", encoding="utf-8") as f:
    f.write(json.dumps({"model": model, "ok": ok, "round2": "Your first answer was" in prompt, "question": "trial jar" in prompt}) + "\n")
if os.path.exists(os.path.join(ctl, "limit")):
    sys.stderr.write('timestamp=2026-10-28T06:50:58.1Z level=ERROR run=ab12 message="stream error" providerID=opencode modelID=%s '
                     'error.error="AI_APICallError: Free usage limit reached. Free models work in OpenCode (https://opencode.ai)."\n' % model)
    sys.stderr.flush()
    import time
    time.sleep(60)  # as OpenCode: it waits for the limit to end
    sys.exit()
if model.endswith("space-bunny-free"):
    print(json.dumps({"type": "error", "error": {"name": "UnknownError", "data": {"message": "Unexpected server error."}}}))
    sys.exit()
conf = 75 if "Your first answer was" in prompt else 70
print(json.dumps({"type": "step_start"}))
print(json.dumps({"type": "text", "part": {"text": "POSITION: Launch the trial jar.\nCONFIDENCE: %d\nREASONS:\n- 62%% of reviews come from first-time buyers.\n" % conf}}))
print(json.dumps({"type": "step_finish", "part": {"tokens": {"total": 1000, "input": 800, "output": 200, "reasoning": 0, "cache": {"read": 0}}}}))
'''

with tempfile.TemporaryDirectory() as d:
    fake = os.path.join(d, "fake_opencode.py")
    open(fake, "w", encoding="utf-8").write(FAKE)
    if os.name == "nt":  # a program Windows can start, which runs the stand-in
        exe = os.path.join(d, "opencode.cmd")
        open(exe, "w").write('@"%s" "%s" %%*\n' % (sys.executable, fake))
    else:
        exe = os.path.join(d, "opencode")
        open(exe, "w").write("#!%s\n" % sys.executable + FAKE)
        os.chmod(exe, 0o755)
    os.environ.update({"MANGOMAN_ENGINE": "", "MANGOMAN_OPENCODE": exe, "FAKE_OPENCODE_DIR": d})
    calls = lambda: [json.loads(x) for x in open(os.path.join(d, "calls.jsonl"), encoding="utf-8")]
    sys.argv = ["council.py", os.path.join(here, "question.md"), "--out", os.path.join(d, "oc")]
    K.main()
    o = json.load(open(os.path.join(d, "oc", K.OUT + ".json")))
    models = [v["model"] for v in o["voices"]]
    expect(models == ["opencode/big-pickle", "opencode/mimo-v2.6-flash-free", "opencode/nemotron-3.5-lightning-free"],
           "OpenCode's own free models by default; a broken one is swapped for a spare: %s" % models)
    expect(all(len(v["rounds"]) == 2 and v["rounds"][1]["ok"] and v["rounds"][1]["confidence"] == 75 for v in o["voices"]),
           "two rounds on OpenCode's models")
    c = calls()
    expect(all(x["ok"] and x["question"] for x in c) and sum(x["round2"] for x in c) == 3,
           "each voice is an OpenCode run with no tools, its role, the question on standard input, from an empty folder: %s" % c)
    expect(o["tokens"] == 6000, "tokens come from OpenCode's step reports: %d" % o["tokens"])
    open(os.path.join(d, "limit"), "w").close()  # OpenCode's free daily limit
    before = len(calls())
    started = __import__("time").time()
    try:
        sys.argv = ["council.py", os.path.join(here, "question.md"), "--out", os.path.join(d, "limit-run")]
        K.main()
        expect(False, "the free limit stops the council")
    except SystemExit as e:
        expect("free AI messages are used up" in str(e) and "picks up where it stopped" in str(e), "the free limit stops the council: %s" % e)
    expect(len(calls()) - before == 3, "at the free limit no spare model is tried: they share it (%d runs)" % (len(calls()) - before))
    took = __import__("time").time() - started
    expect(took < 30, "OpenCode waits at the limit; the council stops it at once (%.0f s)" % took)
    expect(not os.path.exists(os.path.join(d, "limit-run", K.OUT + ".json")) or json.load(open(os.path.join(d, "limit-run", K.OUT + ".json")))["done"] == 0,
           "the round cut by the limit is asked again next time")

for f in fails:
    print("FAIL  selftest: " + f)
if not fails:
    print("PASS  council selftest (%d checks)" % len(done))
sys.exit(1 if fails else 0)
