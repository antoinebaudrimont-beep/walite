#!/bin/sh
set -eu
umask 022

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [ "$(uname -s)" != Darwin ]; then
	printf '%s\n' 'The native notification helper must be compiled on macOS.' >&2
	exit 1
fi
build_arch=${WALITE_NOTIFIER_ARCH:-$(uname -m)}
case "$build_arch" in
	amd64|x86_64 ) swift_arch=x86_64 ;;
	arm64 ) swift_arch=arm64 ;;
	* ) printf 'Unsupported helper architecture: %s\n' "$build_arch" >&2; exit 1 ;;
esac
display_version=${WALITE_NOTIFIER_VERSION:-0.1}
build_number=${WALITE_NOTIFIER_BUILD_NUMBER:-3}
case "$display_version" in
	''|*[!0-9.]*|.*|*.|*..* ) printf '%s\n' 'Invalid helper display version.' >&2; exit 1 ;;
esac
case "$build_number" in
	''|*[!0-9]*|0* ) printf '%s\n' 'Invalid helper build number.' >&2; exit 1 ;;
esac
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
# Defaults preserve the existing development build. Release packaging supplies
# its display version and an explicitly maintained, monotonically raised build.
cat > "$stage_app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>WaliteNotifier</string>
<key>CFBundleIdentifier</key><string>io.github.antoinebaudrimontbeep.walite.notifier</string>
<key>CFBundleIconFile</key><string>Walite.icns</string>
<key>CFBundleName</key><string>Walite Notifications</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleVersion</key><string>$build_number</string>
<key>CFBundleShortVersionString</key><string>$display_version</string>
<key>LSUIElement</key><true/>
<key>LSMinimumSystemVersion</key><string>12.0</string>
<key>NSAppleEventsUsageDescription</key><string>Restore the selected iTerm2 session when you click a Walite notification.</string>
</dict></plist>
PLIST

xcrun swiftc -O -target "$swift_arch-apple-macosx12.0" \
	"$script_dir/NotificationRequest.swift" "$script_dir/ChatActivation.swift" \
	"$script_dir/SessionRestoration.swift" "$script_dir/WaliteNotifier.swift" \
	-o "$stage_app/Contents/MacOS/WaliteNotifier"
"$script_dir/generate-icon.sh" "$script_dir/../../assets/walite-icon.png" \
	"$stage_app/Contents/Resources/Walite.icns"
plutil -lint "$stage_app/Contents/Info.plist"
chmod 0755 "$stage_app" "$stage_app/Contents" "$stage_app/Contents/MacOS" "$stage_app/Contents/Resources" \
	"$stage_app/Contents/MacOS/WaliteNotifier"
chmod 0644 "$stage_app/Contents/Info.plist" "$stage_app/Contents/Resources/Walite.icns"
# No timestamp service, Developer ID credential, or notarization is involved.
codesign --force --sign - --timestamp=none "$stage_app"
codesign --verify --strict "$stage_app"
test "$(lipo -archs "$stage_app/Contents/MacOS/WaliteNotifier")" = "$swift_arch"
test ! -e "$app_path"
mv "$stage_app" "$app_path"
printf '%s\n' "$app_path"
