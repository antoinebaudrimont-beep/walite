#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
build_dir=${WALITE_NOTIFIER_BUILD_DIR:-"$HOME/Applications"}
app_name='Walite Notifications.app'
app_path="$build_dir/$app_name"

if [ -e "$app_path" ]; then
	printf '%s\n' "App already exists: $app_path" >&2
	exit 1
fi

mkdir -p "$build_dir"
stage_dir=$(mktemp -d "$build_dir/.walite-notifier-build.XXXXXX")
trap 'rm -rf -- "$stage_dir"' 0
stage_app="$stage_dir/$app_name"
mkdir -p "$stage_app/Contents/MacOS" "$stage_app/Contents/Resources"
# Increment CFBundleVersion for each published helper build. The display
# version is maintained separately; this identity/icon update is build 3.
cat > "$stage_app/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>WaliteNotifier</string>
<key>CFBundleIdentifier</key><string>io.github.antoinebaudrimontbeep.walite.notifier</string>
<key>CFBundleIconFile</key><string>Walite.icns</string>
<key>CFBundleName</key><string>Walite Notifications</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleVersion</key><string>3</string>
<key>CFBundleShortVersionString</key><string>0.1</string>
<key>LSUIElement</key><true/>
<key>NSAppleEventsUsageDescription</key><string>Restore the selected iTerm2 session when you click a Walite notification.</string>
</dict></plist>
PLIST

xcrun swiftc "$script_dir/NotificationRequest.swift" "$script_dir/ChatActivation.swift" \
	"$script_dir/SessionRestoration.swift" "$script_dir/WaliteNotifier.swift" \
	-o "$stage_app/Contents/MacOS/WaliteNotifier"
"$script_dir/generate-icon.sh" "$script_dir/../../assets/walite-icon.png" \
	"$stage_app/Contents/Resources/Walite.icns"
plutil -lint "$stage_app/Contents/Info.plist"
codesign --force --sign - "$stage_app"
codesign --verify --strict "$stage_app"
test ! -e "$app_path"
mv "$stage_app" "$app_path"
printf '%s\n' "$app_path"
