#!/usr/bin/env bash
# One-command live test: start the router, send real requests (plain and
# streaming), run doctor, show usage. Keys come from environment variables
# (GROQ_API_KEY, CEREBRAS_API_KEY, OPENROUTER_API_KEY, NVIDIA_API_KEY), for
# example Codespaces secrets, so nothing is written to disk.
#
#   scripts/try.sh            quick doctor (basic, stream, tools, json)
#   scripts/try.sh --full     full doctor corpus
set -euo pipefail
cd "$(dirname "$0")/.."

MM=bin/mangoman
[ -x "$MM" ] || make build >/dev/null
export MANGOMAN_HOME="${MANGOMAN_HOME:-$HOME/.mangoman}"
export MANGOMAN_KEYSTORE="${MANGOMAN_KEYSTORE:-file}"
export MANGOMAN_PASSPHRASE="${MANGOMAN_PASSPHRASE:-try-script-not-a-secret}"
"$MM" init >/dev/null

keys=0
for v in GROQ_API_KEY CEREBRAS_API_KEY OPENROUTER_API_KEY NVIDIA_API_KEY; do
  if [ -n "${!v:-}" ]; then echo "found $v"; keys=$((keys + 1)); fi
done
if [ "$keys" -eq 0 ]; then
  echo "No provider keys in the environment."
  echo "Add GROQ_API_KEY as a Codespaces secret (github.com/settings/codespaces), or run:"
  echo "  export GROQ_API_KEY=gsk_..."
  exit 1
fi

port=$(python3 -c "import json,os;print(json.load(open(os.environ['MANGOMAN_HOME']+'/config.json'))['port'])")
token=$(python3 -c "import json,os;print(json.load(open(os.environ['MANGOMAN_HOME']+'/config.json'))['token'])")

"$MM" serve >"$MANGOMAN_HOME/serve.log" 2>&1 &
pid=$!
trap 'kill $pid 2>/dev/null || true' EXIT
for _ in $(seq 1 40); do
  curl -sf "http://127.0.0.1:$port/healthz" >/dev/null && break
  sleep 0.25
done

echo
echo "== 1. plain request"
"$MM" test "In one sentence, what is a router?" || echo "(request failed; doctor below shows why)"

echo
echo "== 2. streaming request (words should arrive one by one)"
curl -sN "http://127.0.0.1:$port/v1/chat/completions" \
  -H "Authorization: Bearer $token" -H "Content-Type: application/json" \
  -d '{"model":"free/auto","stream":true,"messages":[{"role":"user","content":"Count from 1 to 10."}]}' |
  python3 -c '
import json, sys
got, other = False, []
for line in sys.stdin:
    line = line.strip()
    if not line.startswith("data:"):
        other.append(line)
        continue
    if line.endswith("[DONE]"):
        continue
    got = True
    for c in json.loads(line[5:]).get("choices", []):
        print(c.get("delta", {}).get("content") or "", end="", flush=True)
print()
if not got:
    print("(stream failed: " + " ".join(other)[:300] + ")")
    print("(doctor below shows why)")'

echo
echo "== 3. doctor"
if [ "${1:-}" = "--full" ]; then "$MM" doctor --yes; else "$MM" doctor --quick --yes; fi

echo
echo "== 4. usage"
"$MM" usage

echo
echo "Router log: $MANGOMAN_HOME/serve.log"
