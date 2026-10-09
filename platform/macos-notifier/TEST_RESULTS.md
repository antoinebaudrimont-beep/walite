# Validation results — 2026-10-09

Test host: macOS 27.0.1, arm64, Apple Swift 6.4 from the installed Command Line
Tools. Repository HEAD remained `a1d3cce40ecc2f62e50cfe25bd2a67021cc87d14`.

| Check | Result |
| --- | --- |
| Swift request parser | 24 checks passed: exact keys/types, Unicode and escapes, malformed/incomplete JSON, version validation, UUID validation, empty title/body, two distinct targets |
| Swift helper typecheck | Passed with `-warnings-as-errors` |
| Shell scripts | Syntax passed; missing-session test rejected; build refused to overwrite an existing helper |
| Actual macOS build | Passed; final plist validated; required bundle ID present; strict code-signature verification passed |
| Runtime dependencies | Only macOS frameworks and system Swift libraries |
| Go-encoded live requests | Two requests generated with `json.Encoder` and `SetEscapeHTML(false)` accepted; macOS delivery records used the exact supplied notification IDs |
| Successful request cleanup | Both live request files deleted after notification acceptance |
| Live malformed requests | Invalid JSON, version 2 and missing session UUID rejected; all three files retained; no notification requested for them |
| Manual click A after posting B | User confirmed window return; helper logged success; read-only iTerm2 check confirmed A's exact session selected and B's window still minimized |
| Manual click B | User confirmed window return; helper logged success; read-only check confirmed B's exact session selected |
| Window and helper lifetime | iTerm2 window count unchanged; helper still running after both clicks |
| Delivered manual test script | Generated and opened a valid request; exited zero; request file deleted |
| Scope preservation | Original prototype and all tracked Go/backend/worker/mute files unchanged; no commit made |

The first live permission test exposed an error from simultaneous authorization
calls. The second request was retained on disk. Authorization is now serialized
and pending requests keep their own payloads; the final build accepted both
reposted requests and deleted both files. A fresh permission-state repeat of the
queue fix was not performed; the final two-request run used the permission
already granted during the first test.

Repository checks: `go vet ./...` passed. `go test ./...` and
`go test -race ./...` passed for `cmd/walite` and all other packages except
`internal/config` and `internal/tui`, whose unchanged XDG-path tests fail on this
Mac. `gofmt -l` still flags the unchanged `internal/wa/text_sender.go`; no Go
formatting was modified.

Remaining limits: logout/reboot, helper quit/relaunch, revoked permissions, and
simultaneous production Walite instances have not been validated. Authorization
or scheduling failures retain requests for explicit retry or the Go backend's
24-hour abandoned-file cleanup. Production selection and release packaging are
outside this task.

## Task 3D — Swift IPC integration, 2026-10-09

Baseline branch `feat/mute-notifications`, HEAD
`37ae272ef8208f5dc85ee1aa68cf721f3aee5695`; clean checkout before this task.
Same macOS 27.0.1 arm64 host and Apple Swift 6.4 compiler. This section covers
the modified sources, not the earlier installed app's manual click results.

| Check | Result |
| --- | --- |
| `./platform/macos-notifier/test.sh` | Passed: 24 existing parser checks and 86 activation/IPC/restoration checks |
| Activation parsing | Passed: valid/absent/incomplete/null/wrong types, invalid ChatIDs, UTF-8 byte bounds, socket path and token bounds; invalid metadata preserves ordinary requests |
| Independent notification targets | Passed: A after B, distinct sessions/endpoints/tokens, UUID binding, one-time consumption, replacement/failure cleanup and 256-entry production retention bound (capacity-two test) |
| Receiver protocol | Passed: exactly `version`, `chat_id`, `token`; version 1; Unicode/escaping roundtrip; two native private Unix receivers observed complete commands and EOF |
| Failure/security cases | Passed: missing socket, closed listener, public socket/parent, symlink, same-user connected peer and stable socket identity on successful sends |
| Timeout/cancellation | Passed: expired send deadline, real saturated socket write wait, mid-wait cancellation, eight-job admission limit, queued cancellation on shutdown, queue delay counted toward one-second deadline |
| Swift typecheck | Passed with `-warnings-as-errors` for all four production Swift files |
| Native build | Passed in a separate temporary build directory; installed helper was not replaced or launched |
| Bundle/signature/dependencies | Plist lint and stable bundle ID passed; accessory app flag present; ad hoc strict signature valid; only system libraries/frameworks |
| Shell syntax | `sh -n` passed for build, test and existing manual request scripts |
| Window restoration | Traversal exactly matches baseline after stripping indentation; only a timeout wrapper was added. Native AppleScript compilation and UUID/injection checks passed; no modified-app manual window click performed |
| Existing Go activation tests | `go test ./cmd/walite ./internal/tui -run 'Test(ChatActivation\|RunChatActivation)' -count=1` and corresponding `-race` run passed, including draft/pending-send guards |
| Repository static checks | `go vet ./...` passed; `git diff --check` passed |
| Scope/install preservation | All changes confined to this directory. Installed executable and plist SHA-256 values unchanged; no generated app or binary in the repository; no commit or push |

