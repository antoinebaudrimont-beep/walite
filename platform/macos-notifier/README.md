# Native macOS JSON notification helper

See [TEST_RESULTS.md](TEST_RESULTS.md) for the on-Mac automated and manual results.

Standalone helper for the version-1 request written by
`cmd/walite/notification_helper_backend.go`. The validated implementation under
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

Version 1 optionally supplies `chat_id`, `activation_socket` and
`activation_token` as a complete group. A missing group preserves ordinary
notifications. Incomplete, null, wrongly typed or invalid activation fields
disable chat activation while preserving delivery and window restoration.
Chat IDs use Go's neutral identifier rules: nonblank, at most 512 UTF-8 bytes,
no control characters; they are never resolved or rewritten. Socket paths must
be absolute, at most 103 UTF-8 bytes, without NUL, trailing slash or `.`/`..`
components. Tokens must be the receiver's 64 lowercase hexadecimal characters.

Activation targets are held only in a private in-memory mapping keyed by
notification ID and bound to its session UUID. `userInfo` still contains only
the session UUID. Scheduling failures remove the corresponding target; clicks
consume it once. The map retains at most 256 targets, evicting the oldest at
capacity. Replacement IDs cannot inherit stale credentials. Quitting the helper
loses chat activation for previously delivered notifications; their persisted
session UUID still permits window restoration when macOS delivers the click.

The click callback finishes immediately and queues the existing AppleScript
traversal on the main queue (at most eight pending clicks, with a five-second
AppleEvent timeout). Successful restoration precedes asynchronous IPC. At most
eight socket jobs are admitted; queueing, connection and writes share a
one-second deadline. A cancelled job exits at the next bounded poll (at most
20 milliseconds apart); termination cancels pending jobs. An unavailable socket
does not undo window restoration. Failed restoration skips activation.

Before sending credentials, the helper checks a same-user 0700 parent directory,
a same-user 0600 socket, no symlink at either endpoint, the connected peer UID,
and unchanged socket identity. It does not change endpoint permissions. This
protects against other users, not malicious processes running as the same user.
IPC uses native Darwin sockets, never a shell. The command has exactly
`version: 1`, `chat_id` and `token`, and the write side is closed for EOF framing.
There is no reply, acknowledgement or retry. The Go receiver/TUI remains the
authority: composing a draft or another guarded UI state rejects activation.
Cancellation cannot retract bytes already delivered, and `sent` does not imply
that a conversation was selected. Logs never include IDs, paths or credentials.

A request file is deleted only after `UserNotifications.add` succeeds. Invalid
JSON, unsupported versions, missing/wrongly typed fields, invalid UUIDs,
authorization denial, and scheduling failures retain the file. The Go backend
already removes abandoned owned request files after 24 hours. Logs contain fixed
status messages only, without notification text, contact names, UUIDs or paths.

## Build and automated checks

From the repository root on macOS, with Apple Command Line Tools installed:

```sh
./platform/macos-notifier/test.sh
./platform/macos-notifier/build.sh
```

The build creates `~/Applications/Walite Notifications.app`, with bundle ID
`io.github.antoinebaudrimontbeep.walite.notifier` and an ad hoc signature.
It refuses to overwrite an existing app. `WALITE_NOTIFIER_BUILD_DIR` can select a
different build directory; use an Applications location for the notification
test, because macOS must find and validate the app. No third-party runtime is
required. The app remains running as an accessory app to receive clicks.

`test.sh` also exercises activation parsing, independent targets, exact JSON
serialization, two private local Unix receivers, EOF framing, unavailable and
unsafe endpoints, stalled socket timeout, cancellation and bounded queueing.
It compiles the restoration AppleScript without executing it. No WhatsApp
connection, notification permission change or installed app replacement occurs.

To build an isolated validation copy without replacing the installed helper:

```sh
WALITE_NOTIFIER_BUILD_DIR=$(mktemp -d /private/tmp/walite-notifier-build.XXXXXX) \
  ./platform/macos-notifier/build.sh
```

### Official Walite icon and permanent identity

[`assets/walite-icon.png`](../../assets/walite-icon.png) is the single artwork
source. It is a byte-for-byte copy of the supplied official PNG (1254 × 1254,
RGB). The original appearance, background and source resolution are preserved;
no redraw, recoloring, cropping or transparency changes are applied.

`generate-icon.sh` uses macOS `sips` and `iconutil` to make the standard 16, 32,
128, 256 and 512 point representations, each at 1× and 2× (16 through 1024
pixels). Temporary PNG representations are packed into
`Contents/Resources/Walite.icns`; `CFBundleIconFile` names that file. Resources
are installed before ad hoc signing and strict verification. Intermediate
iconsets and generated ICNS files are build outputs; keep them and compiled apps
out of Git. The Desktop image is not needed for future builds. Repeated builds
are checked for identical ICNS bytes with the current macOS tools; other tool/OS
versions are not guaranteed to produce identical compressed bytes.

