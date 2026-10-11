#!/bin/sh
set -eu

if [ -z "${ITERM_SESSION_ID:-}" ]; then
	printf '%s\n' "ITERM_SESSION_ID is missing; run this command inside the iTerm2 session to restore." >&2
	exit 1
fi

build_dir=${WALITE_CLICK_PROTOTYPE_DIR:-"$HOME/Applications"}
app_path="$build_dir/Walite Click Notification Prototype.app"
request_dir=${WALITE_CLICK_PROTOTYPE_REQUEST_DIR:-"${TMPDIR:-/tmp}/walite-click-notification-prototype"}
request_path="$request_dir/iterm-session.txt"

if [ ! -d "$app_path" ]; then
	printf '%s\n' "Prototype app not found. Run build-prototype.sh first." >&2
	exit 1
fi

umask 077
mkdir -p "$request_dir"
chmod 700 "$request_dir"
printf '%s\n' "$ITERM_SESSION_ID" > "$request_path"
open -a "$app_path" "$request_path"