The required full `go test ./...` and `go test -race ./...` were run **before
copying the Swift changes into the still-clean repository**. Both failed at this
baseline on this Mac: XDG/platform path expectations, unsafe cache/media paths,
media fixture failures and rejected connection fixtures. Affected packages:
`cmd/walite`, `internal/config`, `internal/mediacache`, `internal/service`,
`internal/store`, `internal/tui`, `internal/wa`. Existing activation-specific
tests pass separately. `gofmt -l` flags the unchanged
`internal/wa/text_sender.go`. No Go source was changed or reformatted, and no
WhatsApp authentication/connection was performed.

The initial local receiver failure fixture used `shutdown` on a listening socket;
on this host that did not close the listener. The fixture now closes its owned
descriptor and the final failure check passes. This was a test-fixture fix, not
a production transport change.

Remaining manual validation: the modified app's notification A/B clicks through
to receiver/chat selection; stopped receiver and ordinary/malformed metadata
still restoring windows; a composing draft staying in its current conversation.
See README for the sequence. Previous manual delivery/restoration remains valid
evidence for the original installed helper only.

Limits: there is no receiver acknowledgement or retry, and cancellation cannot
undo an already sent command. Chat activation is lost after helper restart or
eviction beyond 256 retained targets, while session restoration remains in
`userInfo`. Five seconds is the AppleEvent timeout, not a guaranteed total
window-traversal duration. Same-user malicious processes are outside the Unix
permission boundary. Release signing, packaging, reboot behavior and installing
the modified build are outside this task.

## Task 3E — native integration validation, 2026-10-09

Preparation verified HEAD `37ae272ef8208f5dc85ee1aa68cf721f3aee5695` on
`feat/mute-notifications`; changes remained confined to this directory. Repeated
Swift tests passed (24 + 86 checks), complete typecheck passed with warnings as
errors, shell syntax and whitespace checks passed, and the native app built and
passed bundle/signature verification before installation.

The original helper was archived privately outside Applications. Every bundle
file matched the archive byte-for-byte; a second retained copy has a `.bundle`
suffix, and neither backup was registered with Launch Services. Original
executable SHA-256:
`ac1d8f8bdab99e8231f773ae475090212cefa92cf4639e1d08dc03f01af97141`.
The original plist SHA-256 remained
`b9bc960ab0d02bd3db06662a90a8c4d5de3387a68b6c04ec0893193703a3e3b4`.

The old helper stopped cleanly before replacement at the original application
path. The new helper's running executable path was checked through
`NSRunningApplication` and `lsof`, with a fresh process ID after the old process
had exited. Its executable SHA-256 is
`c5f698b247f1232fb9c424da6ced376662ce4d45a0014cfcf6f1e76d24e62935`;
ad hoc CDHash `fe966d354eb08e9e91a70f9812cb40536adfac33`; strict signature valid;
bundle ID `io.github.antoinebaudrimontbeep.walite.notifications`. Bundle version
is still 1 / 0.1; the hash and fresh running process distinguish the new build.
No notification or Automation permission reset was performed. A syntax-checked
rollback script and verified original helper remain available outside Applications.

The latest unmodified Go executable was rebuilt separately and ran Test A.
The normally installed Walite binary was never replaced; its original hash
matched again at completion. Only one Walite process ran at a time.

