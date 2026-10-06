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
# the same cases. Set CASE_TIMEOUT to change the 900 s a case is given.
#
# Needs: provider keys in MANGOMAN_HOME, opencode on PATH, and the pack
# dependencies (pandas, Pillow, python-pptx, python-docx, pypdf, playwright).
# Prints a Markdown table of pack, case, score, requests, tokens and seconds,
# and exits 1 if any case scored below 50.
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

packs=("$@")
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
  for case in "$src"/tests/cases/*/; do
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
    read -r score notes <<< "$(printf '%s' "$out" | python3 -c '
import json, sys
try:
    s = json.load(sys.stdin)
except ValueError:
    print("0 scorer gave no result"); raise SystemExit
print(s.get("score", 0), (s.get("notes") or "").replace("|", "/"))
')"
    echo "| $pack | $name | $score | $((r1 - r0)) | $((t1 - t0)) | $secs |"
    [ "$score" -lt 50 ] && { low=1; echo "      $name: $notes" >&2; }
  done
done
exit $low
