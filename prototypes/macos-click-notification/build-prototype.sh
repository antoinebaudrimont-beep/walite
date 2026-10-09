#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
build_dir=${WALITE_CLICK_PROTOTYPE_DIR:-"$HOME/Applications"}
app_path="$build_dir/Walite Click Notification Prototype.app"
executable_path="$app_path/Contents/MacOS/WaliteClickNotificationPrototype"

if [ -e "$app_path" ]; then
	printf '%s\n' "Prototype already exists: $app_path" >&2
	printf '%s\n' "Move or remove that disposable prototype app before rebuilding." >&2
	exit 1
fi

mkdir -p "$app_path/Contents/MacOS"
cat > "$app_path/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>WaliteClickNotificationPrototype</string>
<key>CFBundleIdentifier</key><string>io.github.antoinebaudrimontbeep.walite.clicknotificationprototype</string>
<key>CFBundleName</key><string>Walite Click Notification Prototype</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleVersion</key><string>1</string>
<key>CFBundleShortVersionString</key><string>0.1</string>
<key>LSUIElement</key><true/>
<key>NSAppleEventsUsageDescription</key><string>Restore the selected iTerm2 session when you click a Walite notification.</string>
</dict></plist>
PLIST

xcrun swiftc "$script_dir/WaliteNotificationPrototype.swift" -o "$executable_path"
codesign --force --sign - "$app_path"
codesign --verify --strict "$app_path"
printf '%s\n' "$app_path"