The user could not arrange messages from contacts for the remaining tests. A
temporary Go build overlay outside the repository supplied a local-only entry
point and synthetic event/settings adapter. It used the unchanged production
application receiver, notification worker/backend, service, SQLite cache, TUI,
and realtime mute worker. Only the transport/settings inputs were synthetic.
No repository Go files were edited, no new dependencies were added, and the
fixture opened its separate test cache without opening the WhatsApp session DB.
Startup/shutdown and `go vet` checks passed for this fixture before manual use.

| Test | Result and evidence scope |
| --- | --- |
| A | **Passed live.** User confirmed a new incoming B notification restored the exact minimized iTerm2 window and selected B in the latest real Walite run. Helper logged restoration and command dispatch. |
| B | **Passed locally.** Synthetic A then B notifications came through the actual Go pipeline. User clicked B, confirming Local B selection; then clicked the earlier A, confirming restoration and switch back to Local A. |
| C | **Passed locally.** While composing an unfinished synthetic draft in Local A, the user clicked a fresh Local B notification. Window restored; Local A and the exact draft remained unchanged. Dispatch was logged but UI rejection/preservation was verified by the user. |
| D | **Passed.** An ordinary Go-generated notification with empty ChatID omitted IPC metadata. User confirmed normal window restoration. Relevant click logged restoration without activation dispatch. |
| E | **Passed locally.** User exited the local Walite normally, then clicked its retained B notification. Window returned to the shell; no Walite restarted. Logs showed restoration followed by the expected fixed activation-failure status. |
| F | **Passed locally; live account check pending.** Synthetic muted input exercised the actual Go mute worker. The message committed to the isolated cache, and the user observed no desktop notification. The adapter supplied synthetic mute settings; real WhatsApp mute-store lookup/delivery was not tested. |

Completion checks: installed new-helper process remained unchanged and alive;
zero Walite processes after Test E; zero pending request files in both live and
fixture request directories; no new Walite/helper crash reports; final strict
signature and executable hash verified. Logs were filtered to fixed helper
statuses, excluding message text, ChatIDs, socket endpoints and tokens. Unified
info-log retention is incomplete, so cumulative log counts were not treated as
complete delivery statistics or proof of TUI acceptance.

No helper implementation defect appeared in the tested paths, and no further
production changes were required. During the switch to local testing, the user
initially reported readiness while the real process was still running; the
process check prevented posting fixture events, and that run was stopped through
its existing SIGINT shutdown handler before the fixture started.

Remaining limits: live-account muted-message/settings behavior; simultaneous
multiple Walite instances; helper restart/reboot with retained notifications;
revoked permissions; malformed-metadata native clicks (parser behavior is covered
automatically). Native A/B checks used one iTerm2 session for the local fixture;
independent session/socket targets remain covered by the prior automated tests.
The receiver still provides no acknowledgement or retry, and activation
credentials intentionally disappear when the helper exits or evicts old targets.
No commit, push, merge, tag, release or Linux/mute-filtering change was made.

## Option A — local terminal icon, 2026-10-09

Baseline HEAD `0a2682180b390fca31338973e081f2c17ae2b04e`, branch
`feat/mute-notifications`, initially clean. Changes are limited to the build-time
icon selector, build/test scripts, focused tests and documentation in this
directory. The four notification/IPC/restoration Swift files and Go code are
unchanged. No new helper was installed or launched and no permissions were reset.

| Check | Result |
| --- | --- |
| Existing Swift suite | 24 parser + 86 IPC/restoration checks passed unchanged |
| New icon suite | 45 checks passed: all five terminal identity mappings, parent fixtures, environment/path hints, override precedence, conflicting/unknown/SSH signals, registered identity mismatch, symlinks, corrupt/missing/undeclared icons, copied bytes and plist preservation |
| Selector typecheck / shell syntax | Passed with warnings as errors; `sh -n` passed for build, test and existing manual script |
| Real automatic iTerm build | Passed in a disposable iTerm session with no explicit override: selected `/Applications/iTerm.app` and its declared `iTerm2 App Icon for Release.icns` |
| Explicit iTerm override | Passed; same icon resource selected and copied byte-for-byte |
| Explicit Apple Terminal override | Passed: `/System/Applications/Utilities/Terminal.app`, declared `Terminal.icns`; confirms no default iTerm selection |
| No reliable terminal / invalid override | Both native builds succeeded with generic icon: no `CFBundleIconFile` key and no copied icon resource |
| Parent-only live iTerm probe | Safe generic fallback. With all environment hints removed, ancestors included a detached session server outside the terminal app bundle; no reliable `.app` identity was inferred. Parent detection itself is covered with five bundle fixtures. |
| Generated bundles | Six separate builds passed plist lint, unchanged helper bundle identity, exact expected resource bytes (or absence for fallback), file-list checks and strict ad hoc signature verification |
| Icon configuration | Successful builds use `Contents/Resources/WaliteTerminal.icns` and `CFBundleIconFile = WaliteTerminal.icns`; resources installed before signing |
| Overwrite protection | Default build refused the currently installed app before writing any resources |
| Preservation / scope | Installed helper executable/plist and rollback archive/original executable hashes unchanged; no generated binaries/icons/apps in Git; `git diff --check` passed |

