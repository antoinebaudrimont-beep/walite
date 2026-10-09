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
