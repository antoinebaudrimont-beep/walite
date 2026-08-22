# Milestone 1A Results

## Scope

Milestone 1A contains the dependency-free model, validated configuration,
bounded offline fake source, in-memory store, retention policy, bounded service
pipeline, and synthetic textual demo. It excludes WhatsApp connectivity and
authentication, sending, media, SQLite/filesystem persistence, configuration
file parsing, and a terminal UI.

## Revision / environment

- Base revision tested: `cad4def` (`Add offline walite demo`), plus the
  uncommitted Increment 8 evidence files recorded here.
- Branch: `milestone/1a-offline-core`.
- Go: `go1.26.5 linux/amd64`.
- OS/architecture: Linux/amd64.
- CPU: Intel Core 2 Duo T9900 at 3.06 GHz.
- Date: 2026-08-22.

No username, hostname, address, or personal filesystem location is recorded.

## Validation

| Command | Result |
|---|---|
| `gofmt -w cmd/walite/*.go internal/config/*.go internal/model/*.go internal/service/*.go internal/store/*.go internal/syncpolicy/*.go internal/wa/*.go` | PASS; no frozen production diff |
| `go vet ./...` | PASS |
| `go test ./...` | PASS |
| `go test -race ./...` | PASS |
| `go test -race -count=2 ./internal/service ./cmd/walite` | PASS |
| `go test -run '^$' -bench . -benchmem ./internal/model ./internal/service ./internal/syncpolicy ./internal/store` | PASS |
| `go list -deps ./...` | PASS; reviewed below |
| `go list -m all` | PASS; only the walite module |
| `git diff --check` | PASS |
| `go run ./cmd/walite` | PASS; finite natural completion |

The planned race repetition was reduced from 10 to 2 because one race-enabled
100,000-record integration run took about 123 seconds on this target-like Core
2 Duo; two repeated runs completed in 237.437 seconds. The reduction and reason
are explicit rather than silently skipping repetition.

## Offline demo evidence

The observed demo printed `ready` before releasing history, showed the
synthetic live message before history completion, reported one real bulk
admission drop, retained a bounded final `100` messages and bodies, and stopped
naturally. History result batch sizes varied with scheduling. The run used the
offline `wa.FakeSource`; the dependency and import audits found no network API.

Representative terminal facts:

```text
walite — offline synthetic demo
ready
live chat=demo-chat message=demo-live-1 text="Synthetic live message"
...
degraded discarded=1
summary chats=1 messages=100 bodies=100 anchors=1 bytes=12317
stopped
```

## 100,000-record integration evidence

- Source records: exactly 100,000 through one lazy `wa.HistorySpec`; no
  100,000-element application message collection was constructed.
- Configured chunks: at most 32 records and 128 KiB.
- The first history-origin store write was blocked by a context-aware gate.
  A real `UpdateLive` was observed before that gate was released, proving live
  progress while traversal was incomplete.
- Traversal completed naturally, `Core.Run` joined, and `Updates` closed.
- Final usage: 1 chat, 100 messages, 100 bodies, 1 anchor, 14,732 estimated
  bytes—dramatically below 100,000 retained records.
- A separate 20-iteration test constructed, ran, joined, and discarded a new
  Core/source/store/policy graph each time without reusing Core.

These facts establish bounded application queues and lazy input. They do not
claim a measured RSS plateau or characterize server-decoded HistorySync.

## Benchmarks

Observed on this machine using the exact benchmark command above:

| Benchmark | ns/op | B/op | allocs/op | Extra metrics |
|---|---:|---:|---:|---|
| Normalize/max retained valid | 10,367 | 16,384 | 1 | 16 KiB retained input |
| Normalize/invalid UTF-8 prefix | 171,206 | 16,384 | 1 | bounded replacement path |
| Normalize/valid 1 MiB | 10,388 | 16,384 | 1 | bounded prefix work |
| Normalize/valid 8 MiB | 10,558 | 16,384 | 1 | bounded prefix work |
| Normalize/valid 32 MiB | 10,691 | 16,384 | 1 | bounded prefix work |
| Queue TryPut/available | 210.5 | 24 | 1 | TryPut timed in fixed batches; take/release occurs with timer stopped |
| Queue TryPut/entry full | 334.6 | 24 | 1 | bounded rejection |
| Queue TryPut/byte full | 107.0 | 0 | 0 | bounded rejection |
| History/100,000 | 31,015,291,941 | 8,147,471,480 | 88,927,961 | 3,224 records/s; retained 100; history peak 4 entries/21,696 bytes; history-write peak 256 entries/22,842 bytes |
| Policy/eligible | 369.5 | 40 | 2 | decision only |
| Policy/too old | 368.0 | 40 | 2 | decision only |
| Policy/count full | 372.1 | 40 | 2 | decision only |
| Policy/cache full | 377.6 | 40 | 2 | decision only |
| Memory write/unique 1 | 46,014 | 124,920 | 16 | fresh store outside timed operation |
| Memory write/duplicate 1 | 2,494 | 1,336 | 7 | preseeded store |
| Memory write/unique 25 | 82,928 | 138,480 | 101 | fresh store outside timed operation |
| Memory write/duplicate 25 | 38,475 | 14,168 | 90 | preseeded store |
| Memory write/unique 50 | 119,820 | 152,312 | 176 | fresh store outside timed operation |
| Memory write/duplicate 50 | 73,284 | 27,104 | 165 | preseeded store |
| Memory prune end-to-end/100 | 416,032 | 240,632 | 1,267 | includes population and ApplyPrune |
| Memory prune end-to-end/1,000 | 3,245,915 | 1,326,097 | 12,254 | includes population and ApplyPrune |
| Memory prune end-to-end/10,000 | 35,542,107 | 12,335,289 | 113,365 | includes population and ApplyPrune |