The current Codex build context has `TERM=dumb`, no terminal-identifying
environment signals and no terminal app ancestor; its generic fallback is
intentional. Normal iTerm environment detection was separately checked in an
actual iTerm build. Ghostty, Alacritty and WezTerm were exercised with synthetic
bundle fixtures, not native installed copies on this host.

During development the SDK's computed process-path-size macro was replaced with
its equivalent Swift expression. Disposable fixture bitmap/color and URL
directory/symlink comparisons were corrected before the final 45 checks passed.
The user closed disposable test windows; both build completion markers showed
success and all saved output bundles verified. Closing the final ancestor-probe
window caused only its AppleScript window-ID lookup to fail after its diagnostic
log had already completed; that log confirmed the safe parent-only fallback.

Limits: only a readable, valid `.icns` named by `CFBundleIconFile` is copied;
asset-catalog-only or missing resources keep the generic icon. Detached terminal
servers, inherited/unknown environment values, and IDE/multiplexer contexts can
require `WALITE_TERMINAL_APP`. Icon choice does not add terminal restoration
support; restoration continues to use the unchanged iTerm2 mechanism. macOS may
cache app/notification icons by bundle identity and path. Visual notification
appearance after a future controlled install is untested; no cache/permission
reset was attempted. Copied terminal artwork remains only in local generated
apps and must not be included in Git or release archives. No commit or push made.


## Option A — controlled installation and visual validation, 2026-10-09

Baseline HEAD remained `0a2682180b390fca31338973e081f2c17ae2b04e` on
`feat/mute-notifications`. All 155 Swift checks (24 parser, 86 activation/IPC,
45 icon), selector typecheck, shell syntax and whitespace checks passed before
installation. Notification/IPC/restoration sources and Go code remained unchanged.

The currently working IPC helper was preserved in a private archive and two
retained `.bundle` copies outside Applications. All original bundle files matched
the archive and backup hashes, and strict signatures verified. A syntax-checked
rollback script retains the backups when restoring. The earlier Task 3E rollback
backup also remained unchanged; no duplicate backup was registered with Launch
Services.

A fresh build ran inside iTerm2 without `WALITE_TERMINAL_APP`. It detected
`/Applications/iTerm.app`, copied its declared
`iTerm2 App Icon for Release.icns` byte-for-byte, and configured
`CFBundleIconFile = WaliteTerminal.icns`. Bundle ID remained
`io.github.antoinebaudrimontbeep.walite.notifications`. The old process stopped
cleanly before replacement at the existing Applications path. A fresh process
was verified using `NSRunningApplication`, its executable path in `lsof`, and
installed file hashes matching the new build. New executable SHA-256:
`379a4511c036e4aa670d0072ca60dd984b1d60478617c772a53688a0e4d3f46b`.
Strict ad hoc signature verification passed after installation and at completion.

