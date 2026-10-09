#!/bin/sh
set -eu

if [ -z "${ITERM_SESSION_ID:-}" ]; then
	printf '%s\n' 'Run this test inside the iTerm2 session to restore.' >&2
	exit 1
fi
app_path="${WALITE_NOTIFIER_BUILD_DIR:-"$HOME/Applications"}/Walite Notifications.app"
test_dir="${TMPDIR:-/tmp}/walite-notifier-manual-test"
test_label=${1:-A}
test -d "$app_path"
umask 077
mkdir -p "$test_dir"
chmod 700 "$test_dir"

xcrun swift - "$ITERM_SESSION_ID" "$app_path" "$test_dir" "$test_label" <<'SWIFT'
import Foundation
let args = CommandLine.arguments
let raw = args[1].trimmingCharacters(in: .whitespacesAndNewlines)
let session = String(raw.split(separator: ":").last ?? "")
guard session.utf8.count == 36, let uuid = UUID(uuidString: session) else {
    fputs("Invalid iTerm2 session UUID.\n", stderr)
    exit(1)
}
let id = String(UInt64.random(in: 1_000_000_000...9_999_999_999))
let path = URL(fileURLWithPath: args[3]).appendingPathComponent("notification-\(id).json")
let request: [String: Any] = [
    "version": 1, "notification_id": id, "session_uuid": uuid.uuidString,
    "title": "Walite JSON test \(args[4])",
    "body": "Click to restore the iTerm2 session that posted this test."
]
var data = try JSONSerialization.data(withJSONObject: request)
data.append(10)
guard FileManager.default.createFile(atPath: path.path, contents: data,
                                     attributes: [.posixPermissions: 0o600]) else { exit(1) }
let process = Process()
process.executableURL = URL(fileURLWithPath: "/usr/bin/open")
process.arguments = ["-a", args[2], path.path]
try process.run()
process.waitUntilExit()
print("Test request: \(path.path)")
exit(process.terminationStatus)
SWIFT
