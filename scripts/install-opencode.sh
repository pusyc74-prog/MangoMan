#!/usr/bin/env bash
# Installs the OpenCode release MangoMan has tested (the version and checksum
# in internal/opencode/opencode.go) into $MANGOMAN_HOME/tools, where
# mangoman code finds it: CI tests the same OpenCode users get.
set -euo pipefail
cd "$(dirname "$0")/.."
case "$(uname -s)/$(uname -m)" in
  Linux/x86_64) asset=opencode-linux-x64.tar.gz ;;
  Linux/aarch64) asset=opencode-linux-arm64.tar.gz ;;
  Darwin/x86_64) asset=opencode-darwin-x64.zip ;;
  Darwin/arm64) asset=opencode-darwin-arm64.zip ;;
  *) echo "no tested OpenCode build for $(uname -s)/$(uname -m)" >&2; exit 1 ;;
esac
src=internal/opencode/opencode.go
version=$(sed -n 's/^const Version = "\(.*\)"/\1/p' "$src")
sum=$(sed -n "s/.*\"$asset\": *\"\([0-9a-f]*\)\".*/\1/p" "$src")
[ -n "$version" ] && [ -n "$sum" ] || { echo "version or checksum not found in $src" >&2; exit 1; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL -o "$tmp/$asset" "https://github.com/anomalyco/opencode/releases/download/v$version/$asset"
got=$( (sha256sum "$tmp/$asset" 2>/dev/null || shasum -a 256 "$tmp/$asset") | cut -d' ' -f1)
[ "$got" = "$sum" ] || { echo "the download does not match the tested OpenCode release" >&2; exit 1; }
mkdir -p "${MANGOMAN_HOME:?}/tools"
case "$asset" in
  *.zip) unzip -oq "$tmp/$asset" opencode -d "$MANGOMAN_HOME/tools" ;;
  *) tar xzf "$tmp/$asset" -C "$MANGOMAN_HOME/tools" opencode ;;
esac
echo "OpenCode $version installed in $MANGOMAN_HOME/tools"