| Check | Result |
| --- | --- |
| Installed bundle resource and identity | Passed: exact copied icon bytes, correct plist key, unchanged ID and valid signature |
| macOS application-icon lookup | Passed: `NSWorkspace.icon(forFile:)` rendered the iTerm2 artwork, matching the raw copied resource |
| Launch Services | Only the installed current helper was registered for its bundle ID; registration contained the correct primary icon and resource path |
| Notification Centre icon | **Failed / unresolved:** user screenshots and repeated confirmations showed the generic icon |
| Notification settings icon | **Failed / unresolved:** user screenshot also showed the generic icon for Walite Notifications; the separate older prototype entry remained present |
| Click-to-chat | **Passed locally:** user confirmed exact minimized iTerm2 window return and Local B selection; helper logged restoration and dispatch |
| Draft protection | **Passed locally:** after clicking B while composing in Local A, Local A stayed selected and the unfinished test draft was unchanged; restoration and dispatch were logged |
| Delivery and cleanup | Fresh local notifications delivered; zero pending fixture request files at completion |
| Runtime safety | Same installed helper remained running, no new Walite/helper crash reports or helper permission-failure statuses; local test Walite exited normally |

The existing validated synthetic Go fixture was reused with user agreement. It
exercises the unchanged receiver, notification pipeline and TUI with separate
local data; no real WhatsApp connection or new Go modification was needed.
Manual outcomes establish TUI behavior separately from IPC dispatch logs.
Unified info-log retention was incomplete, so cumulative event counts are not
complete delivery statistics.

The icon investigation force-refreshed only the installed app's Launch Services
registration, restarted the current user's Notification Centre UI and `usernoted`
service, then updated the app directory modification time and restarted the
current user's `iconservicesagent`. Fresh notifications remained generic after
each attempt. Bundle file hashes and signatures still verified. No cache files,
notification databases, permission records or app settings were deleted/reset;
no system-wide icon service was restarted. System error records associated with
these test notifications reported no matching item to delete; they did not
establish an icon-loading or permission failure.

The exact notification-icon cause remains unconfirmed. Stale notification-specific
metadata is a hypothesis, supported by the correct system application icon and
registration, but the permitted refresh attempts did not resolve it. Logout/reboot
and application-version changes were not tested. No Swift implementation or bundle
identifier change was made. There was no observed delivery/navigation regression,
so the new build remains installed with verified rollback available. Copied artwork
exists only in local application/backup resources, not in Git or release assets.
No commit, push, merge, tag or release was made.


## Build-number icon refresh experiment — 2026-10-09

Repository HEAD stayed `0a2682180b390fca31338973e081f2c17ae2b04e` on
`feat/mute-notifications`. Existing icon-related changes were preserved. The only
implementation edit in this task changed the build script's `CFBundleVersion`
from `1` to `2`; `CFBundleShortVersionString` stayed `0.1`, the helper bundle ID
stayed `io.github.antoinebaudrimontbeep.walite.notifications`, and icon selection,
notification/IPC/restoration sources and production Go files remained unchanged.

The user reported that the original notification icon remained generic after a
full reboot. A separate prior native app with a fresh identity and byte-identical
iTerm2 icon displayed the iTerm2 icon after its separate permission was enabled
and its native request acceptance was verified. That supports an identity-specific
notification metadata hypothesis, without establishing the exact internal cause.

| Check | Result |
| --- | --- |
| Existing Swift suite | Passed: 24 parser + 86 activation/IPC/restoration + 45 terminal-icon checks |
| Shell syntax and whitespace | Passed |
| Automatic native build | Passed in iTerm2 without an override; detected `/Applications/iTerm.app` and copied the same declared iTerm2 ICNS bytes |
| Bundle metadata | Only plist difference from installed build 1 was `CFBundleVersion = 2`; version `0.1`, bundle ID and icon configuration unchanged |
| Backup | Private build-1 archive and two retained `.bundle` copies matched every original bundle-file hash; strict signatures verified; persistent rollback script syntax checked |
| Controlled replacement | Old helper stopped cleanly; new running process and executable path verified; installed files matched the tested build |
| Launch Services | One registration at the expected Applications path; build `2`, display version `0.1`, correct icon resource recorded |
| Finder/system icon | Passed: system lookup rendered iTerm2 artwork and user confirmed the Finder icon remained correct |
| Signature | Strict ad hoc verification passed before and after installation |
| Existing permissions / delivery | Passed: native request accepted, user reported no new permission prompt; no permission/database/cache reset performed |
| NEW notification icon | **Failed:** the new Local B notification still displayed the generic icon |
| Click-to-chat | **Passed locally:** user confirmed exact minimized window restoration and Local B selection; helper logged restoration and IPC dispatch |
| Request cleanup / safety | Request file removed, no pending fixture requests or new helper/Walite crash reports; local fixture exited normally |

