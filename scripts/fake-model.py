"""A stand-in model for offline tests of the coding screen and `mangoman code
run`: an OpenAI-compatible server that streams like a real provider.

    python3 scripts/fake-model.py 11434 [control dir]

On port 11434 MangoMan finds it as a local Ollama model named "fake"; start
the router with OLLAMA_CONTEXT_LENGTH=131072 (else it counts as too small for
coding requests) and use the model strict/fake. A coding request (one with
tools) follows a script: write hello.txt, run a command (needs approval on
the screen), then answer "Done. I made hello.txt and started the app."

Files in the control dir (default: ./fake-model-control) change what the
next coding request gets; *_once files are removed when used:
    fail_once      HTTP 502
    fail_always    HTTP 500 on every request
    overload_once  "Service temporarily overloaded" inside the stream
    think_once     only reasoning, then the end (no words, no tool call)
    garble_once    words with a leaked <|close|> token
    delay          seconds to wait before answering (the file holds the number)
    cmd            the command for the run step (default: echo ... localhost:5173)
Every request is logged to requests.log in the control dir.
"""
import json
import os
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = int(sys.argv[1])
CTL = sys.argv[2] if len(sys.argv) > 2 else "fake-model-control"
os.makedirs(CTL, exist_ok=True)


def chunk(delta, finish=None):
    return {"id": "x", "object": "chat.completion.chunk", "model": "fake",
            "choices": [{"index": 0, "delta": delta, "finish_reason": finish}]}


def once(name):
    path = os.path.join(CTL, name)
    if os.path.exists(path):
        os.remove(path)
        return True
    return False


def setting(name):
    path = os.path.join(CTL, name)
    return open(path).read().strip() if os.path.exists(path) else ""


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):  # the model list, for discovery
        self.reply(200, {"object": "list", "data": [{"id": "fake", "object": "model"}]})

    def reply(self, code, obj):
        b = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def stream(self, events, done=True):
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        for e in events:
            self.wfile.write(("data: " + json.dumps(e) + "\n\n").encode())
            self.wfile.flush()
        if done:
            self.wfile.write(b"data: [DONE]\n\n")

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        tools = body.get("tools") or []
        with open(os.path.join(CTL, "requests.log"), "a") as f:
            f.write(json.dumps({"time": round(time.time(), 1), "tools": len(tools),
                                **{k: v for k, v in body.items() if k not in ("messages", "tools")}}) + "\n")
        if os.path.exists(os.path.join(CTL, "fail_always")):
            return self.reply(500, {"error": {"message": "internal error"}})
        if not tools:  # side requests (titles): answer at once
            return self.stream([chunk({"role": "assistant", "content": "Hello file"}), chunk({}, "stop")])
        if once("fail_once"):
            return self.reply(502, {"error": {"message": "no answer", "type": "all_candidates_failed"}})
        if once("overload_once"):
            return self.stream([{"error": {"message": "Service temporarily overloaded"}}], done=False)
        if once("think_once"):
            return self.stream([chunk({"role": "assistant", "reasoning_content": "let me think"}), chunk({}, "stop")])
        if once("garble_once"):
            return self.stream([chunk({"role": "assistant", "content": "skill盖 sudo"}), chunk({"content": "<|close|> noise"}), chunk({}, "stop")])
        if setting("delay"):
            time.sleep(float(setting("delay")))
        done = sum(1 for m in body.get("messages", []) if m.get("role") == "tool")
        if done == 0:
            args = {"filePath": "hello.txt", "content": "hi from MangoMan\n"}
            call = {"index": 0, "id": "call_1", "type": "function", "function": {"name": "write", "arguments": json.dumps(args)}}
            events = [chunk({"role": "assistant", "tool_calls": [call]}), chunk({}, "tool_calls")]
        elif done == 1:
            args = {"command": setting("cmd") or "echo installing; echo Local: http://localhost:5173/", "description": "Install and start"}
            call = {"index": 0, "id": "call_2", "type": "function", "function": {"name": "bash", "arguments": json.dumps(args)}}
            events = [chunk({"role": "assistant", "tool_calls": [call]}), chunk({}, "tool_calls")]
        else:
            events = [chunk({"role": "assistant", "content": "Done. I made hello.txt "}), chunk({"content": "and started the app."}), chunk({}, "stop")]
        self.stream(events)


ThreadingHTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
