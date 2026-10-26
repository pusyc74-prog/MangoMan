"""Offline test of council.py: a stand-in for MangoMan's router, so choosing
models, both rounds, a model that fails and the files written run as they do
for real. Run: python3 selftest.py <the pack's scripts folder> [out folder].
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
            return self.reply(429, {"error": {"message": "all free capacity used", "type": "exhausted"}})
        text = ANSWER.get(model, "")
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
fails, done = [], []


def expect(cond, what):
    done.append(what)
    if not cond:
        fails.append(what)


expect(K.maker("gpt-oss-120b") == "OpenAI" and K.maker("gemma-4-31b") == "Google" and K.maker("laguna-s-2.1") == "laguna",
       "makers come from model names; an unknown name is its own maker")
picked = K.pick(MODELS, 3)
expect(picked == ["nemotron-3-ultra-550b", "gpt-oss-120b", "deepseek-v4.1-flash"],
       "one model per maker, the strongest of each, strongest first: %s" % picked)
p = K.parse(ANSWER["deepseek-v4.1-flash"])
expect(p["position"] == "Keep only the 500 g jar." and p["confidence"] == 55 and p["reasons"] == ["Margin is 38% on the 500 g jar."]
       and p["risks"] == ["Fewer new buyers."], "answers in bold or numbered lists are read: %s" % p)
expect(K.parse("no shape at all")["position"] == "", "an answer out of shape keeps its text but no fields")

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
    expect([x["rounds"][0]["ok"] for x in b["voices"]] == [True, False, True] and "429" in b["voices"][1]["rounds"][0]["why"],
           "a failed voice says why and the rest go on")
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

for f in fails:
    print("FAIL  selftest: " + f)
if not fails:
    print("PASS  council selftest (%d checks)" % len(done))
sys.exit(1 if fails else 0)