The old temporary fixture had been removed by reboot. A new local-only Go build
overlay outside the repository reused the unchanged application service, receiver,
notification backend/worker and TUI with a synthetic realtime source, isolated
SQLite cache and A/B labels. Its static checks, build, startup and clean shutdown
passed before manual use. No real WhatsApp connection, installed Walite binary
replacement or production Go file edit was needed. IPC logs were content-free;
UI acceptance was confirmed independently by the user.

Conclusion: incrementing the build number from 1 to 2 **did not refresh the
notification icon on this Mac**, despite macOS recognizing build 2 and correct
Finder artwork. Version increment alone is not a validated fix for the existing
identity's notification icon. Exact cache/registration behavior remains unknown;
no stronger causal claim is established. No notification logic, IPC logic,
bundle ID, short version, system cache or permission changes were made. There was
no observed functional regression, so build 2 remains installed with verified
build-1 rollback available. Draft protection was not manually repeated in this
experiment; its implementation and existing automated checks are unchanged.
No commit, push, merge, tag or release was made.


## Official Walite artwork and permanent identity — 2026-10-09

HEAD remained `0a2682180b390fca31338973e081f2c17ae2b04e` on
`feat/mute-notifications`. Before changes, all current helper source files,
including the uncommitted terminal detector/tests and previous documentation,
were archived privately with verified file hashes. Obsolete terminal detection
was then replaced by the dedicated supplied artwork; its historical results
above are preserved.

The official single source is `assets/walite-icon.png`, a byte-for-byte copy of
the supplied `a_clean_minimal_high_resolution_flat_modern_icon.png`. It is a
1254 × 1254 RGB PNG without alpha. Source SHA-256:
`05a05984fff1c06ae38463e45d8e1fb54c670f8f5aa750859a31f54fa1b7ed1f`.
No artwork redesign, recoloring, cropping, background or transparency edit was
performed. Future builds use the repository PNG without needing the Desktop file.
macOS `sips` generates five standard point sizes and their Retina representations;
`iconutil` packages the ten PNGs as `Walite.icns`. All intermediates remain temporary.
Linux icon support is a separate future task.

The permanent bundle ID and logging subsystem are now
`io.github.antoinebaudrimontbeep.walite.notifier`. App name and installed path are
unchanged. Build number is 3, display version 0.1. Notification/JSON/IPC/chat
activation/restoration logic is unchanged; the only edit in the runtime Swift
source is the logger subsystem string. No production Go/Linux/mute code changed.

| Automated check | Result |
| --- | --- |
| Notification parser | 24 existing checks passed |
| Activation, IPC and restoration | 86 existing checks passed |
| Official artwork/build suite | 41 checks passed: PNG integrity/decoding/dimensions, native conversion, all ten standard/Retina sizes, repeat ICNS bytes, source preservation, invalid/missing/small/non-square input rejection, output overwrite protection, new identity/name/version/accessory behavior, expected app files and signature |
| Swift typecheck | All four runtime files passed with warnings as errors |
| Shell syntax / whitespace | All build/generator/test/manual scripts passed; `git diff --check` passed |
| Native helper build | Passed from repository source and PNG in a separate build directory; no terminal identification/override needed |
| Signature / dependencies | Resources added before signing; strict ad hoc signatures valid; no new third-party dependency |
| PNG preservation | Repository and Desktop source hashes matched exactly |
| Local regression fixture | External Go overlay static checks/build/startup/clean shutdown passed; separate synthetic cache; no WhatsApp connection or production file edits |

During test development, the initial staging directory did not reproduce the
repository's two-level relative asset path; the fixture layout was corrected.
The initial file-list assertion also encountered temporary-path symlink aliases;
URLs were normalized after app creation. Conversion, representation checks and
signing themselves passed. Final 151 native checks passed before replacement.

Repository-required Go validation: `go vet ./...` passed. `go test ./...` and
`go test -race ./...` failed with the previously documented Mac baseline categories:
XDG/platform path expectations, unsafe temporary cache/media paths, media preview
fixtures and rejected connection fixtures. Affected packages: `cmd/walite`,
`internal/config`, `internal/mediacache`, `internal/service`, `internal/store`,
`internal/tui`, `internal/wa`. No race warning was reported. `gofmt -l` still flags
unchanged `internal/wa/text_sender.go`. Those diagnostics were reviewed after the
passing native build was installed; no Go source was changed to resolve unrelated
baseline failures.

