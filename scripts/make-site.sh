#!/bin/sh
# Builds the download site into dist/site: the page, the two install scripts,
# MangoMan for each system and SHA256SUMS.txt. Drop the folder on Netlify
# (Sites, Add new site, Deploy manually) or upload it anywhere static.
#   scripts/make-site.sh [site address]   (default https://getmangoman.netlify.app)
set -eu
site="${1:-https://getmangoman.netlify.app}"
site="${site%/}"
ver="${VERSION:-$(git rev-parse --short HEAD)}"
out=dist/site
rm -rf "$out"
mkdir -p "$out"
for t in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64; do
	goos=${t%/*} goarch=${t#*/} ext=""
	[ "$goos" = windows ] && ext=.exe
	# GOFLAGS emptied: a development override (say a stand-in key store
	# through -modfile) must never reach a release. Shipped once, 13 Oct.
	GOFLAGS= CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -trimpath -ldflags "-s -w -X main.version=$ver" \
		-o "$out/mangoman-$goos-$goarch$ext" ./cmd/mangoman
done
(cd "$out" && { sha256sum mangoman-* 2>/dev/null || shasum -a 256 mangoman-*; } >SHA256SUMS.txt)
for f in index.html site.css site.js install.sh install.ps1; do
	sed -e "s|__SITE__|$site|g" -e "s|__VERSION__|$ver|g" "site/$f" >"$out/$f"
done
cp internal/ingress/ui/icon.svg "$out/"
echo "Built $out for $site (version $ver)."
