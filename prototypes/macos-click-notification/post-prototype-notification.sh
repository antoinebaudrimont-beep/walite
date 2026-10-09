#!/bin/sh
set -eu

if [ -z "${ITERM_SESSION_ID:-}" ]; then
	printf '%s\n' "ITERM_SESSION_ID is missing; run this command inside the iTerm2 session to restore." >&2
	exit 1
fi

build_dir=${WALITE_CLICK_PROTOTYPE_DIR:-"${TMPDIR:-/tmp}/walite-click-notification-prototype"}
app_path="$build_dir/Walite Notification Prototype.app"
request_path="$build_dir/iterm-session.txt"

if [ ! -d "$app_path" ]; then
	printf '%s\n' "Prototype app not found. Run build-prototype.sh first." >&2
	exit 1
fi

umask 077
printf '%s\n' "$ITERM_SESSION_ID" >"$request_path"
open -a "$app_path" "$request_path"
