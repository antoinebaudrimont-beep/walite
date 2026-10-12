#!/bin/sh
# Verify a real release artifact without starting Walite or reading user data.
set -eu
export LC_ALL=C

version=${1:-}
target=${2:-}
release_dist=${3:-dist}
case "$version" in
	v[0-9]* ) ;;
	* ) printf 'usage: %s v<version> <os>/<arch> [dist-directory]\n' "$0" >&2; exit 2 ;;
esac
case "$version" in *[!A-Za-z0-9._-]* ) exit 2 ;; esac
case "$target" in linux/amd64|linux/arm64|darwin/amd64|darwin/arm64 ) ;; * ) exit 2 ;; esac
goos=${target%/*}
goarch=${target#*/}
archive="walite_${version}_${goos}_${goarch}.tar.gz"
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root_dir=$(dirname -- "$script_dir")
verify_tmp=$(mktemp -d "${TMPDIR:-/tmp}/walite-verify.XXXXXX")
trap 'rm -rf "$verify_tmp"' EXIT HUP INT TERM

# Require this artifact's entry; do not accept a manifest containing only some
# other platform. The final workflow separately requires all four artifacts.
checksum_line=$(awk -v archive="$archive" 'NF == 2 && $2 == archive { print }' "$release_dist/SHA256SUMS")
test "$(printf '%s\n' "$checksum_line" | wc -l | tr -d ' ')" = 1
printf '%s\n' "$checksum_line" > "$verify_tmp/checksum"
(
	cd "$release_dist"
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum -c "$verify_tmp/checksum"
	else
		shasum -a 256 -c "$verify_tmp/checksum"
	fi
)

# Whitelist before extracting, including directory entries. Unexpected files,
# traversal, caches and credentials cannot silently become release content.
printf '%s\n' LICENSE README.md walite > "$verify_tmp/expected"
if [ "$goos" = linux ]; then
	printf '%s\n' walite-notification.png >> "$verify_tmp/expected"
else
	printf '%s\n' 'Walite Notifications.app/' 'Walite Notifications.app/Contents/' \
		'Walite Notifications.app/Contents/Info.plist' \
		'Walite Notifications.app/Contents/MacOS/' 'Walite Notifications.app/Contents/MacOS/WaliteNotifier' \
		'Walite Notifications.app/Contents/Resources/' 'Walite Notifications.app/Contents/Resources/Walite.icns' \
		'Walite Notifications.app/Contents/_CodeSignature/' 'Walite Notifications.app/Contents/_CodeSignature/CodeResources' \
		>> "$verify_tmp/expected"
fi
sort "$verify_tmp/expected" > "$verify_tmp/expected.sorted"
tar -tzf "$release_dist/$archive" | sort > "$verify_tmp/actual"
diff -u "$verify_tmp/expected.sorted" "$verify_tmp/actual"
# No symlinks, hardlinks or special entries, even with otherwise valid names.
tar -tvzf "$release_dist/$archive" > "$verify_tmp/types"
if grep -v '^[d-]' "$verify_tmp/types" >/dev/null; then
	printf '%s\n' 'Unexpected archive entry type.' >&2
	exit 1
fi
mkdir "$verify_tmp/unpacked"
tar -xzf "$release_dist/$archive" -C "$verify_tmp/unpacked"
stage="$verify_tmp/unpacked"
test -f "$stage/walite" && test -x "$stage/walite"
cmp "$stage/LICENSE" "$root_dir/LICENSE"
cmp "$stage/README.md" "$root_dir/README.md"
description=$(file -b "$stage/walite")
case "$target:$description" in
	linux/amd64:*'ELF 64-bit'*'x86-64'*|linux/arm64:*'ELF 64-bit'*'ARM aarch64'*|\
	darwin/amd64:*'Mach-O 64-bit'*'x86_64'*|darwin/arm64:*'Mach-O 64-bit'*'arm64'* ) ;;
	* ) printf 'Unexpected binary architecture: %s\n' "$description" >&2; exit 1 ;;
esac

if [ "$(uname -s)" = Darwin ]; then
	test "$(stat -f %Lp "$stage/walite")" = 755
else
	test "$(stat -c %a "$stage/walite")" = 755
fi
if [ "$goos" = linux ]; then
	cmp "$stage/walite-notification.png" "$root_dir/assets/walite-notification.png"
	file -b "$stage/walite-notification.png" | grep 'PNG image data, 48 x 48' >/dev/null
else
	# Native signature verification is mandatory, never simulated on Linux.
	test "$(uname -s)" = Darwin
	app="$stage/Walite Notifications.app"
	/usr/bin/plutil -lint "$app/Contents/Info.plist"
	test "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$app/Contents/Info.plist")" = io.github.antoinebaudrimontbeep.walite.notifier
	test "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIconFile' "$app/Contents/Info.plist")" = Walite.icns
	helper_version=${version#v}
	test "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$app/Contents/Info.plist")" = "${helper_version%%-*}"
	test "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "$app/Contents/Info.plist")" = 5
	test -s "$app/Contents/Resources/Walite.icns"
	test -x "$app/Contents/MacOS/WaliteNotifier"
	case "$goarch" in amd64 ) swift_arch=x86_64 ;; arm64 ) swift_arch=arm64 ;; esac
	test "$(lipo -archs "$app/Contents/MacOS/WaliteNotifier")" = "$swift_arch"
	/usr/bin/codesign --verify --strict "$app"
fi

case "$(uname -s)/$(uname -m)" in
	Linux/x86_64 ) native_target=linux/amd64 ;;
	Linux/aarch64|Linux/arm64 ) native_target=linux/arm64 ;;
	Darwin/x86_64 ) native_target=darwin/amd64 ;;
	Darwin/arm64 ) native_target=darwin/arm64 ;;
	* ) native_target=unknown ;;
esac
if [ "$native_target" = "$target" ]; then
	test "$("$stage/walite" --version)" = "walite $version"
	"$stage/walite" --help | grep 'walite --pair' >/dev/null
fi
printf 'verified %s: contents, permissions, architecture and checksum\n' "$archive"
