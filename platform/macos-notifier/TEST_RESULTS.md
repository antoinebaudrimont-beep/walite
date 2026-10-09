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
