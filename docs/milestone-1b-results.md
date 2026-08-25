# Milestone 1B Results

## Scope and decision

Milestone 1B delivers the direct-tcell offline prototype, synthetic local chat
data, simulated-screen coverage, and an offline benchmark/observability harness.
It adds no WhatsApp connectivity, networking, authentication, SQLite, media, or
real messages.

**Decision: PASS WITH DOCUMENTED DEVIATIONS.** The prototype acceptance suite
passes. The remaining package-boundary and production-wiring deviations below
must be resolved as part of Milestone 2 application integration; this decision
does not claim that the current executable is connected to the Milestone 1A
service pipeline.

## Revision and target machine

- Base revision: `4b3bb94` (`Complete Milestone 1B TUI polish`), plus the
  uncommitted closure evidence described here.
- Branch: `milestone/1a-offline-core`.
- Date: 2026-08-25.
- Go: `go1.26.5 linux/amd64`.
- OS/kernel: Linux `6.12.101+deb13-amd64`, amd64.
- CPU: Intel Core 2 Duo T9900 at 3.06 GHz.
- Machine role: primary old-MacBook target described by `AGENTS.md`, running MX
  Linux. These are target-machine measurements, not CI assertions.

No network or external service is included in any measurement.

## Functional acceptance

| Requirement | Result |
|---|---|
| Direct tcell lifecycle and synthetic TUI | PASS |
| Simulated-screen tests | PASS |
| Cold and warm first-frame harness | PASS |
| Compose operation and input-redraw harness | PASS |
| Redraw observability | PASS; current renderer is full-screen per changed event |
| Idle redraw | PASS; zero additional `Show` calls while idle |
| Unicode/fallback behavior | PASS in simulation |
| Narrow/very-small behavior | PASS in simulation |
| Interactive TUI before fake history completes | PASS in deterministic integration simulation |
| Context, Escape, and Ctrl-C restoration | PASS; `Fini` exactly once and event owner joined |
| Required validation | PASS |

## First-frame measurements

Command:

```sh
go test ./internal/tui -run '^$' -bench '^(BenchmarkFirstFrame|BenchmarkCompose)' -benchmem -benchtime=500ms -count=5
```

The first-frame timer begins immediately before `runWithDependencies` and ends
at the first simulated-screen `Show`. Cold iterations use fresh configuration
and chat-state paths; warm iterations use previously saved files. It measures
offline TUI initialization, loading, model construction, drawing, and the first
show, but not OS process creation or a physical terminal emulator.

| Case | Five-sample range | Median | Goal |
|---|---:|---:|---:|
| Cold first frame | 5.017-7.115 ms/op | 6.179 ms/op | <= 1.5 s |
| Warm first frame | 6.647-8.409 ms/op | 7.525 ms/op | <= 750 ms |

The goals are design targets and are intentionally not wall-clock test
assertions.

## Compose measurements

The same five-run command produced:

| Operation | Five-sample range | Median |
|---|---:|---:|
| ASCII rune insertion | 60.25-81.80 ns/op | 70.77 ns/op |
| UTF-8 rune insertion | 63.73-89.41 ns/op | 73.16 ns/op |
| Family-emoji grapheme insertion | 102.9-148.0 ns/op | 130.3 ns/op |
| Left/right cursor movement | 1.459-2.321 us/op | 1.597 us/op |
| Backspace plus fixture restoration | 486.1-776.6 ns/op | 580.0 ns/op |
| Compose input plus full redraw/show | 2.052-3.065 ms/op | 2.366 ms/op |

A separate fixed 1,000-sample simulated compose-input test measured p50
`1.595694 ms`, p95 `6.942696 ms`, and maximum `22.021328 ms`. This passes the
50 ms p95 design goal on this machine; it is not a CI timing assertion.

## Redraw and idle behavior

Simulation at 100x30 (resize at 60x20) reported:

| Operation | Clear | Show | Approximate cleared + written cells |
|---|---:|---:|---:|
| First frame | 1 | 1 | 5,427 |
| Compose key input | 1 | 1 | 5,428 |
| One synthetic live update | 1 | 1 | 5,524 |
| 32-message history burst, coalesced draw | 1 | 1 | 5,645 |
| Resize to 60x20 | 1 | 1 | 2,480 |

The renderer calls `Clear` and rebuilds the full frame for every changed event;
it is not dirty-region rendering. The cell total counts the full clear area plus
content writes and is therefore approximate. The deterministic idle test sees
only the initial frame and no further `Show`: idle redraw rate is 0 frames/s.

## Unicode and terminal sizes

