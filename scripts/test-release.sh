#!/bin/sh
# Negative packaging checks against an already-built real Linux archive.
set -eu
version=${1:-}
release_dist=${2:-dist}
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
"$script_dir/verify-release.sh" "$version" linux/amd64 "$release_dist"
test_tmp=$(mktemp -d "${TMPDIR:-/tmp}/walite-release-tests.XXXXXX")
trap 'rm -rf "$test_tmp"' EXIT HUP INT TERM
archive="walite_${version}_linux_amd64.tar.gz"
mkdir "$test_tmp/dist" "$test_tmp/stage"
tar -xzf "$release_dist/$archive" -C "$test_tmp/stage"

reject_artifact() {
	(cd "$test_tmp/dist" && sha256sum "$archive" > SHA256SUMS)
	if "$script_dir/verify-release.sh" "$version" linux/amd64 "$test_tmp/dist" >"$test_tmp/result" 2>&1; then
		printf 'invalid artifact unexpectedly accepted: %s\n' "$1" >&2
		exit 1
	fi
	printf 'rejected %s\n' "$1"
}

# Deliberately synthetic data only; no real config or credentials are opened.
printf 'synthetic forbidden fixture\n' > "$test_tmp/stage/unexpected.txt"
tar -czf "$test_tmp/dist/$archive" -C "$test_tmp/stage" LICENSE README.md walite walite-notification.png unexpected.txt
reject_artifact 'unexpected payload'
tar -czf "$test_tmp/dist/$archive" -C "$test_tmp/stage" LICENSE README.md walite
reject_artifact 'missing notification icon'
mv "$test_tmp/stage/README.md" "$test_tmp/stage/README.original"
ln -s README.original "$test_tmp/stage/README.md"
tar -czf "$test_tmp/dist/$archive" -C "$test_tmp/stage" LICENSE README.md walite walite-notification.png
reject_artifact 'symlink in payload'
mv "$test_tmp/stage/README.original" "$test_tmp/stage/README.md"
chmod 0644 "$test_tmp/stage/walite"
tar -czf "$test_tmp/dist/$archive" -C "$test_tmp/stage" LICENSE README.md walite walite-notification.png
reject_artifact 'missing executable permissions'
cp "$release_dist/$archive" "$test_tmp/dist/$archive"
printf '%064d  %s\n' 0 "$archive" > "$test_tmp/dist/SHA256SUMS"
if "$script_dir/verify-release.sh" "$version" linux/amd64 "$test_tmp/dist" >"$test_tmp/result" 2>&1; then
	printf '%s\n' 'bad checksum unexpectedly accepted' >&2
	exit 1
fi
printf '%s\n' 'rejected bad checksum'
cp "$release_dist/$archive" "$test_tmp/dist/$archive.other"
(cd "$test_tmp/dist" && sha256sum "$archive.other" > SHA256SUMS)
if "$script_dir/verify-release.sh" "$version" linux/amd64 "$test_tmp/dist" >"$test_tmp/result" 2>&1; then
	printf '%s\n' 'checksum for a different filename unexpectedly accepted' >&2
	exit 1
fi
printf '%s\n' 'rejected checksum for a different filename'

if [ "$(uname -s)" = Linux ]; then
	if WALITE_TARGETS=darwin/arm64 WALITE_DIST_DIR="$test_tmp/forbidden-macos" \
		"$script_dir/build-release.sh" "$version" >"$test_tmp/result" 2>&1; then
		printf '%s\n' 'Linux unexpectedly produced a macOS release' >&2
		exit 1
	fi
	test ! -e "$test_tmp/forbidden-macos"
	if WALITE_NOTIFIER_BUILD_DIR="$test_tmp/forbidden-helper" \
		"$script_dir/../platform/macos-notifier/build.sh" >"$test_tmp/result" 2>&1; then
		printf '%s\n' 'Linux unexpectedly built a native helper' >&2
		exit 1
	fi
	test ! -e "$test_tmp/forbidden-helper"
	printf '%s\n' 'rejected native macOS packaging on Linux without creating outputs'
fi