The 100,000-record history benchmark confirms bounded observed queue depths,
but its high cumulative allocation traffic is a future optimization target.
All timings and allocation totals are observations, not portable guarantees.

## Dependency audit

Production dependency review confirms:

- `internal/model` imports only the standard library.
- `internal/config` imports only the standard library.
- `internal/wa` imports `internal/model`.
- `internal/store` imports `internal/model`.
- `internal/syncpolicy` imports `internal/model` and `internal/config`.
- `internal/service` imports `internal/model` and the standard library.
- `cmd/walite` wires config, model, service, store, syncpolicy, and the offline
  fake source.

Forbidden directions are absent: service does not import store, store does not
import service, syncpolicy does not import service, model imports no internal
package, and there is no `internal/common`, `internal/util`, or import cycle.
The production import search found none of `net`, `net/http`, `net/url`,
`crypto/tls`, `database/sql`, or `os/exec` under `cmd` or `internal`.

`go list -m all` returned only:

```text
github.com/antoinebaudrimont-beep/walite
```

## Acceptance checklist

| Milestone 1A requirement | Result and factual evidence |
|---|---|
| `internal/model` | PASS — bounded constructors, normalization tests, and benchmarks |
| service-owned interfaces | PASS — structural assignment tests compile |
| config value validation | PASS — boundary and aggregate tests; parser/XDG deferred |
| fake event source | PASS — bounded ownership/full/cancellation tests |
| in-memory store | PASS — write/page/retention/prune/usage tests; SQLite deferred |
| pure retention | PASS — count, age, cache decisions and benchmarks |
| bounded entry and bytes | PASS — queue, budget, mailbox saturation/release tests |
| cancellation | PASS — staged shutdown and race tests |
| textual demo | PASS — compiled synthetic offline executable terminates naturally |
| offline tests | PASS — no network dependency or behavior found |
| benchmarks | PASS — five groups above report allocations |
| acyclic package graph | PASS — `go list` and direct imports reviewed |
| demo ready before history | PASS — output/gate test |
| live precedence | PASS — unit barriers and 100k integration gate |
| full history path cannot delay live | PASS — saturated unit path and 100k integration |
| category admission <=4 | PASS — fixed 2/1/1 fake tokens; real protocol payload deferred |
| newest 100 / 90 days | PASS — policy/store tests and final integration count 100 |
| finite 1A metadata | PASS — bounded chat/message rows and one anchor; mutations/contacts deferred |
| huge strings respect queue memory | PASS — 1/8/32 MiB inputs retain one 16 KiB allocation |
| deterministic fake clock | PASS — manually advanced service tests |
| zero leaked owned goroutines | PASS — joins and repeated lifecycle test; no goroutine-count assertion |
| no media call | PASS — no media API/package; all media deferred |
| standard library only | PASS — module list contains only walite |
| required quality gates | PASS — format, vet, normal, race, reduced repeat, and diff checks |

SQLite, the TUI, WhatsApp network/authentication, real messages, real
HistorySync classification, sending, media, and remote older-history requests
are deferred exactly as planned and are not claimed by 1A.

## Known observations

- History result batch sizes are scheduler-dependent and update publication is
  intentionally coalesced.
- Benchmark timings and allocations describe this machine and synthetic data.
- The 100k traversal generated high cumulative allocations despite bounded
  live queues; this is not an RSS measurement.
- Memory prune benchmarks are explicitly end-to-end and include population.
- Server-decoded HistorySync memory behavior remains unknown until the later
  whatsmeow integration milestone.
