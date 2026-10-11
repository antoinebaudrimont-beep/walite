#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
	printf '%s\n' 'Usage: generate-icon.sh source.png output.icns' >&2
	exit 2
fi
source_png=$1
output_icns=$2
test -f "$source_png"
if [ -e "$output_icns" ]; then
	printf '%s\n' "Icon already exists: $output_icns" >&2
	exit 1
fi

properties=$(/usr/bin/sips -g format -g pixelWidth -g pixelHeight "$source_png")
format=$(printf '%s\n' "$properties" | awk '$1 == "format:" { print $2 }')
width=$(printf '%s\n' "$properties" | awk '$1 == "pixelWidth:" { print $2 }')
height=$(printf '%s\n' "$properties" | awk '$1 == "pixelHeight:" { print $2 }')
case "$width:$height" in
	*[!0-9:]*|:*|*:) printf '%s\n' 'Invalid source image dimensions.' >&2; exit 1 ;;
esac
if [ "$format" != png ] || [ "$width" -ne "$height" ] || [ "$width" -lt 1024 ]; then
	printf '%s\n' 'Icon source must be a square PNG at least 1024 pixels wide.' >&2
	exit 1
fi

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/walite-icon.XXXXXX")
trap 'rm -rf -- "$work_dir"' 0
iconset="$work_dir/Walite.iconset"
mkdir "$iconset"
for size in 16 32 128 256 512; do
	/usr/bin/sips -z "$size" "$size" "$source_png" --out "$iconset/icon_${size}x${size}.png" >/dev/null
	retina_size=$((size * 2))
	/usr/bin/sips -z "$retina_size" "$retina_size" "$source_png" --out "$iconset/icon_${size}x${size}@2x.png" >/dev/null
done
/usr/bin/iconutil -c icns "$iconset" -o "$work_dir/Walite.icns"
mkdir -p "$(dirname -- "$output_icns")"
test ! -e "$output_icns"
mv "$work_dir/Walite.icns" "$output_icns"
