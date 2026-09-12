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

for command_name in go git tar gzip sha256sum; do
	if ! command -v "$command_name" >/dev/null 2>&1; then
		printf 'required release tool is unavailable: %s\n' "$command_name" >&2
		exit 1
	fi
done
if ! tar --version 2>/dev/null | grep 'GNU tar' >/dev/null 2>&1; then
	printf 'reproducible packaging requires GNU tar\n' >&2
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

targets=${WALITE_TARGETS:-"linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"}
release_tmp=$(mktemp -d "${TMPDIR:-/tmp}/walite-release.XXXXXX")
trap 'rm -rf "$release_tmp"' EXIT HUP INT TERM
mkdir -p dist

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

	tar --format=ustar --sort=name --mtime="@$source_date_epoch" \
		--owner=0 --group=0 --numeric-owner \
		-C "$stage" -cf "$tar_path" LICENSE README.md walite
	gzip -n -c "$tar_path" >"dist/$archive"
done

(
	cd dist
	LC_ALL=C sha256sum walite_"$version"_*.tar.gz >SHA256SUMS
	sha256sum -c SHA256SUMS
)