Controlled installation preserved the complete old build-2 helper in a verified
private archive and two retained `.bundle` copies outside Applications, together
with original hashes/signature/plist and a syntax-checked rollback script that
handles both identities. Previous backup directories were not removed or edited.
The old process stopped cleanly; installed files matched the tested build and
signature. Exactly one new helper process ran from the expected executable path,
with the new bundle ID verified via `NSRunningApplication` and `lsof`.
The system application-icon lookup rendered the supplied Walite artwork.
No permission database, system cache or existing permission record was reset.

### Manual visual and functional validation

| Check | Result |
| --- | --- |
| Finder | **Passed:** user confirmed supplied Walite artwork |
| Notification Centre | **Passed:** after native request acceptance and cleanup were verified, user confirmed the new Local B notification and click worked with the Walite icon |
| Notification settings | **Passed:** user confirmed the new entry displayed Walite artwork; old generic entries were retained unchanged |
| Notification permission | **Passed:** initial authorization stayed pending; user enabled notifications for the new identity only, then the queued request was accepted and deleted without restarting the helper |
| A — correct minimized window | **Passed locally:** user confirmed window return after the new notification click |
| B — correct tab/session | **Passed locally:** from another tab in the same window, clicking earlier A restored the original Walite tab/session |
| C — originating chat | **Passed locally:** B selected Local B; A selected Local A through the unchanged authenticated receiver/TUI |
| D — independent targets | **Passed locally:** A then B were posted; B was clicked first, then earlier A, and each selected its own chat |
| E — unfinished draft | **Passed locally:** clicking fresh B restored the window while Local A and its exact unfinished draft remained unchanged |
| F — no restart after exit | **Passed locally:** user exited normally, then clicked retained B; window returned to the shell, with no new Walite process |
| G — unavailable IPC | **Passed locally:** same post-exit click logged successful restoration and expected fixed activation-failure status; helper stayed alive and user saw no error/crash |
| Delivery/request cleanup | Fresh requests accepted; zero pending fixture requests at completion |
| Runtime safety | One unchanged new-helper PID remained running; zero Walite processes after exit; zero new Walite/helper crash reports |

The actual notification remained queued during the first icon confirmations,
so those initial reports were not treated as proof of native delivery. The user
then enabled the new entry's notification switch. Only after request acceptance
and removal were verified was the actual titled notification's icon and click
checked again. System-wide permissions, old entries, databases and caches were
never reset or deleted. Any iTerm2 Automation prompt was handled by the user;
no permission bypass was attempted.

The unchanged Go receiver/backend/worker, application service and TUI were used
with synthetic A/B labels and an isolated cache. This is local regression evidence,
not a new live WhatsApp-account test. A different tab in the same terminal window
was used for the session restoration check. Independent session/socket targets
remain covered by the unchanged automated suite. IPC dispatch logs alone were
not treated as UI acceptance; the user separately confirmed chat and draft states.
Unified info-log retention is incomplete, so cumulative log counts are not
complete delivery statistics.

Final bundle metadata/signature and every retained old-helper/source archive hash
verified again. Launch Services had one entry for the new identity at the expected
installation path, build 3/version 0.1, with `Walite.icns`. No implementation defect
or functional regression appeared; the new official-icon helper remains installed
and the complete previous-helper rollback is available. The supplied PNG is the
only new repository artwork; generated apps/ICNS/iconsets and validation backups
are outside Git. Notification/IPC logic and production Go/Linux/mute filtering
are unchanged.

Remaining limitations: live WhatsApp-account retest under the new identity;
logout/reboot and helper restart with retained notifications; revoked permissions;
simultaneous production Walite instances; cross-macOS-toolchain byte reproducibility;
release signing/notarization/packaging. In-memory activation targets still disappear
when the helper exits, as documented in the existing contract. The new identity
resolved the tested icon display, but the exact old identity's metadata/cache cause
remains unconfirmed. Linux icon support is a separate future task.

No commit, push, merge, tag or release was made. Required visual confirmation was
received; further publication/commit work awaits the user's separate instruction.
