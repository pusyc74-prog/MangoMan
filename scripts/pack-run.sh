#!/usr/bin/env bash
# Run skill packs on their test cases with a real model, score the result and
# record what each task costs.
#
# Usage: scripts/pack-run.sh MODEL [PACK ...]
#   MODEL  free/auto, free/coder or strict/<model>
#   PACK   pack folder names (default: every pack that has test cases)
#
# Set VARIANT=ste to use a pack's other instruction style
# (tests/<VARIANT>/SKILL.md) instead of its SKILL.md, so both can be scored on
# the same cases. Set CASE_TIMEOUT to change the 900 s a case is given, and
# CASES to use only the first N cases of each pack (CASES=1 is a cheap smoke
# run: one task per pack instead of three).
#
# Needs: provider keys in MANGOMAN_HOME, opencode on PATH, and the pack
# dependencies (pandas, Pillow, python-pptx, python-docx, pypdf, playwright).
# Prints a Markdown table of pack, case, score, requests, tokens and seconds,
# writes each case's output to pack-run-logs/<pack>/<case>.log and the usage so
# far (with where the input tokens went) to <case>.usage.json, and exits 1 if
# any case scored below 50.
set -u
cd "$(dirname "$0")/.."
repo=$PWD
model=${1:?usage: pack-run.sh MODEL [PACK ...]}
shift
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

go build -o "$work/mm" ./cmd/mangoman || exit 1
M="$work/mm"
"$M" skills install --dir "$work/skills" > /dev/null || exit 1
export PYTHONPATH="$repo/internal/skills/packs/shared"

# used prints the requests and tokens recorded so far, as "requests tokens".
used() {
  "$M" usage --json --days 1 2>/dev/null | python3 -c '
import json, sys
try:
    s = json.load(sys.stdin)
except ValueError:
    print("0 0"); raise SystemExit
print(s.get("requests", 0), sum(r.get("tokens", 0) for r in s.get("rows") or []))
'
}

# Pack names may be separated by spaces or commas.
read -r -a packs <<< "$(printf '%s ' "$@" | tr ',' ' ')"
if [ ${#packs[@]} -eq 0 ]; then
  packs=()
  for d in internal/skills/packs/mangoman-*; do
    [ -d "$d/tests/cases" ] && packs+=("$(basename "$d")")
  done
fi

echo "| Pack | Case | Score | Requests | Tokens | Seconds |"
echo "| --- | --- | --- | --- | --- | --- |"
low=0
for pack in "${packs[@]}"; do
  src="$repo/internal/skills/packs/$pack"
  [ -d "$src/tests/cases" ] || { echo "no test cases: $pack" >&2; continue; }
  if [ -n "${VARIANT:-}" ] && [ -f "$src/tests/$VARIANT/SKILL.md" ]; then
    cp "$src/tests/$VARIANT/SKILL.md" "$work/skills/$pack/SKILL.md"
  fi
  n=0
  for case in "$src"/tests/cases/*/; do
    n=$((n + 1))
    [ -n "${CASES:-}" ] && [ "$n" -gt "$CASES" ] && break
    name=$(basename "$case")
    d="$work/run/$pack/$name"
    mkdir -p "$d" && cp "$case"/* "$d/"
    prompt=$(cat "$d/prompt.txt")
    read -r r0 t0 <<< "$(used)"
    start=$SECONDS
    (cd "$d" && timeout "${CASE_TIMEOUT:-900}" "$M" code --model "$model" --no-web run --auto --dir "$d" "$prompt") \
      > "$d/run.log" 2>&1
    secs=$((SECONDS - start))
    read -r r1 t1 <<< "$(used)"
    out=$(python3 "$src/tests/score.py" "$d" 2>> "$d/run.log")
    # Scores can be fractional, so the "too low" test is done here, not in sh.
    read -r bad score notes <<< "$(printf '%s' "$out" | python3 -c '
import json, sys
try:
    s = json.load(sys.stdin)
except ValueError:
    print("1 0 scorer gave no result"); raise SystemExit
n = float(s.get("score") or 0)
print(int(n < 50), s.get("score", 0), (s.get("notes") or "").replace("|", "/"))
')"
    echo "| $pack | $name | $score | $((r1 - r0)) | $((t1 - t0)) | $secs |"
    mkdir -p "$repo/pack-run-logs/$pack"
    tail -c 200000 "$d/run.log" > "$repo/pack-run-logs/$pack/$name.log"
    "$M" usage --json --days 1 > "$repo/pack-run-logs/$pack/$name.usage.json" 2>/dev/null
    [ "$bad" = 1 ] && { low=1; echo "      $name: $notes" >&2; }
  done
done
exit $low
