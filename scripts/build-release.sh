#!/bin/sh

set -eu

version=${1:-}
if [ -z "$version" ]; then
	printf 'usage: %s v<version>\n' "$0" >&2
	exit 2
fi

case "$version" in
	v[0-9]* ) ;;
	* )
		printf 'release version must start with v followed by a digit: %s\n' "$version" >&2
		exit 2
		;;
esac
case "$version" in
	*[!A-Za-z0-9._-]* )
		printf 'release version contains an unsupported character: %s\n' "$version" >&2
		exit 2
		;;
esac

for command_name in go git gzip; do
	if ! command -v "$command_name" >/dev/null 2>&1; then
		printf 'required release tool is unavailable: %s\n' "$command_name" >&2
		exit 1
	fi
done
release_tar=tar
if ! tar --version 2>/dev/null | grep 'GNU tar' >/dev/null 2>&1 && command -v gtar >/dev/null 2>&1; then
	release_tar=gtar
fi
if ! "$release_tar" --version 2>/dev/null | grep 'GNU tar' >/dev/null 2>&1; then
	printf 'reproducible packaging requires GNU tar\n' >&2
	exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
	release_checksum() { sha256sum "$@"; }
elif command -v shasum >/dev/null 2>&1; then
	release_checksum() { shasum -a 256 "$@"; }
else
	printf 'required checksum tool is unavailable (sha256sum or shasum)\n' >&2
	exit 1
fi

source_date_epoch=${SOURCE_DATE_EPOCH:-}
if [ -z "$source_date_epoch" ]; then
	source_date_epoch=$(git log -1 --format=%ct)
fi
case "$source_date_epoch" in
	''|*[!0-9]* )
		printf 'SOURCE_DATE_EPOCH must be a non-negative integer\n' >&2
	exit 2
		;;
esac

host_os=$(uname -s)
case "$host_os" in
	Linux ) default_targets='linux/amd64 linux/arm64' ;;
	Darwin ) default_targets='darwin/amd64 darwin/arm64' ;;
	* ) printf 'unsupported release build host: %s\n' "$host_os" >&2; exit 1 ;;
esac
targets=${WALITE_TARGETS:-"$default_targets"}
# Validate the entire target list before creating output. A Darwin archive is
# never published without a genuinely native-built Swift application.
for target in $targets; do
	case "$target" in
		linux/amd64|linux/arm64 ) ;;
		darwin/amd64|darwin/arm64 )
			if [ "$host_os" != Darwin ]; then
				printf 'macOS releases require native Swift compilation on macOS\n' >&2
				exit 1
			fi
			;;
		* ) printf 'unsupported release target: %s\n' "$target" >&2; exit 2 ;;
	esac
done
release_tmp=$(mktemp -d "${TMPDIR:-/tmp}/walite-release.XXXXXX")
trap 'rm -rf "$release_tmp"' EXIT HUP INT TERM
release_dist=${WALITE_DIST_DIR:-dist}
mkdir -p "$release_dist"

for target in $targets; do
	case "$target" in
		linux/amd64|linux/arm64|darwin/amd64|darwin/arm64 ) ;;
		* )
			printf 'unsupported release target: %s\n' "$target" >&2
			exit 2
			;;
	esac

	goos=${target%/*}
	goarch=${target#*/}
	archive="walite_${version}_${goos}_${goarch}.tar.gz"
	stage="$release_tmp/${goos}_${goarch}"
	tar_path="$release_tmp/${archive%.gz}"
	mkdir -p "$stage"

	printf 'building %s\n' "$target"
	ldflags="-s -w -X main.version=$version"
	CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
		go build -trimpath -buildvcs=false -ldflags="$ldflags" \
		-o "$stage/walite" ./cmd/walite
	cp LICENSE README.md "$stage/"
	chmod 0755 "$stage/walite"
	chmod 0644 "$stage/LICENSE" "$stage/README.md"

	if [ "$goos" = linux ]; then
		cp assets/walite-notification.png "$stage/"
		chmod 0644 "$stage/walite-notification.png"
		set -- LICENSE README.md walite walite-notification.png
	else
		helper_version=${version#v}
		helper_version=${helper_version%%-*}
		# Bump for subsequent published helper builds; the bundle ID stays fixed.
		WALITE_NOTIFIER_BUILD_DIR="$stage" WALITE_NOTIFIER_ARCH="$goarch" \
			WALITE_NOTIFIER_VERSION="$helper_version" WALITE_NOTIFIER_BUILD_NUMBER=4 \
			./platform/macos-notifier/build.sh
		set -- LICENSE README.md walite 'Walite Notifications.app'
	fi

	"$release_tar" --format=ustar --sort=name --mtime="@$source_date_epoch" \
		--owner=0 --group=0 --numeric-owner \
		-C "$stage" -cf "$tar_path" "$@"
	gzip -n -c "$tar_path" >"$release_tmp/$archive"
	mv "$release_tmp/$archive" "$release_dist/$archive"
done

(
	cd "$release_dist"
	LC_ALL=C release_checksum walite_"$version"_*.tar.gz >SHA256SUMS
	release_checksum -c SHA256SUMS
)
