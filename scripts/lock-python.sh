#!/usr/bin/env bash
# Pins the packages the skill packs need, for every system MangoMan runs on.
#
#   scripts/lock-python.sh [out dir]   (default: internal/pyenv)
#
# Writes requirements.txt (one list for all systems, every package pinned
# with its checksums) and sizes.json (bytes to download per system, and the
# Playwright package alone, for the setup page). Needs uv and pip with PyPI
# reachable; the workflow pylock.yml runs it.
set -euo pipefail
cd "$(dirname "$0")/.."
out=${1:-internal/pyenv}
mkdir -p "$out"
uv pip compile internal/pyenv/requirements.in --universal --python-version 3.12 \
  --generate-hashes --no-header -o "$out/requirements.txt"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
# uv's name for each system, to list exactly the packages it gets (markers
# such as "sys_platform == 'win32'" are judged for that system, not this one).
declare -A target=(
  [windows-amd64]=x86_64-pc-windows-msvc [darwin-arm64]=aarch64-apple-darwin [darwin-amd64]=x86_64-apple-darwin
  [linux-amd64]=x86_64-manylinux_2_28 [linux-arm64]=aarch64-manylinux_2_28
)
# The wheel tags each system takes, newest first (pip matches them exactly).
declare -A tags=(
  [windows-amd64]="win_amd64"
  [darwin-arm64]="macosx_15_0_arm64 macosx_14_0_arm64 macosx_13_0_arm64 macosx_12_0_arm64 macosx_11_0_arm64 macosx_10_13_universal2 macosx_10_9_universal2"
  [darwin-amd64]="macosx_13_0_x86_64 macosx_12_0_x86_64 macosx_11_0_x86_64 macosx_10_15_x86_64 macosx_10_13_x86_64 macosx_10_10_x86_64 macosx_10_9_x86_64 macosx_10_13_universal2 macosx_10_9_universal2"
  [linux-amd64]="manylinux_2_28_x86_64 manylinux_2_27_x86_64 manylinux_2_17_x86_64 manylinux2014_x86_64 manylinux1_x86_64"
  [linux-arm64]="manylinux_2_28_aarch64 manylinux_2_27_aarch64 manylinux_2_17_aarch64 manylinux2014_aarch64"
)
for sys in "${!tags[@]}"; do
  flags=()
  for t in ${tags[$sys]}; do flags+=(--platform "$t"); done
  uv pip compile "$out/requirements.txt" --python-platform "${target[$sys]}" --python-version 3.12 \
    --no-header --no-annotate -q -o "$tmp/$sys.txt"
  python3 -m pip download -q --no-deps --only-binary=:all: --python-version 3.12 --implementation cp \
    "${flags[@]}" -r "$tmp/$sys.txt" -d "$tmp/$sys"
done
python3 - "$tmp" "$out/sizes.json" <<'EOF'
import json, os, sys
root, dst = sys.argv[1], sys.argv[2]
out = {}
for sysname in sorted(d for d in os.listdir(root) if os.path.isdir(os.path.join(root, d))):
    files = os.listdir(os.path.join(root, sysname))
    size = lambda f: os.path.getsize(os.path.join(root, sysname, f))
    out[sysname] = {"packages": sum(map(size, files)),
                    "playwright": sum(size(f) for f in files if f.lower().startswith("playwright-"))}
json.dump(out, open(dst, "w"), indent=1, sort_keys=True)
print(json.dumps(out, indent=1, sort_keys=True))
EOF