Simulated acceptance covers ASCII, `Café`, `界`, `❤️`, `👍🏽`,
`👨‍👩‍👧‍👦`, and `🇦🇹`. Composer data remains the original grapheme.
The picker draws the grapheme where supported or a visible fallback without
substituting fallback data into the composer.

The simulated size matrix passes at 120x40, 80x24, 60x20, 40x8, and 30x6.
Widths of 70 cells or more use two panes; widths below 70 use one pane. Heights
below 8 use the very-small fallback. Draw and resize do not panic, and compose,
picker, draft, and valid scroll state survive the matrix.

Terminal- and font-specific complex-emoji rendering can still differ from the
simulation.

## Service ordering and terminal restoration

The deterministic `cmd/walite` integration test starts the existing Milestone
1A service fake with history blocked, starts the actual TUI lifecycle, observes
the first frame, enters compose mode, and verifies history is still blocked.
It then releases history and joins both owners. This proves the components can
remain interactive concurrently in simulation. It does not claim production
service updates are wired into the TUI; `main.go` still starts only the TUI.

Context cancellation, navigation-mode Escape, and Ctrl-C each return from
`Run`, call `Fini` exactly once after successful initialization, and join the
TUI-owned event goroutine. Failed initialization is not finalized.

## Architecture audit

The desired dependency direction remains `cmd/walite -> config, service,
store, wa, tui` and `tui -> model, service`. No dependency direction was
changed in this closure increment.

| Location | Deviation | Severity | Smallest future correction |
|---|---|---|---|
| `internal/tui/tui.go`, `internal/tui/view.go` | TUI imports `internal/config` to load `config.json`, retain `config.UI`, and render read-only settings values. | Blocker before Milestone 2 application integration | Load configuration in `cmd/walite`; inject a small immutable TUI options value (and storage boundary where needed). |
| `internal/tui/chat_store.go` and TUI startup | TUI owns JSON persistence for chats/messages, while final application data ownership belongs behind service/store. | Temporary prototype deviation | Preserve the schema/file for now; during Milestone 2, expose service/store state to the TUI and make an explicit JSON retirement or migration decision. |
| `internal/tui/preferences.go` and TUI startup | Emoji MRU path and file I/O are owned inside the TUI. | Temporary prototype deviation | Inject a preference store/path from executable wiring if this storage remains after Milestone 2 integration. |
| `cmd/walite/main.go` versus `cmd/walite/demo.go` | Production `main` starts only `tui.Run`; the existing service/store/fake wiring in `demo.go` is not connected to the TUI. TUI imports neither model nor service today. | Blocker before Milestone 2 application integration | Make `cmd/walite` the composition root, start service before history completion, and pass bounded service-backed view/update APIs into TUI. |
| `internal/tui/view.go` | Every changed event clears and redraws the whole terminal. | Temporary prototype performance deviation | Introduce measured dirty regions after the service-backed update model is defined; preserve zero idle redraw. |

Removing `tui -> config` cleanly requires changing startup/options ownership and
is therefore deliberately deferred rather than hidden inside this evidence
increment. The prototype JSON schema and `~/.local/share/walite/state.json` are
unchanged.

## Known limitations

- Complex emoji had historical terminal rendering artifacts. Full logical-cell
  redraw and fallback tests pass, but terminal/font-specific behavior can vary.
- JSON state is prototype persistence, not the Milestone 2 SQLite cache.
- Settings are read-only in the TUI.
- Full-frame event redraw is measured, not optimized.
- The service/TUI integration test runs both real component lifecycles with the
  existing fake barriers, but production update delivery is not wired yet.

## Validation

| Command | Result |
|---|---|
| `gofmt -w` on changed Go files | PASS |
| `go mod tidy -diff` | PASS; no module-file diff |
| `go vet ./...` | PASS |
| `go test ./...` | PASS |
| `go test -race ./...` | PASS |
| `git diff --check` | PASS |

Reproduce measurements on another target machine with the benchmark command
above. Results from any machine other than the environment recorded here must
be labeled separately rather than substituted for these measurements.

## Milestone 2.1 follow-up

Milestone 2.1 resolves the two blocking application-integration deviations
identified above: `internal/tui` no longer imports `internal/config`, and
production startup now constructs, starts, cancels, and joins the existing
offline `service.Core` around the TUI lifecycle. Configuration loading and the
`config.UI` to `tui.Options` mapping now belong to `cmd/walite`.

This follow-up does not alter the historical Milestone 1B measurements or claim
that TUI chat data is service-driven. Prototype JSON chat persistence, emoji
preference I/O, synthetic TUI data, and full-screen event redraw remain as
documented temporary deviations.
