#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
build_dir=${WALITE_CLICK_PROTOTYPE_DIR:-"${TMPDIR:-/tmp}/walite-click-notification-prototype"}
app_path="$build_dir/Walite Notification Prototype.app"

mkdir -p "$build_dir"
chmod 700 "$build_dir"

if [ -e "$app_path" ]; then
	printf '%s\n' "Prototype already exists: $app_path" >&2
	printf '%s\n' "Remove that disposable prototype app before rebuilding." >&2
	exit 1
fi

osacompile -o "$app_path" "$script_dir/WaliteNotificationPrototype.applescript"
printf '%s\n' "$app_path"
