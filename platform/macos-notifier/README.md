# Native macOS JSON notification helper

See [TEST_RESULTS.md](TEST_RESULTS.md) for the on-Mac automated and manual results.

Standalone helper for the version-1 request written by
`cmd/walite/notification_helper_backend.go`. Production Go selection and workers
are not connected to this helper yet. The validated implementation under
`prototypes/macos-click-notification/` is preserved.

## Contract

The JSON object must contain `version` (integer 1), `notification_id` (nonblank
string), `session_uuid` (36-byte hexadecimal UUID with hyphens), `title` (string),
and `body` (string). Snake-case keys match the Go serialization. Empty title and
body strings are accepted because the Go backend permits them. Pane prefixes
are not accepted: Go normalizes the session UUID before writing the request.

The supplied notification ID is used unchanged. Each delivered notification
stores its own normalized UUID in `userInfo["session_uuid"]`. No latest-session
state is shared between notifications. Only a validated UUID enters the tested
AppleScript window/tab/session traversal; title and body never enter that script.
Authorization is requested once at a time; requests arriving during a permission
prompt remain queued with their individual IDs, UUIDs and file paths.

A request file is deleted only after `UserNotifications.add` succeeds. Invalid
JSON, unsupported versions, missing/wrongly typed fields, invalid UUIDs,
authorization denial, and scheduling failures retain the file. The Go backend
already removes abandoned owned request files after 24 hours. Logs contain fixed
status messages only, without notification text, contact names, UUIDs or paths.

## Build and parser checks

From the repository root on macOS, with Apple Command Line Tools installed:

```sh
./platform/macos-notifier/test.sh
./platform/macos-notifier/build.sh
```

The build creates `~/Applications/Walite Notifications.app`, with bundle ID
`io.github.antoinebaudrimontbeep.walite.notifications` and an ad hoc signature.
It refuses to overwrite an existing app. `WALITE_NOTIFIER_BUILD_DIR` can select a
different build directory; use an Applications location for the notification
test, because macOS must find and validate the app. No third-party runtime is
required. The app remains running as an accessory app to receive clicks.

## Manual click and two-session test

1. In the first iTerm2 session, run
   `./platform/macos-notifier/post-test-request.sh A`. Allow notifications for
   **Walite Notifications** if prompted.
2. In a different iTerm2 session, run the same script with `B`.
3. Check that both printed request paths disappear after scheduling. Retain
   both notifications in Notification Centre.
4. Minimize the first session's window and click **Walite JSON test A** after B
   has been posted. Allow iTerm2 Automation access if prompted. Verify the exact
   original window, tab and session return.
5. Minimize the second window and click **Walite JSON test B**. Verify its exact
   session returns. If both sessions share a window, switch tabs between clicks
   and verify each session separately.

For rejection checks, open disposable files containing invalid JSON, a version-2
object, or a version-1 object missing `session_uuid` with
`open -a "$HOME/Applications/Walite Notifications.app" request.json`.
They must remain on disk and produce no notification. An `open` exit code of zero
only means Launch Services dispatched the request; it does not confirm delivery.

Logout/reboot click delivery, helper termination/relaunch, revoked Automation
permission, and multiple simultaneous production Walite instances are not yet
validated. Ad hoc signing and this build script are development tooling, not
release packaging.