The source must decode as a square PNG at least 1024 pixels wide. Missing,
malformed, undersized or rectangular sources fail the build without publishing
an app; there is no silent artwork substitution or upscaling. Both the generator
and ordinary app build refuse existing outputs. Terminal icon detection,
`WALITE_TERMINAL_APP` and its obsolete tests have been removed. Their prior work
was archived privately before removal; historical results remain in TEST_RESULTS.md.
Linux use of the same PNG is a separate future task; Linux notifications and
packaging are unchanged here.

The permanent bundle identifier is
`io.github.antoinebaudrimontbeep.walite.notifier`. Logging uses the same subsystem.
The app name/path remain **Walite Notifications** and
`~/Applications/Walite Notifications.app`, so existing Go discovery is unchanged.
Notification JSON, IPC, chat selection and iTerm2 restoration are unchanged.
This artwork/identity update is build **3**, display version **0.1**. Maintain
`CFBundleVersion` explicitly in build.sh and increase it for published helper
updates; maintain the display version separately.

The previous identity's notification icon remained generic despite correct
Finder artwork, reboot and a build-number increment. A fresh-identity native
probe displayed the same terminal icon correctly. That motivated this permanent
identity change; the exact internal macOS metadata cause remains unconfirmed.

### Controlled installation and permissions

An ordinary build never overwrites an installed helper. For updates:

1. Build into a fresh separate directory and run `test.sh` first.
2. Record the installed bundle ID, build number, signature and file hashes.
   Make a verified private archive/`.bundle` backup outside Applications. Do not
   register a duplicate `.app` backup with Launch Services. Retain older backups.
3. Quit the currently running helper cleanly and verify it has exited.
4. Replace the app at the existing path with the verified build. Check all copied
   files and the final signature. Launch this exact path and verify the new
   process, executable path and bundle ID; avoid duplicate helper processes.
5. Allow notifications for **Walite Notifications** under the new identity when
   macOS asks. Allow iTerm2 Automation access on a new notification click if asked.
   Keep existing permission records, caches and system databases intact.
6. Inspect a **new** notification, Finder and Notification settings visually.
   Check exact window/tab/session restoration, originating-chat selection,
   independent A/B targets, unfinished drafts and missing IPC. Request acceptance
   and IPC dispatch logs are not proof that the UI changed conversations.
7. If functionality regresses, stop the new helper and restore the verified
   backup at the same path, then verify its signature and process identity.

Old notification permission entries may remain under the previous identity.
Previously delivered notifications belong to that identity and cannot validate
the new helper. Chat activation targets are in memory and disappear when the
helper exits, so always generate fresh requests for testing after replacement.
No system-wide permission, database or cache reset is part of this procedure.
The 2026-10-09 controlled installation passed human confirmation in Finder,
Notification Centre and Notification settings. Local A/B clicks, exact tab/session
restoration, draft protection and post-exit/missing-IPC safety passed. The new
notification permission was enabled by the user; old records and caches remained
intact. See TEST_RESULTS.md for evidence scope and remaining limits.

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

## Additional manual IPC validation

Use a separate validated build selected explicitly by its app path. macOS may
confuse two registered copies with the same bundle ID; deliberately select the
test copy and retain the currently working installed app. Do not reset permissions.

1. With a local test receiver or a Walite test instance, create a private 0600
   request in a 0700 directory using the contract above. Obtain the socket and
   token from that receiver; never put them in notification text or test logs.
2. Post A and B for two different sessions/conversations and independent receiver
   targets. Keep both notifications. Minimize A's window, click A after B, then
   repeat for B. Verify exact window/tab/session restoration and the corresponding
   receiver's command/chat selection. Use synthetic IDs with a local receiver;
   no WhatsApp connection is needed to check delivery of the command.
3. Stop the receiver before clicking a fresh notification. Window restoration
   must still succeed. Repeat with no activation fields and malformed activation
   fields: those notifications must still restore their sessions.
4. For the full TUI check, compose a draft, then click a notification for another
   conversation. The window must return while the current chat and draft remain.
   The receiver provides no acknowledgement; use the UI to verify this outcome.

Task 3E has now validated the modified installed helper: a real incoming
notification selected its originating conversation; local fixture notifications
validated B-then-A targeting, draft preservation, ordinary restoration and
safe clicks after Walite exit. The local fixture used the actual Go receiver,
backend, service, TUI and mute worker with a separate synthetic cache, without
opening a WhatsApp session database. Its muted message produced no desktop
notification. Real-account mute/settings delivery, multiple simultaneous
instances and malformed-metadata native clicks remain untested. See
`TEST_RESULTS.md` for the distinction between live and fixture evidence.

Logout/reboot click delivery, helper termination/relaunch, revoked Automation
permission, and multiple simultaneous production Walite instances are not yet
validated. Ad hoc signing and this build script are development tooling, not
release packaging.
