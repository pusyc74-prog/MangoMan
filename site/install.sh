#!/bin/sh
# Installs MangoMan on a Mac or Linux:  curl -fsSL __SITE__/install.sh | sh
# A file fetched with curl carries no quarantine flag, so macOS does not block
# it as an app from an unknown developer. Mac: ~/Applications/MangoMan.app,
# found with Spotlight; it runs MangoMan in the background and opens it in the browser. Linux:
# ~/.local/bin/mangoman. MANGOMAN_SITE fetches from another copy of the site
# and MANGOMAN_NO_START skips starting MangoMan (both for the CI checks).
# Everything is in main, called on the last line: a download cut off halfway
# runs nothing.
set -eu

main() {
site="${MANGOMAN_SITE:-__SITE__}"

case "$(uname -s)" in
Darwin) os=darwin name=Mac ;;
Linux) os=linux name=Linux ;;
*) echo "This line installs MangoMan on a Mac or Linux. For Windows, see $site"; exit 1 ;;
esac
case "$(uname -m)" in
arm64 | aarch64) arch=arm64 ;;
x86_64 | amd64) arch=amd64 ;;
*) echo "MangoMan does not run on this kind of chip yet ($(uname -m))."; exit 1 ;;
esac
# Terminal on an Apple chip can run through Rosetta and then says x86_64.
if [ "$os" = darwin ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" = 1 ]; then
	arch=arm64
fi
file="mangoman-$os-$arch"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "Downloading MangoMan for $name (about 10 MB)..."
curl -fSL --progress-bar "$site/$file" -o "$tmp/mangoman"
curl -fsSL "$site/SHA256SUMS.txt" -o "$tmp/sums"
want=$(awk -v f="$file" '{ sub(/^\*/, "", $2) } $2 == f { print $1 }' "$tmp/sums")
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp/mangoman" | cut -d' ' -f1)
else
	got=$(shasum -a 256 "$tmp/mangoman" | cut -d' ' -f1)
fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then
	echo "The download was damaged. Please run the line again."
	exit 1
fi
chmod +x "$tmp/mangoman"

if [ "$os" = darwin ]; then
	app="$HOME/Applications/MangoMan.app"
	# A Mac's disk ignores the case of names: the program must not be called
	# mangoman next to the launcher MangoMan, or one overwrites the other (the
	# launcher then opened itself in Terminal again and again, 19 Oct).
	rm -rf "$app/Contents/MacOS"
	mkdir -p "$app/Contents/MacOS"
	mv -f "$tmp/mangoman" "$app/Contents/MacOS/mangoman-cli"
	# Started with no window, MangoMan runs in the background and opens in the
	# browser; another click only opens the page. Stop MangoMan is on Settings.
	cat >"$app/Contents/MacOS/MangoMan" <<'EOF'
#!/bin/sh
exec "$(dirname "$0")/mangoman-cli" app </dev/null
EOF
	chmod +x "$app/Contents/MacOS/MangoMan"
	cat >"$app/Contents/Info.plist" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>MangoMan</string>
	<key>CFBundleIdentifier</key><string>app.mangoman</string>
	<key>CFBundleExecutable</key><string>MangoMan</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>__VERSION__</string>
</dict>
</plist>
EOF
	echo "MangoMan is installed. Next time: press Cmd + Space, type MangoMan, press Enter."
	[ -n "${MANGOMAN_NO_START:-}" ] || open "$app"
else
	bin="$HOME/.local/bin"
	mkdir -p "$bin"
	mv -f "$tmp/mangoman" "$bin/mangoman"
	case ":$PATH:" in
	*":$bin:"*) echo "MangoMan is installed. Next time, run: mangoman" ;;
	*) echo "MangoMan is installed. Next time, run: $bin/mangoman" ;;
	esac
	# The keyboard, not the script coming through the pipe from curl.
	[ -n "${MANGOMAN_NO_START:-}" ] || "$bin/mangoman" </dev/tty
fi
}

main "$@"
