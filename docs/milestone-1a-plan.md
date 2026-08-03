# Milestone 1A implementation plan

This plan covers only the dependency-free offline core described by
`docs/architecture.md`. It is an implementation plan, not an authorization to
connect to WhatsApp or to begin a later milestone. Every proposed production
package uses only the Go standard library.

## 1. Current repository state

The following is the repository state inspected before this plan was created.
The working tree was clean.

```text
.
├── .gitignore
├── AGENTS.md
├── README.md
├── go.mod
├── cmd/
│   └── walite/                 (empty)
├── docs/
│   ├── architecture.md
│   └── reference-analysis.md
└── internal/
    ├── config/                 (empty)
    ├── service/                (empty)
    ├── store/                  (empty)
    ├── syncpolicy/             (empty)
    ├── tui/                    (empty)
    └── wa/                     (empty)
```

There are also empty workspace-control directories `.agents/` and `.codex/`.
They contain no project files. Empty directories are not represented by Git.
`internal/model` does not exist yet and must be created by the first coding
increment.

- Module path: `github.com/antoinebaudrimont-beep/walite`.
- Go version declared by `go.mod`: `1.24.4`.
- Locally available toolchain during planning: `go1.26.5 linux/amd64`, which can
  build a module whose `go` directive is 1.24.4.
- Current branch: `milestone/1a-offline-core`.
- Current revision: `0176c28 Define walite architecture`.
- `go.mod` contains only the module and Go directives; it has no dependencies.
- No Go package exists yet, so package test/vet commands become meaningful only
  after increment 1 creates the first `.go` files; this is not a blocker.

No repository issue blocks Milestone 1A. The empty packages are intentional
scaffolding. Two non-blocking documentation observations should not be changed
inside this milestone plan: `AGENTS.md` does not yet list the architecture's
new `internal/model` package, and its final `text` fence is not closed. The
explicit task and current architecture establish the model package and the
validation commands, so implementation can proceed; either documentation fix
requires a separately scoped change.

The repository also has no project licence file. Because 1A is independently
implemented using only the standard library, that does not block coding or
tests, but release/provenance work must resolve it before distribution.

The architecture mentions an `errgroup`/`WaitGroup` owner. Because
`golang.org/x/sync/errgroup` is not in the standard library, Milestone 1A will
use `sync.WaitGroup`, `context.WithCancelCause`, and a bounded first-error
channel. This preserves owned cancellation without adding a dependency.

## 2. Scope

Milestone 1A contains exactly:

- minimal values and bounded constructors in `internal/model`;
- narrow interfaces owned by `internal/service`;
- dependency-free configuration values and validation;
- a bounded fake event source;
- an in-memory chat/message store;
- a pure retention policy;
- a priority mechanism bounded by both entries and bytes;
- context cancellation and joined goroutine shutdown;
- a non-interactive textual demonstration using synthetic data;
- offline unit, integration, race, saturation, and allocation tests;
- dependency-free benchmarks.

It explicitly excludes:

- tcell or any other TUI library;
- SQLite, SQL schemas, migrations, WAL, or filesystem cache enforcement;
- whatsmeow, protobufs, QR authentication, or linked-device state;
- network access or any live integration test;
- real messages, contacts, phone numbers, JIDs, or personal data;
- sending or an automation/command API;
- media metadata, `DownloadRef`, attachments, downloads, or prefetch;
- XDG directory creation, configuration parsing, and persistence.

The 250 MiB cache setting is a design value and deterministic estimator input
in 1A. It is not represented as an actual SQLite/filesystem guarantee until the
storage milestone.

Implementation is independent. Reference repositories remain read-only; no
file or symbol is copied into 1A. The plan uses only the already documented
consumer-owned seams, bounded workers, and paged-query concepts.

## 3. Small implementation increments

Each increment is independently reviewable. Later increments may modify files
introduced earlier only where listed. No increment adds a module dependency.

### Increment 1 — minimal model values and bounded constructors

Files:

- create `internal/model/model.go`;
- create `internal/model/normalize.go`;
- create `internal/model/normalize_test.go`.

Types and functions:

- immutable `ChatID`, `MessageID`, `Message`, and `Event` values with
  unexported representation and read-only accessors;
- `MessageInput` as the raw constructor input;
- `ValidationCode` and `ValidationError`;
- hard constants for identifier, body, and normalized-event bytes;
- `NewChatID`, `NewMessageID`, `NewMessage`, and
  `NewEvent(message, receivedAt)`;
- `NormalizeText` and exact `Message.ByteSize`/`Event.ByteSize` accessors.

Tests cover empty/valid/oversized identifiers, exact limits, valid and malformed
UTF-8, truncation at a rune boundary, zero message/received timestamps, huge
input strings, immutable ownership, and the 32 KiB event ceiling.

Validation:

```text
gofmt -w internal/model/*.go
go vet ./internal/model
go test ./internal/model
go test -race ./internal/model
```

Completion means no constructor can return a value beyond its declared bound,
no returned value aliases mutable input, and the package imports only the
standard library. Rollback removes only the three files above. There are no
dependencies on another increment.

### Increment 2 — configuration values and validation

Files:

- create `internal/config/values.go`;
- create `internal/config/values_test.go`.

Types and functions:

- `Values`, `Retention`, `Queues`, `History`, and `Batch` structs;
- `DefaultValues() Values`;
- `func (Values) Validate() error` and `func (Retention) Validate() error` for
  the pure policy constructor;
- typed `Field`, `ValidationRule`, and `ValidationError` values that identify
  the exact failing field and bound without matching error strings.

Tests cover every lower/upper boundary, zero and negative values, integer
overflow-safe aggregate byte-budget addition, the 6.5 MiB queue-budget ceiling,
history-chunk consistency, and unchanged valid defaults.

Validation:

```text
gofmt -w internal/config/*.go
go vet ./internal/config
go test ./internal/config
go test -race ./internal/config
```

Completion means defaults validate, every unsafe value fails with a typed
field error, and config has no internal-package import. Rollback removes the
two files. This increment is independent of increment 1.

### Increment 3 — service-owned contracts and bounded fakes

Files:

- create `internal/model/history.go`;
- create `internal/model/history_test.go`;
- create `internal/model/update.go`;
- create `internal/model/update_test.go`;
- create `internal/config/limits_external_test.go`;
- create `internal/service/interfaces.go`;
- create `internal/service/interfaces_external_test.go`;
- create `internal/wa/fake_source.go`;
- create `internal/wa/fake_source_test.go`.

Types and functions:

- immutable, constructor-bounded `HistoryJobID`, `HistoryClass`, `HistoryJob`,
  `HistoryChunk`, `SourceStatus`, `UpdateKind`, and `Update` model values;
- service-owned `EventSource`, `Clock`, and `Timer` interfaces, limited to the
  methods that compile at this increment; persistence/policy contracts follow
  with their model values in increment 4;
- `wa.FakeSource`, bounded `wa.HistorySpec` and `wa.ScriptStep`,
  `NewFakeSource`, `Run`, `TryRealtime`, `NextHistory`, and `ReleaseHistory`;
  `TryHistory` is the nonblocking test/demo admission method used by `Run`;
- model `SourceFailureKind` (`SourceFull`, `SourceClosed`, `SourceRejected`) and
  content-free `wa.SourceError` with `Error`, `Unwrap`, and
  `SourceFailureKind` methods, so the fake exposes that code structurally and
  does not need service error types;
- compile-time structural assignments in the external service contract test.

The fake source owns a fixed one-slot real-time output, three independent
history-job outputs (metadata 2, on-demand 1, bulk 1), and one fixed status slot
with a capacity-one wake. The same 2/1/1 limits are category admission tokens:
a token remains occupied after a job is delivered until `ReleaseHistory`; a
full category sheds the new fake application import and publishes degraded
status. `Run` is the sole closer of output channels. It closes admission before
closing those channels, so concurrent `TryRealtime`/`TryHistory` calls return a
typed source-closed error. Already delivered jobs have a separate cursor
lifetime: `NextHistory` remains valid with its own context after `Run` closes,
and `ReleaseHistory` always frees the cursor and token. Tests use barriers
rather than sleeps and cover 2/1/1 admission, full, unknown, duplicate job IDs,
lazy traversal after normal source completion, release after closure,
cancellation, concurrent injection/shutdown, and channel ownership.
The fake tests also cover exact script byte/entry limits and clearing processed
step references.
`history_test.go` covers every class, invalid/zero jobs, 32-record/128-KiB
chunk boundaries, overflow, defensive copy, indexed access, and source-status
counter access. `update_test.go` covers all eight kinds, per-kind required IDs,
32-KiB accounting, and absence of body/string aliases. The external config
limit test proves defaults and accepted maxima do not exceed model event/chunk
bounds without adding a config production import.

Validation:

```text
gofmt -w internal/config/*.go internal/model/*.go internal/service/*.go internal/wa/*.go
go vet ./internal/config ./internal/model ./internal/service ./internal/wa
go test ./internal/config ./internal/model ./internal/service ./internal/wa
go test -race ./internal/config ./internal/model ./internal/service ./internal/wa
```

Completion means the fake source satisfies the service-owned interface without
importing `internal/service`, all values crossing the interface are model or
standard-library values, and shutdown leaves no blocked fake producer.
Rollback removes the files listed for this increment and restores the model to
increment 1 while retaining config value files. Dependencies: increments 1–2.

### Increment 4 — in-memory store and pure retention policy

Files:

- create `internal/model/retention.go`;
- create `internal/model/retention_test.go`;
- create `internal/model/store.go`;
- create `internal/model/store_test.go`;
- create `internal/store/memory.go`;
- create `internal/store/memory_test.go`;
- create `internal/syncpolicy/policy.go`;
- create `internal/syncpolicy/policy_test.go`;
- extend `internal/service/interfaces.go` with `MessageStore` and
  `RetentionPolicy`;
- extend `internal/service/interfaces_external_test.go` with store/policy
  structural assignments.

Types and functions:

- model `Chat`, `MessageAnchor`, `Cursor`, `WriteOrigin`, `WriteBatch`,
  `MessageSummary`, `RetentionState`, `RetentionDecision`,
  `RetentionSnapshot`, `PrunePlan`, `PruneResult`, and `CacheUsage`;
- `NewChat`, bounded display-name normalization, and an immutable
  `Message.WithoutBody` transformation used for metadata-only anchor admission
  and explicit pruning;
- `store.Memory`, `MemoryOptions`, `NewMemory`, `UpsertChats`, `Chat`,
  `ApplyBatch`, `MessagesBefore`, `RetentionSnapshot`, `ApplyPrune`, and
  `CacheUsage`;
- content-free `store.Error` with `Error`, `Unwrap`, and structural
  `MetadataLimitKind` classification;
- `syncpolicy.Policy`, `New(config.Retention) (*Policy, error)`, `Decide`, and
  `PlanPrune`.

Tests cover structural compatibility, `(chat ID, message ID)` identity,
duplicate upsert, deterministic timestamp/ID ordering, bounded queries,
placeholder-chat creation, a shared named/placeholder chat cap, cache
estimation, count pruning, the exact age
boundary, opaque collection constructor bounds, and cancellation before bounded
store operations.
Model tests independently cover the cursor/no-cursor contract, chat and anchor
accessors, 50-item `WriteBatch`, 551-summary snapshot/prune limits, defensive
copies, negative/overflowing cache counters, invalid retention enums, and
`PruneResult` accounting.

Validation:

```text
gofmt -w internal/model/*.go internal/store/*.go internal/syncpolicy/*.go internal/service/*.go
go vet ./internal/model ./internal/store ./internal/syncpolicy ./internal/service
go test ./internal/model ./internal/store ./internal/syncpolicy ./internal/service
go test -race ./internal/model ./internal/store ./internal/syncpolicy ./internal/service
```

Completion means the store and policy satisfy service contracts structurally,
the store imports only model, syncpolicy imports only model/config, and the
newest-100/90-day/metadata limits are deterministic. Rollback removes the
increment files and the corresponding external assertions. Dependencies:
increments 1–3.

### Increment 5 — bounded entry-and-byte queue primitives

Files:

- create `internal/service/budget.go`;
- create `internal/service/queue.go`;
- create `internal/service/mailbox.go`;
- create `internal/service/budget_test.go`;
- create `internal/service/queue_test.go`;
- create `internal/service/mailbox_test.go`.

Types and functions are unexported implementation details: `byteBudget`,
`permit`, `boundedQueue[T]`, `lease[T]`, and `fixedMailbox[K,V]`, plus
`newByteBudget`, `tryAcquire`/`acquire`/`replace`, `Release`,
`newBoundedQueue`, nonblocking `TryPut`, `tryReserve`/`putReserved`, cancellable
`Put`/`Take`, entry `spaceWake`/`notEmptyWake`, budget-change wake, atomic
`newFixedMailbox`, mailbox `Replace`/`TakeDirty`/`Close`, producer-only queue
`Close`, and discard/drain operations.
`Stats() queueStats` reports entries, used bytes, entry/byte waiters, and stopped
state; a capacity-one `stateWake` signals state transitions for deterministic
tests and diagnostics without polling.

The exact helper surface is:

```go
type weighFunc[T any] func(T) (int64, error)

func newByteBudget(int64) (*byteBudget, error)
func (*byteBudget) tryAcquire(int64) (*permit, error)
func (*byteBudget) acquire(context.Context, int64) (*permit, error)
func (*byteBudget) replace(*permit, int64) (*permit, error)
func (*byteBudget) usedBytes() int64
func (*byteBudget) capacityBytes() int64
func (*permit) Release() error

func newBoundedQueue[T any](int, *byteBudget, weighFunc[T]) (*boundedQueue[T], error)
func (*boundedQueue[T]) TryPut(T) error
func (*boundedQueue[T]) tryReserve(T) (*permit, error)
func (*boundedQueue[T]) putReserved(T, *permit) error
func (*boundedQueue[T]) Put(context.Context, T) error
func (*boundedQueue[T]) Take(context.Context) (lease[T], error)
func (*boundedQueue[T]) TryTake() (lease[T], bool)
func (*boundedQueue[T]) Close()
func (*boundedQueue[T]) drainAndRelease() int
func (*boundedQueue[T]) Stats() queueStats
func (*boundedQueue[T]) spaceChanges() <-chan struct{}
func (*boundedQueue[T]) notEmptyChanges() <-chan struct{}
func (*boundedQueue[T]) budgetChanges() <-chan struct{}
func (*boundedQueue[T]) stateChanges() <-chan struct{}
func (lease[T]) Value() T
func (*lease[T]) Release() error

func newFixedMailbox[K comparable, V any]([]K, *byteBudget, weighFunc[V]) (*fixedMailbox[K, V], error)
func (*fixedMailbox[K, V]) Replace(K, V) error
func (*fixedMailbox[K, V]) TakeDirty() (K, lease[V], uint64, bool)
func (*fixedMailbox[K, V]) Close()
func (*fixedMailbox[K, V]) drainAndRelease() int
func (*fixedMailbox[K, V]) changes() <-chan struct{}
```

`Close` is idempotent and producer-only; `drainAndRelease` is idempotent and
may run only from the declared consumer/orchestrator cleanup after producers
have joined. `putReserved` takes permit ownership only on successful enqueue; on every error the caller
still owns and releases it. `TryTake` plus `lease.Release` is the only
single-item discard path, so there is no unnamed side buffer.

This increment also declares package-local sentinel errors for invalid weight,
queue full/stopped, unknown mailbox key, and `errPermitReleased`; the tests that
first use those errors do not depend on increment 6.

Tests cover entry and byte saturation independently, oversized single values,
cancellable acquisition, cancellation after permit acquisition but before
enqueue, release after consume/drop/replacement/drain, double-release
protection, fixed-key rejection, concurrent use, and stable accounting after
all paths. No test uses a sleep for ordering.

Validation:

```text
gofmt -w internal/service/*.go
go vet ./internal/service
go test ./internal/service
go test -race ./internal/service
```

Completion means entry slots and bytes must both be available, observed used
bytes never exceed the configured budget, and there is no overflow slice or
goroutine-per-operation path. Rollback removes only these six files.
Dependencies: increments 1–3.

### Increment 6 — priority coordinator, isolated history, and writer

Files:

- create `internal/service/service.go`;
- create `internal/service/coordinator.go`;
- create `internal/service/history.go`;
- create `internal/service/writer.go`;
- create `internal/service/testclock_test.go`;
- create `internal/service/coordinator_test.go`;
- create `internal/service/history_test.go`;
- create `internal/service/writer_test.go`;
- create `internal/service/shutdown_test.go`.

Types and functions:

- `Core`, `Options`, `QueueOptions`, `New`, `Run`, and `Updates`;
- unexported `runRealtimeIngress`, `runHistoryTransformer`,
  `runHistoryIngester`, `runCoordinator`, `runWriter`, `applyBoundedBatch`, and
  `runPublisher` loops/helpers;
- `systemClock`, exported `NewSystemClock() Clock`, and test-only
  `manualClock`, `manualTimer`, `newManualClock`,
  `Advance`, and `waitTimerRegistrations`;
- `CoreErrorKind` (`CoreBusy`, `CoreCancelled`, `CoreClosed`, `CoreMalformed`,
  `CoreDegraded`, `CoreInvariant`) and content-free `CoreError`.

Tests prove real-time precedence, propagation of category-specific shedding and
unknown-category degradation, at-most-four admitted fake jobs, exactly-once
job traversal and record walking,
writer live preference, one charged pending live write with an injected-clock
busy deadline, full-history-path isolation, all saturation behavior, root
cancellation, source closure, update-channel ownership, and joined goroutine
termination. The full-history isolation test fills
an entry-capacity-one test `historyWriteQ` while leaving valid byte accounting,
starts the ingester without the writer, and waits on queue `stateWake` until
`Stats.EntryWaiters == 1`. That proves the worker is blocked inside `Put`, not
merely before it. The test injects a real-time event into a concurrently running
coordinator, observes its update result, and only then dequeues/releases the
prefilled history operation.

Validation:

```text
gofmt -w internal/service/*.go
go vet ./internal/service
go test ./internal/service
go test -race ./internal/service
```

Completion means the coordinator never consumes `historyQ` or sends to
`historyWriteQ`, every goroutine has one owner and cancellation exit, and live
work is handled while history is saturated. Rollback removes the increment 6
files without changing the already tested primitives. Dependencies:
increments 1–5.

### Increment 7 — plain textual fake-data demonstration

Files:

- create `cmd/walite/main.go`;
- create `cmd/walite/demo.go`;
- create `cmd/walite/demo_test.go`.

Types and functions:

- unexported `demoScenario` and `gatedStore` types;
- `newDemoScenario(config.Values) (demoScenario, error)` and
  `runDemo(context.Context, io.Writer, demoScenario) error`;
- the seven `gatedStore` methods matching `service.MessageStore`, each delegating
  to `store.Memory` except the bounded first-history `ApplyBatch` gate;
- `main`, which supplies the signal context, defaults, scenario, and stdout;
- root signal context and standard-library wiring of defaults, fake source,
  memory store, policy, and service core.

The demo prints one ready line before releasing fake history, then finite lines
for a live message, background history progress, degradation if deliberately
saturated, a bounded retention summary, and clean shutdown. It never clears or
redraws the terminal, accepts no message text from the user, and terminates
after the synthetic scenario or signal cancellation.

Tests call `runDemo` with a buffer and barriers, asserting ready precedes
history completion, live output precedes the next history quantum, all data is
synthetic, output is finite, and cancellation joins all components.

Validation:

```text
gofmt -w cmd/walite/*.go
go vet ./cmd/walite ./internal/...
go test ./cmd/walite ./internal/...
go test -race ./cmd/walite ./internal/...
go run ./cmd/walite
```

`go run` is offline and uses only compiled synthetic data. Completion means it
demonstrates all five required observations without network or continuous
redraw. Rollback removes `cmd/walite/*.go`; the offline core remains testable.
Dependencies: increments 1–6.

### Increment 8 — cross-package tests, benchmarks, and milestone record

Files:

- create `internal/service/core_integration_test.go`;
- create `internal/model/normalize_benchmark_test.go`;
- create `internal/service/queue_benchmark_test.go`;
- create `internal/service/history_benchmark_test.go`;
- create `internal/syncpolicy/policy_benchmark_test.go`;
- create `internal/store/memory_benchmark_test.go`;
- create `docs/milestone-1a-results.md`;
- update `README.md` only to identify the offline demo and its non-network
  status.

This increment adds the 100,000-record integration path, allocation reporting,
whole-milestone race/leak repetitions, documented benchmark output from the
target-like machine, package-import review, and an acceptance checklist with
observed results. It does not add features.

Validation:

```text
gofmt -w cmd/walite/*.go internal/config/*.go internal/model/*.go internal/service/*.go internal/store/*.go internal/syncpolicy/*.go internal/wa/*.go
go vet ./...
go test ./...
go test -race ./...
go test -run '^$' -bench . -benchmem ./internal/model ./internal/service ./internal/syncpolicy ./internal/store
go list -deps ./...
go list -m all
git diff --check
```

Completion means all acceptance rows in section 14 have evidence, benchmark
allocations are recorded rather than converted into unsupported guarantees,
and `go list -m all` lists only the walite module. Rollback removes the new
benchmark/integration-result files and reverts only the identified README edit
without touching the working core.
Dependencies: increments 1–7.

## 4. Package dependency validation

The intended Milestone 1A graph is:

```text
model       -> Go standard library only
config      -> Go standard library only
wa          -> model (dependency-free fake only in 1A)
syncpolicy  -> model, config
service     -> model, syncpolicy
store       -> model
cmd/walite  -> config, model, service, store, syncpolicy, wa
```

These arrows are allowed dependency directions, not a requirement to add an
otherwise needless import. Because 1A injects the service-owned
`RetentionPolicy`, its actual service import can be the safe subset
`service -> model`; `cmd/walite` imports `syncpolicy` and supplies the policy.
If service later uses a pure syncpolicy helper, `service -> syncpolicy` remains
allowed. The reverse direction never is.

It must never contain:

```text
service    -> store
store      -> service
model      -> any internal package
syncpolicy -> service
any circular import
internal/common or internal/util
```

`EventSource`, `MessageStore`, `RetentionPolicy`, and `Clock` are declared by
`internal/service`, the consumer. Their method signatures contain only
standard-library and `internal/model` types. `wa.FakeSource`,
`store.Memory`, and `syncpolicy.Policy` declare matching methods without
importing service. Structural conformance is checked from a third package that
is allowed to see both sides:

```go
var _ service.EventSource = (*wa.FakeSource)(nil)
var _ service.MessageStore = (*store.Memory)(nil)
var _ service.RetentionPolicy = (*syncpolicy.Policy)(nil)
```

Those assignments belong in `internal/service/interfaces_external_test.go`
using package `service_test`, and equivalent assignments may appear in
`cmd/walite` wiring. They do not create an implementation-to-consumer import.
`go list -deps ./...` plus an explicit import review is the final dependency
check; no generic shared package is introduced.

## 5. Minimal model

The model is deliberately smaller than the future architecture model. Values
are immutable after construction: identifier/message structs have unexported
fields, strings are cloned only after their bounded prefix is known, and
slice-backed values copy bounded input and expose indexed access or defensive
copies. Queue admission recomputes or verifies the stored charge; it does not
trust a caller-supplied size.

### Types needed in 1A

| Type | Purpose now |
|---|---|
| `ChatID`, `MessageID` | comparable, constructor-validated storage identity |
| `Chat` | ID, bounded display name, truncation flag, and placeholder flag only |
| `MessageInput` | raw bounded-constructor input |
| `Message` | ID, chat ID, sent time, direction, normalized text, and truncation/body-retained flags |
| `Event` | one fake real-time message plus received time; the source channel establishes its real-time origin |
| `HistoryJobID`, `HistoryClass`, `HistoryJob` | immutable fake payload descriptor and application classes `Metadata`, `OnDemand`, `Bulk`, `Unknown`, required for lazy 2/1/1 admission |
| `HistoryChunk` | immutable, bounded indexed collection of at most 32 `Message` records and 128 KiB yielded by one job |
| `SourceStatus` | fixed-size degraded/drop counters from fake admission; no text/body |
| `SourceFailureKind` | content-free full/closed/rejected source classification shared structurally |
| `UpdateKind`, `Update` | eight finite observable ready/live/history/summary/degraded/stopping/stopped/fatal outputs for tests/demo |
| `Cursor` | deterministic `(sent time, message ID)` in-memory paging boundary |
| `MessageAnchor` | at most one bounded paging identity per retained chat |
| `WriteOrigin`, `WriteBatch` | immutable max-50 live/history message batch so writer/store and demo gate share only model values |
| retention values | explicit decision state, bounded snapshot, prune plan/result, and reasons |
| `CacheUsage` | deterministic estimated bytes plus chat/message/body counts |
| `MetadataLimitKind` | content-free interface-level code for the global chat limit |
| `ValidationCode`, `ValidationError` | content-free construction failures for bounded model values |

`HistoryClass` is an application test value, not a claim about WhatsApp
category importance. `HistoryJob` contains only a bounded job ID and class; the
concrete source retains cursor/payload state privately. `NewHistoryJob`,
`HistoryJob.ID`/`Class`, `NewHistoryChunk`, `HistoryChunk.Len`/`At`/`ByteSize`,
`NewWriteBatch`, `WriteBatch.Origin`/`Len`/`At`/`ByteSize`, `NewSourceStatus`,
and `NewUpdate` enforce construction and read-only access. The fake cursor
models lifetime and one-pass traversal, while actual protobuf references and
WhatsApp classification remain deferred to milestone 5.

The planned constructor/accessor surface is explicit:

```go
type MessageInput struct {
    ChatID, MessageID string
    SentAt            time.Time
    FromMe            bool
    Text              string
}

type ChatInput struct {
    ID          string
    DisplayName string
    Placeholder bool
}

func NewChatID(string) (ChatID, error)
func NewMessageID(string) (MessageID, error)
func NewMessage(MessageInput) (Message, error)
func NewChat(ChatInput) (Chat, error)
func NewEvent(Message, time.Time) (Event, error)
func NormalizeText(string) (text string, truncated bool)

func NewHistoryJob(id string, class HistoryClass) (HistoryJob, error)
func NewHistoryChunk([]Message) (HistoryChunk, error) // hard maxima only
func NewWriteBatch(WriteOrigin, []Message) (WriteBatch, error)
func NewMessageAnchor(ChatID, MessageID, time.Time, bool) (MessageAnchor, error)
func NewCursor(time.Time, MessageID) (Cursor, error)
func NoCursor() Cursor // explicit newest-page sentinel
func NewMessageSummary(Message) MessageSummary
func NewRetentionDecision(RetentionAction, RetentionReason) (RetentionDecision, error)
func NewSourceStatus(SourceStatusInput) SourceStatus
func NewUpdate(UpdateInput) (Update, error)
func NewCacheUsage(estimatedBytes int64, chats, messages, bodies, anchors int) (CacheUsage, error)
func NewPruneResult(deletedRows, removedBodies int, anchorReplaced bool) (PruneResult, error)

type UpdateInput struct {
    Kind                UpdateKind
    ChatID              ChatID
    MessageID           MessageID
    Accepted, Discarded uint64
    Degraded            bool
}

type SourceStatusInput struct {
    MetadataDropped, OnDemandDropped uint64
    BulkDropped, UnknownDropped      uint64
    Degraded                         bool
}

type RetentionState struct {
    Now         time.Time
    NewerBodies int
    Usage       CacheUsage
    Origin      WriteOrigin
}

func NewRetentionSnapshot(ChatID, []MessageSummary, *MessageAnchor) (RetentionSnapshot, error)
func NewPrunePlan(ChatID, []MessageID, *MessageAnchor) (PrunePlan, error) // IDs are rows to delete
```

Opaque ID/message/event values expose named scalar accessors such as `String`,
`ChatID`, `MessageID`, `SentAt`, `FromMe`, `Text`, `BodyTruncated`,
`BodyRetained`, and `ByteSize`. Collection values expose only `Len` and `At`;
constructors defensively copy and bound their inputs. `Update` never contains a
message body. `Event` exposes `Message`, `ReceivedAt`, and `ByteSize`; `Chat`
exposes `ID`, `DisplayName`, `NameTruncated`, and
`Placeholder`; `MessageAnchor` exposes chat/message IDs, time, and direction.
`MessageSummary` contains identity, time, direction, retained/body byte
count, and no text. `NoCursor` is the only no-boundary value accepted by
`MessagesBefore`; a constructed cursor exposes `IsZero`, `SentAt`, and
`MessageID`. `MessageSummary`
exposes its IDs, time, direction, body-retained flag, and body bytes; `RetentionDecision`
exposes `Action`/`Reason`; `CacheUsage` exposes its five nonnegative counters;
`PrunePlan` exposes `DeleteLen`/`DeleteAt`/`ReplacementAnchor`; and
`PruneResult` exposes deleted-row/removed-body counts plus anchor replacement.
Snapshot/prune accessors return indexed values or bounded defensive copies.
`RetentionAction` is the fixed enum `KeepBody`, `KeepMetadata`, or `Discard`.
`RetentionReason`, `WriteOrigin`, and `SourceFailureKind` are likewise closed,
content-free enums;
constructors reject unknown enum values. `MetadataLimitKind` has the single 1A
value `MetadataChatLimit`; later metadata classes require an explicit model
addition rather than an arbitrary string.

### Types deliberately deferred

- `AccountState`, connection/reconnect state, contacts, presence, and groups;
- the complete future `WhatsAppClient` and protocol error taxonomy;
- QR frames, pairing tokens, authentication and session values;
- `DownloadRef`, attachments, media metadata, paths, and byte streams;
- commands, `Submit`, sending, outgoing acknowledgements, and compose state;
- edits, deletes, reactions, receipts, tombstones, and mutation versions;
- remote `HistoryRequest`, WhatsApp JIDs/protobuf enums, and opaque payloads;
- final TUI snapshots/layout values and SQLite-specific plans.

The textual demo is self-driving, so it does not justify a `Command` or
`Submit` type in 1A.

### Hard normalized-value limits

| Value | Hard limit | Behavior |
|---|---:|---|
| chat ID | 512 bytes, nonempty | reject |
| message ID | 512 bytes, nonempty | reject |
| fake history job ID | 128 bytes, nonempty | reject |
| chat display name | 1 KiB UTF-8 | bounded truncate and mark truncated |
| retained text | 16 KiB UTF-8 | bounded truncate at rune boundary; set `BodyTruncated` |
| normalized event | 32 KiB including a conservative 256-byte envelope charge | reject if still too large |
| observable update | 32 KiB including fixed accounting | omit optional detail or reject before mailbox publication |
| history chunk | 32 records and 128 KiB | constructor rejects excess; producer splits first |
| write batch | 50 messages | constructor rejects excess; writer uses fixed arrays |
| retention snapshot | 551 summaries plus one anchor | constructor rejects excess |
| prune plan | 551 message-row IDs plus one anchor | constructor rejects excess |

`NormalizeText` scans at most the prefix needed to emit 16 KiB. It does not
copy or range over the rest of a multi-megabyte input. Invalid UTF-8 in that
bounded prefix is replaced with U+FFFD through a bounded builder; it stops
before the next rune would exceed the output limit. Valid prefixes are cloned
so the normalized value owns only bounded storage. IDs are required to be
valid UTF-8 and free of control/NUL bytes; oversized or malformed IDs fail
rather than being silently changed.

The size charge is a conservative, documented formula, not JSON/gob output and
not a claim about exact Go heap layout:

```text
event charge = 256 bytes
             + chat-ID bytes
             + message-ID bytes
             + retained-text bytes
```

The constructor checks integer addition before comparing with 32 KiB, even
though the smaller 1A field limits make the current legal maximum lower. The
architecture's 8 KiB aggregate optional-metadata limit is enforced only when
those deliberately deferred metadata types are introduced; 1A does not add an
unused arbitrary metadata field merely to reach the event ceiling.
`HistoryChunk` adds each message's corresponding normalized charge plus a fixed
32-byte per-record index charge and rejects overflow. Queue envelopes add their
own accounting charge outside these values.

Constructors return `*model.ValidationError` with a stable code (`Required`,
`InvalidUTF8`, `ControlCharacter`, `TooLong`, or `SizeExceeded`), field name,
and byte limit. Tests use `errors.As`; rendered errors never include rejected
content. No exported mutable byte slice exists in the 1A model.

## 6. Configuration validation

Milestone 1A uses Go values only. There is no TOML parser, environment lookup,
XDG path resolution, reload, or filesystem mutation.

```go
type QueueLimit struct {
    Entries int
    Bytes   int64
}

type Retention struct {
    MessagesPerChat    int
    MaxAge             time.Duration
    CacheBytes         int64
    MaxChats            int
}

type Queues struct {
    Realtime, History, Commands QueueLimit
    LiveWrites, HistoryWrites   QueueLimit
    ViewUpdates                 QueueLimit
    LiveWriteBusy               time.Duration
}

type History struct {
    ChunkRecords int
    ChunkBytes   int64
}

type Batch struct {
    MaxOperations int
    MaxWait       time.Duration
}

type Values struct {
    Retention Retention
    Queues    Queues
    History   History
    Batch     Batch
}
```

All sizes are already bytes; no parser-facing KiB/MiB conversion helper is
exported. Defaults use checked integer constants (`1 << 10`, `1 << 20`). A
future parser must convert units before constructing these values.

```text
Values
├── Retention: MessagesPerChat, MaxAge, CacheBytes,
│              MaxChats
├── Queues: Realtime, History, Commands, LiveWrites,
│           HistoryWrites, ViewUpdates (each Entries + Bytes),
│           LiveWriteBusy
├── History: ChunkRecords, ChunkBytes
└── Batch: MaxOperations, MaxWait
```

Defaults and safe ranges:

| Field | Default | Accepted range/rule |
|---|---:|---|
| retention messages/chat | 100 | 20–500 |
| retention age | 90 days | 1–365 days |
| cache design bytes | 250 MiB | 32–2048 MiB, overflow-safe |
| global chats, including placeholders | 10,000 | 100–10,000 |
| realtime entries/bytes | 128 / 2 MiB | 32–512 / 512 KiB–4 MiB |
| history entries/bytes | 4 / 512 KiB | 1–16 / 128 KiB–2 MiB |
| command entries/bytes | 32 / 512 KiB | 1–64 / 64 KiB–1 MiB |
| live-write entries/bytes | 128 / 1 MiB | 16–256 / 256 KiB–2 MiB |
| history-write entries/bytes | 256 / 2 MiB | 16–512 / 512 KiB–4 MiB |
| view entries/bytes | 64 / 512 KiB | exactly 64 / 128 KiB–1 MiB |
| live-write busy deadline | 25 ms | 5–100 ms |
| history chunk records/bytes | 32 / 128 KiB | 1–32 / 64–128 KiB |
| batch operations/wait | 50 / 25 ms | 1–50 / 5–100 ms in 1A |

The six default queue byte budgets total 6.5 MiB. `Validate` also requires the
configured total to remain at most 6.5 MiB, preserving approximately 1.5 MiB
of the architecture's 8 MiB target for fixed status mailboxes, queue
envelopes, one bounded ingress value per worker, the bounded offline fake
script/cursors, and accounting overhead.
Server-provided HistorySync payloads do not exist in 1A and remain excluded in
the future architecture.

Additional cross-field rules are:

- every entry and byte capacity is positive; zero never means unlimited;
- view entry capacity is exactly 64 because it is a fixed-key mailbox, not a
  dynamically sized channel;
- a queue byte budget must admit at least one maximum value for that lane;
- `History.ChunkBytes <= Queues.History.Bytes`;
- `Retention.MessagesPerChat + Batch.MaxOperations + 1` is checked for
  overflow and cannot exceed the hard 551-summary model bound;
- the 64 KiB minimum history chunk admits one maximum normalized message plus
  record accounting; a cross-package test keeps config/model constants aligned;
- aggregate queue-byte addition is overflow-checked before comparison;
- batch limits cannot exceed corresponding queue entries;
- the 1A batch maximum is deliberately narrower than the architecture's future
  configurable safe range; widening above 50 requires a later measured design;
- defaults are returned by value and validation does not mutate them.

`config.ValidationError` contains `Field`, `Rule`, `Value`, `Min`, and `Max`
numeric metadata. Rules include `Required`, `OutOfRange`, `Overflow`, and
`AggregateExceeded`. It contains no model value, keeping config independent.
Filesystem and TOML errors are deferred.

## 7. Interfaces and fakes

The service contracts are intentionally narrower than the architecture's
future interfaces. The following method sets are the planned 1A contracts;
widening or moving one requires review at its increment boundary.

```go
type EventSource interface {
    Run(context.Context) error
    RealtimeEvents() <-chan model.Event
    MetadataHistoryJobs() <-chan model.HistoryJob
    OnDemandHistoryJobs() <-chan model.HistoryJob
    BulkHistoryJobs() <-chan model.HistoryJob
    StatusWake() <-chan struct{}
    Status() model.SourceStatus
    NextHistory(context.Context, model.HistoryJob, int, int64) (model.HistoryChunk, bool, error)
    ReleaseHistory(model.HistoryJob)
}

type MessageStore interface {
    UpsertChats(context.Context, []model.Chat) error
    Chat(context.Context, model.ChatID) (model.Chat, bool, error)
    ApplyBatch(context.Context, model.WriteBatch) error
    MessagesBefore(context.Context, model.ChatID, model.Cursor, int) ([]model.Message, error)
    RetentionSnapshot(context.Context, model.ChatID, int) (model.RetentionSnapshot, error)
    ApplyPrune(context.Context, model.PrunePlan) (model.PruneResult, error)
    CacheUsage(context.Context) (model.CacheUsage, error)
}

type RetentionPolicy interface {
    Decide(model.Message, model.RetentionState) (model.RetentionDecision, error)
    PlanPrune(model.RetentionSnapshot, model.RetentionState) (model.PrunePlan, error)
}

type Clock interface {
    Now() time.Time
    NewTimer(time.Duration) Timer
}

type Timer interface {
    C() <-chan time.Time
    Stop() bool
}
```

Service construction is exact and does not import config:

```go
type QueueOptions struct {
    Entries int
    Bytes   int64
}

type Options struct {
    Realtime, History         QueueOptions
    LiveWrites, HistoryWrites QueueOptions
    ViewUpdates               QueueOptions
    HistoryChunkRecords       int
    HistoryChunkBytes         int64
    BatchMaxOperations        int
    RetentionSnapshotLimit    int
    BatchWait                 time.Duration
    LiveWriteBusy             time.Duration
    ShutdownGrace             time.Duration
}

func New(Options, EventSource, MessageStore, RetentionPolicy, Clock) (*Core, error)
func NewSystemClock() Clock
func (*Core) Run(context.Context) error
func (*Core) Updates() <-chan model.Update
```

`CoreError` has exact fields `Kind CoreErrorKind`, `Operation CoreOperation`,
and wrapped `Cause error`; `CoreOperation` is a fixed enum, not an arbitrary
content string. `Error`/`Unwrap` expose no message body or identifier. Queue
primitive sentinels remain service-local. The wa-owned `SourceError` is mapped
structurally without a concrete-package import.

For store metadata exhaustion, service uses `errors.As` against its own
unexported structural classifier
`interface { MetadataLimitKind() model.MetadataLimitKind }`. Concrete
`store.Error` implements that method without importing service. Context errors
remain discoverable with `errors.Is`; all other store errors map to a redacted
fixed `CoreOperation`/`CoreErrorKind`. No store-owned sentinel appears in a
service signature or switch.

Source errors use the parallel service-local classifier
`interface { SourceFailureKind() model.SourceFailureKind }`, implemented by
`wa.SourceError`. Service maps `SourceFull`/`SourceRejected` to degradation and
`SourceClosed` to normal closure only when source outputs are actually closed;
it never imports `internal/wa` or switches on a wa-owned type.

`DefaultShutdownGrace` is a fixed 1A value of two seconds; `New` accepts
100 ms–5 s so tests can use an injected-clock value without waiting. The
executable maps each applicable validated queue/history/batch field into
`Options`, sets
`ShutdownGrace` to that constant, and passes `config.Retention` to
`syncpolicy.New`. Its store mapping is
`MaxChats = Retention.MaxChats`, `MaxQueryMessages = 200`,
`MaxRetentionSummaries = Retention.MessagesPerChat + Batch.MaxOperations + 1`,
and `MaxBatchOperations = Batch.MaxOperations`; the same computed summary bound
is passed as `Options.RetentionSnapshotLimit`. The command budget is validated
but not instantiated in 1A, and cmd passes `service.NewSystemClock()`. This
wiring in `cmd/walite` prevents service/store
from importing config. `New` and `NewMemory` revalidate nonzero local invariants
because callers other than cmd may construct options directly; service requires
`BatchMaxOperations + 1 <= RetentionSnapshotLimit <= 551`, and the integration
test proves cmd's computed limit matches the memory-store option.

`Core.Run(ctx)` owns the offline component lifetime and `Core.Updates()` returns
a receive-only bounded update stream. `Core` is single-use: a concurrent `Run`
returns `CoreBusy`, a call after completion returns `CoreClosed`, and `Updates`
always returns the same channel. A full future `ApplicationService`,
`WhatsAppClient`, `View`, logger, command submission, media, and query surface
are deferred.

The concrete fake source has the fixed outputs described in increment 3.
`Run` owns its configured finite script, runs it in that one component
goroutine, and closes all output channels exactly once on
completion/cancellation. A `HistorySpec` contains one validated `HistoryJob`, finite synthetic count, chat
ID, start time, and bounded text prefix; `NextHistory` lazily generates at most
the caller's configured record/byte limits and never prebuilds the records.
`ReleaseHistory` is idempotent and frees the cursor plus category token exactly
once. `TryRealtime` and `TryHistory` never block. The source never starts a
goroutine per event. Service starts, cancels, and joins `EventSource.Run`;
tests/demo configure gate-controlled `ScriptStep` values.

Output-channel closure and cursor lifetime are intentionally separate. `Run`
sets `admissionClosed` under the same mutex used by injection, then closes the
real-time, three category, and status-wake channels. New admissions fail with
`SourceClosed`, but `NextHistory` still accepts a previously admitted job until
its traversal context is cancelled or that job is released. `ReleaseHistory`
does not consult `admissionClosed`; it always removes a matching live cursor
and returns its category token, even after `Run` has returned. Cursor state is
gone only when all delivered jobs have been released.
`FakeSource` is single-use: a second or concurrent `Run` returns
`SourceRejected` and cannot close an output twice.

Adding `Run`, cursor, status, and release methods is a deliberate 1A lifecycle
refinement of the architecture's illustrative channel-only `EventSource`. It
keeps source goroutine and retained-job ownership explicit without importing
service. Likewise, the timer-channel `Clock`/`Timer` refinement replaces the
architecture's illustrative blocking `Wait`: multiplexing queue space,
control, results, cancellation, and deterministic deadlines requires a
selectable timer channel and must not create a timer goroutine per wait.

The fake construction surface is:

```go
func NewHistorySpec(job model.HistoryJob, count int, chat model.ChatID,
    start time.Time, textPrefix string) (HistorySpec, error)
func NewRealtimeStep(event model.Event, gate <-chan struct{}) ScriptStep
func NewHistoryStep(spec HistorySpec, gate <-chan struct{}) ScriptStep
func NewBarrierStep(gate <-chan struct{}) ScriptStep
func NewFakeSource(script []ScriptStep) (*FakeSource, error) // copies, max 256 and 512 KiB
func (*FakeSource) TryRealtime(model.Event) error
func (*FakeSource) TryHistory(HistorySpec) error
```

`HistorySpec`/`ScriptStep` fields are unexported and immutable after their
constructors. A metadata job must have count zero and an empty text prefix and
therefore cannot disguise a message-heavy generator; on-demand/bulk/unknown fake jobs allow
0–1,000,000 records. Job/chat/time must be valid, and text prefix is
normalized/bounded before storage. A gate is receive-only, may be nil for
immediate execution, and is waited through `select { gate, ctx.Done() }`.
`NewFakeSource` recomputes a conservative charge and rejects a script over 256
steps or 512 KiB; `Run` clears its private copy of each processed step so old
event/prefix references do not remain live for the whole run.
`NewBarrierStep` emits nothing and keeps a scripted source alive until its gate
or source context completes, which permits deterministic external injection.
Synthetic message IDs derive from bounded job ID plus decimal index without
personal data. `NextHistory` advances one private index and honors the smaller
of caller-configured and hard chunk limits. It may return `done=true` with the
final nonempty chunk; transformer enqueues that chunk before its single release.
An empty `done=true` is valid for a metadata-only/zero-record job.

Job IDs are unique for one `FakeSource` lifetime. The source stores admitted
IDs in a preallocated fixed 256-element array, rejects a duplicate before
creating cursor state, and admits at most 256 history jobs over its lifetime;
this prevents a stale `ReleaseHistory` call from freeing a newer job while
keeping identity tracking finite. Category-full rejections do not consume an
ID, so the same application import may be retried after a token is released.
Reaching the lifetime bound rejects/degrades further fake history admissions.

Source status is a fixed mutex-protected cumulative slot, not a replaceable
ordinary channel element. Admission updates the slot and nonblockingly signals
capacity-one `StatusWake`; service snapshots it through `Status()`. A closed
wake is nilled after observation. Its four category counters saturate at
`math.MaxUint64` rather than wrapping. This preserves degradation even when
several drops coalesce.

The in-memory store and retention policy use matching method sets but import no
service package. The external interface test and `cmd/walite` are the only
places that import both contract and implementation packages.

Channel ownership and shutdown:

| Channel | Owner/closer | Consumer behavior |
|---|---|---|
| fake real-time/three category-job outputs and status wake | fake source `Run` | service receives/snapshots; never closes; transformer releases each job through source method |
| internal real-time queue | real-time ingress worker | coordinator selects context and queue |
| history queue | history transformer | history ingester drains/release |
| live-write queue | coordinator | writer drains bounded live tail |
| history-write queue | history ingester | writer discards pending history on shutdown |
| store-result queue | writer | coordinator drains until writer ends |
| update stream | service core | demo/tests only receive; service closes last |

Cancelling/completing a concrete fake run is idempotent. Admission after its
closed state is published returns the fake package's typed `SourceClosed`;
already-admitted cursor traversal/release follows the separate lifetime above.
Cancellation returns the caller's `context.Cause` where available. No receiver
closes a channel it did not create.

## 8. Bounded byte-budget mechanism

The queue mechanism is standard-library-only and enforces two independent
limits: a buffered channel enforces entry count and `byteBudget` enforces the
sum of charges held by queued/in-flight leases.

| Lane | Entry bound | Variable-byte budget |
|---|---:|---:|
| `realtimeQ` | 128 | 2 MiB |
| fake-source job admission | 2 metadata + 1 on-demand + 1 bulk, queued plus active | each fake descriptor/cursor state <=18 KiB; charged to reserve, not a configured queue |
| `historyQ` | 4 chunks | 512 KiB |
| `commandQ` | 32 | 512 KiB reserved in config; queue not instantiated because commands are deferred |
| `liveWriteQ` | 128 | 1 MiB |
| `historyWriteQ` | 256 | 2 MiB |
| view mailbox | 64 fixed keys | 512 KiB |

These configured lane budgets total 6.5 MiB. Fixed control paths use the
remaining approximately 1.5 MiB reserve: eight fixed status keys, an
eight-entry fixed-size `resultQ`, capacity-one wake/error/update-output
channels, the fake source's one-slot real-time output/status wake and four-job
descriptor capacity across three category outputs, fixed batch arrays,
the fake's hard-512-KiB script copy, and conservative envelope/allocation
overhead. Result/status/error values have
no arbitrary strings or slices. A source-held real-time event is at most
32 KiB, the transformer's sole not-yet-admitted history chunk is at most
128 KiB, and the capacity-one public update output holds at most one 32 KiB
normalized update; those possible copies are explicitly part of this reserve
rather than the 6.5 MiB lane total. At most four source-owned fake cursor/job
states of at most 18 KiB each are also part of the reserve; their generator
state never contains the generated record collection. Every 1A queue, mailbox,
wake, result, error, source channel, script, and cursor therefore has an
explicit finite bound. The future server-provided decoded HistorySync payloads
remain excluded from the architecture's 8 MiB application-allocation target.

### Permit design

`byteBudget` contains a mutex, `capacity`, `used`, and a capacity-one
notification channel. It provides:

- nonblocking `tryAcquire(weight)`;
- cancellable `acquire(ctx, weight)` that waits on the current notification
  channel and rechecks under the mutex;
- `replace(oldPermit, newWeight)` for atomic fixed-mailbox replacement;
- read-only `usedBytes` and `capacityBytes` test/diagnostic accessors.

A `permit` is a pointer to state containing its budget, exact weight, and an
atomic released flag. `Release` uses compare-and-swap; a second release returns
`errPermitReleased` and never subtracts again. Release subtracts under the
budget mutex and nonblockingly signals the reusable wake channel. Waiters treat
the token only as a hint and recheck under the mutex, so a release allocates no
new channel. Each lane permits at most one registered blocking budget waiter;
only its fixed owner worker can use `acquire`. Coordinator and mailbox paths use
nonblocking reservation plus wake selection. A second blocking waiter is an
internal-invariant error rather than an implicit waiter queue.

Weights must be positive and no larger than the budget. Addition and
subtraction are overflow/underflow checked. Permits cannot be constructed by
callers.

### Queue design

`boundedQueue[T]` stores only `envelope[T]`, containing one immutable value and
one permit. It never contains an overflow slice. Admission is:

1. compute/verify the conservative charge;
2. acquire a byte permit;
3. try or wait for an entry slot;
4. if entry admission fails, cancellation wins, or the producer has stopped,
   release the permit before returning;
5. on success, transfer sole permit ownership to the envelope.

Each queue also has capacity-one `spaceWake` and `notEmptyWake` channels.
Dequeuing signals entry space immediately even though the in-flight lease still
owns its byte permit; releasing the lease separately signals budget change.
Enqueue signals not-empty. Wake tokens are hints, so every waiter rechecks
entry, byte, stopped, and context state rather than treating a token as a
reservation.

`Take` returns a `lease[T]`; the consumer releases it after processing, not
merely after receiving. The transformer acquires the history-queue permit for
its one locally held bounded chunk before enqueue. Creating a write operation
in a different lane acquires that lane's permit before releasing the source
chunk lease, conservatively charging the brief overlap.

Only the declared producer closes a queue, and only after all calls that may
send have joined. A closed queue is drained explicitly: processed items release
after consume and discarded items release immediately. This ordering avoids a
send/close race and makes permit accounting testable.

Blocking `Put` is available only to the fixed transformer/history workers. It
is not exposed to the coordinator, callbacks, or arbitrary concurrent callers;
otherwise blocked caller stacks would become an unbounded hidden queue.

The coordinator does not call blocking `Put`. It retains the source event
lease, uses `tryReserve` for destination bytes and `putReserved` for the entry,
and releases a failed reservation before selecting on `spaceWake`, budget
change, control/result wakes, cancellation, or its busy timer. Thus a full byte
budget or entry lane creates exactly one pending value that remains charged by
its real-time source lease until a destination reservation succeeds, not a
blocked producer collection.

### Fixed mailbox replacement

`fixedMailbox[K,V]` receives a finite key list at construction and allocates no
new keys later. Under the mailbox lock, replacement asks the budget to atomically
change `used` from `used-oldWeight` to `used-oldWeight+newWeight`. If the new
value does not fit, the old value remains. On success the old permit is marked
released, a new permit is installed, the slot generation increments, and a
capacity-one wake channel is signaled nonblockingly. This is an actual keyed
slot, not an impossible attempt to replace an element already inside an
ordinary channel.

The 1A view mailbox allocates exactly 64 slots. Eight fixed keys map one-for-one
to `UpdateReady`, `UpdateLive`, `UpdateHistory`, `UpdateSummary`,
`UpdateDegraded`, `UpdateStopping`, `UpdateStopped`, and `UpdateFatal`; the
remaining 56 slots are reserved and cannot be selected by 1A callers. This
keeps the architecture's capacity/budget stable without inventing dynamic chat
keys. A future milestone must assign a reserved key explicitly or revise the
architecture; it cannot grow the key map at runtime.

The update publisher owns at most one mailbox lease as `pendingUpdate`. It
selects between sending that immutable value to the capacity-one public output,
mailbox wake, and `publisherCtx`; it never dequeues a second pending value. A
full public output therefore holds one max-32-KiB copy from the 1.5 MiB reserve,
the publisher holds one value still charged to the 512 KiB view budget, and
newer keyed generations remain/coalesce in fixed mailbox slots. On successful
send, the publisher releases the mailbox lease and synchronously rescans dirty
slot generations before waiting again. That rescan is required because a
mailbox wake for a newer generation may have been consumed while the prior
`pendingUpdate` was blocked; no update may depend on a second wake arriving.
On normal mailbox close or cancellation it attempts at most one final
nonblocking output send and releases that mailbox lease whether the send
succeeds or the output is full; a successful output copy is covered by the
fixed reserve. It then drains/releases all remaining fixed slots and closes the
output immediately.
A closed output may still yield its one already buffered value normally. This
defines full and shutdown behavior without replacing an ordinary channel
element or depending on a receiver.

The design has no goroutine per admission, timeout, event, replacement, or
release. Deterministic tests inspect entry depth, used bytes, generations, and
permit states directly from the same package. A saturation test fills the
public output, replaces the same mailbox key twice, lets the publisher consume
the single wake, receives the blocked pending value, and proves the publisher's
synchronous rescan observes the newest generation without another write.

## 9. Priority and isolation

Milestone 1A selects the architecture's separate bounded history-ingestion
worker. The coordinator handles real-time, control/status, store results, and
observable updates. It never consumes `historyQ`, never sends to
`historyWriteQ`, and cannot be blocked by a full history writer lane.

`Core.Run` creates every queue before launching workers and waits for one
capacity-one started signal from each fixed component. The publisher begins
scanning; every event-producing/processing worker signals and then waits on one
shared close-only `startGate`. After all signals arrive, Core puts
`UpdateReady` in its fixed mailbox. Ready uses a fixed atomic startup state
`Waiting -> Committed` or `Waiting -> Aborted`: the publisher must win the
commit transition before its guaranteed nonblocking first send to the empty
capacity-one public output, then it closes a one-shot `readyPublished`
acknowledgement. Before this transition the publisher suppresses/releases every
non-Ready key, so Ready is necessarily the first possible send. Core closes
`startGate` exactly once after that ack. Thus no
live/history update can overtake ready, startup never waits for an external
receiver, and a component failing before its signal/ack cancels startup without
a false ready state. Root cancellation that wins `Waiting -> Aborted` suppresses
and releases Ready. If publication has already committed, Core waits for the
nonblocking send/ack, opens the gate, and immediately enters ordered shutdown;
Ready may then correctly precede Stopping. The source-owner worker is the
deliberate exception to simply leaving an aborted pre-gate wait: it invokes
`EventSource.Run` exactly once with the already cancelled `sourceCtx` and joins
it, allowing the source owner to close all of its outputs. Ingress/transformer
can then observe closure; Core never closes source-owned channels itself.

The test-only manual clock is mutex-protected. `NewTimer` records an absolute
deadline, allocates one capacity-one timer channel, increments a monotonic
registration count, and signals a reusable capacity-one registration wake.
It installs the timer in one of eight fixed active slots; firing or `Stop`
clears that slot, and exhausting the slots or overflowing the registration
counter is an internal test invariant panic rather than unbounded growth.
`waitTimerRegistrations(ctx, n)` rechecks that count under the mutex and is the
barrier every busy, batch, and shutdown-grace test uses before `Advance`.
`Advance` moves time forward under the mutex, marks all newly due one-shot
timers fired, and synchronously makes one nonblocking send to each due timer's
capacity-one channel; it starts no goroutine. `Stop` is mutex-protected and
idempotent, returning true only for an active timer. This prevents a test from
advancing before the component has registered the timer it intends to observe.

### Offline pipeline

```text
fake realtime -> nonblocking ingress -> realtimeQ -> coordinator -> liveWriteQ
                                                              |
                                                              +-> update mailbox

fake history spec -> independent source admission (metadata 2 / on-demand 1 / bulk 1)
                  -> transformer chooses metadata, then on-demand, then bulk
                     at every bounded chunk boundary and calls NextHistory
                  -> historyQ -> history ingester -> historyWriteQ

liveWriteQ -----------+
                       +-> live-first writer -> MessageStore -> resultQ
historyWriteQ --------+
```

The fake source admits lazy jobs into fixed metadata (2), on-demand (1), and
bulk (1) slots. Unknown classes are shed and mark synchronization degraded. A
full class slot sheds the new fake application import; it never evicts an
in-process job. Queued plus transformer-active jobs never exceed 2/1/1 or four
total. Metadata specs contain no message records. At every chunk boundary, the
transformer drains ready metadata descriptors, then prefers an active/ready
on-demand job, then advances an active/ready bulk job. It holds at most the
fixed 2/1/1 descriptors and never copies a complete job. This is an application
scheduling policy, not a WhatsApp importance or delivery-order claim.

For the selected descriptor the transformer calls `NextHistory` for one
configured quantum (never above hard 32 records/128 KiB), puts a returned chunk
into the 512 KiB `historyQ`, then rechecks higher-priority category outputs. It
calls `ReleaseHistory` exactly once after end, error, or cancellation; until
then the source retains the category token and private cursor. The history
ingester separately owns each chunk lease, applies retention, and may block
cancellably while putting a bounded write into `historyWriteQ`. It releases the
chunk after downstream accounting succeeds or the record is discarded. No
coordinator resource is held while it blocks.

The transformer stores `nil` for each closed category receive channel and exits
only after all three are closed and every fixed active descriptor is finished,
or after cancellation has released active and buffered descriptors. Closed
category channels therefore cannot form a busy loop.

### Coordinator pseudocode

```text
loop:
    if root cancelled: enter ordered shutdown

    handle every ready control/status slot

    handledLive = 0
    while handledLive < 32 and realtimeQ is immediately ready:
        take leased event
        validate event and call retention policy
        if retained:
            form exactly one pending live-write value, still charged only by event lease
            try liveWriteQ immediately
            if full:
                start one injected-clock busy timer
                while that single operation is pending, select among:
                    cancellation
                    control/status wake
                    store result
                    liveWriteQ space wake
                    busy timer
                on space: retry; transfer a newly acquired destination permit on success
                on timer: mark the value dropped and publish cache-degraded status
                do not consume another realtime event while one is pending
        publish bounded/coalesced live update
        release event lease exactly once on the common success/drop/cancel path
        handledLive++

    select cancellably among:
        one realtime event
        one store result
        one control/status wake
        source completion

    history data is never a case in this loop
```

The fairness cap prevents control/results starvation. Live-write admission has
one cancellable pending operation charged by its still-held real-time lease and
a configured/injected-clock busy deadline; there is no pending slice or held
destination permit between retries. While waiting, the coordinator keeps
servicing cancellation, fixed control/status slots, and store results. It does
not consume more real-time events, so `realtimeQ` remains the only backlog. A
deadline degrades/drops the pending value; the helper's common cleanup releases
the real-time lease exactly once.

After any source/result channel closes, its loop stores `nil` in the local
receive-channel variable so a permanently ready closed channel cannot create a
busy loop. Termination depends on the explicit set of nil/closed inputs, not a
polling default case.

### History-ingestion pseudocode

```text
loop:
    take one leased bounded chunk from historyQ or stop on cancellation
    read one bounded CacheUsage snapshot for this chunk
    for each record, once, with a cancellation check between records:
        ask pure policy for early age/cache eligibility; propagate typed validation error
        if KeepBody or KeepMetadata:
            if not KeepBody: transform immutably to bodyless metadata
            acquire historyWriteQ entry+bytes with context
            transfer bounded immutable message to the write operation
            # this wait may block only this worker
    release the chunk lease exactly once
```

Cancellation while waiting for `historyWriteQ` releases any newly acquired
write permit and the current chunk lease. Remaining category/history leases
are discarded and released by their owners.

### Writer pseudocode

```text
pendingHistory = none
loop:
    if a live write is immediately ready:
        gather at most Options.BatchMaxOperations (never above 50)
        or until Options.BatchWait fake-clock deadline
        call applyBoundedBatch with origin Live
        release leases and publish one bounded result
        continue

    if pendingHistory exists:
        recheck live once more
        if live became ready: process live and retain only this one pending history lease
        otherwise process at most min(25, Options.BatchMaxOperations)
        history operations through applyBoundedBatch with origin History, release, publish result
        continue

    select cancellably for live write, history write, or shutdown
    if history selected: hold it as the sole pending history operation and loop
```

The second live check removes Go `select` randomness from the priority
guarantee. The only hidden pending state is one charged history lease and two
fixed arrays with hard capacities 50 live/25 history; configured batch values
select prefixes of those arrays. They are never append-only slices. History can
be delayed or discarded indefinitely under live load.

Every writer store call uses one `applyBoundedBatch` sequence; retention is not
merely described and forgotten:

```text
validate WriteBatch and collect affected chat IDs in a fixed max-50 array
for each candidate message:
    request a body-free RetentionSnapshot capped at Options.RetentionSnapshotLimit
    derive NewerBodies and current CacheUsage
    call Policy.Decide with injected Clock.Now; abort batch on validation error
    retain body, convert to bodyless metadata, or omit as directed
store.ApplyBatch(filtered immutable batch) # bodyless entries update one anchor/chat, not message rows
for each affected chat, rechecking live between chats:
    request the same bounded post-write summary
    plan = Policy.PlanPrune(summary, current state); abort on validation error
    store.ApplyPrune(plan)  # deletes excess/old message rows and replaces one anchor
publish one fixed-size result including retained/discarded counts
```

The pre-write decision avoids unnecessary history bodies; the serialized
post-write snapshot/plan is authoritative for newest-count and age rules when
live/history races. `RetentionDecision.KeepMetadata` becomes a bodyless anchor
candidate, not a message row or skipped record, so the store can replace the
one-per-chat anchor. The
snapshot contains summaries only, and each call/plan is capped; no writer step
loads message bodies or an unbounded chat. If cache usage reaches the design
limit, new optional history bodies are rejected first. Actual filesystem-size
enforcement remains deferred.

### Exact shutdown order

Workers do not all receive the root context directly, because that would make
an ordered drain impossible. `Core.Run` preserves context values with
`context.WithoutCancel(root)` and derives separately cancellable source,
real-time-ingress, history, history-store-call, coordinator, writer, and
publisher contexts. `sourceCtx` belongs only to `EventSource.Run`; the ingress
worker lives until the source real-time output closes during an ordinary stop.
Every history-origin store operation in `applyBoundedBatch` receives a child of
`historyStoreCtx`, while live store calls receive `writerCtx`. One orchestrator
watches the root and an injected-clock grace timer. Fatal component failure
cancels all component contexts immediately. On a nil `EventSource.Run`
completion, ingress drains the closed real-time output and the transformer
finishes already admitted cursors through the closed category outputs; the
bounded downstream pipeline closes naturally and `Core.Run` returns nil. A
non-cancellation source error is fatal. Ordinary root cancellation follows this
order:

1. Mark the fixed status mailbox `Stopping` and stop new demonstration/control
   intake. There is no 1A command API.
2. Cancel `sourceCtx`, join the single `EventSource.Run` component, and let that
   producer alone close its real-time/job/status outputs. Private cursor state
   for already delivered jobs remains only until transformer release.
3. The separately lived real-time ingress consumes the bounded final source
   value, observes the closed output, closes `realtimeQ`, and exits; it was not
   cancelled with `sourceCtx`. In parallel, cancel `historyCtx` and
   `historyStoreCtx`: the transformer calls `ReleaseHistory` for every active
   and queued category descriptor (valid even after source closure) and, as
   sole producer, closes `historyQ`; the ingester releases its current chunk
   lease and, as sole producer, closes `historyWriteQ`. Cancellation of
   `historyStoreCtx` prevents subsequent history calls and unblocks a
   context-waiting fake gate. An in-progress `store.Memory` call may finish its
   hard-bounded 50-message or 551-summary/prune atomic section, after which the writer joins normally;
   no store call is detached in another goroutine. `writerCtx` remains alive
   for the final live drain.
4. Coordinator processes at most `Options.BatchMaxOperations` (never above 50)
   already accepted real-time events, releases the rest with a degraded count,
   resolves or times out its one pending live write, and—as sole producer—closes
   `liveWriteQ`. It continues consuming the bounded `resultQ` while the writer
   finishes.
5. Writer discards/releases all optional history, drains the closed
   `liveWriteQ` for one final bounded live quantum, performs at most that one
   final store call, then forbids further calls, closes `resultQ`, and exits.
   Coordinator observes closed `resultQ`, publishes final stopped state to the
   fixed mailbox, and exits.
6. Close the update mailbox after coordinator has joined, then cancel
   `publisherCtx`. The publisher attempts at most one nonblocking final
   delivery, releases its pending value and at most the fixed slots, and—as sole
   owner—closes the public update channel last. Normal shutdown therefore never
   waits for an updates receiver or for the grace deadline.
7. Join every component with `sync.WaitGroup`, drain/release any closed-queue
   leases defensively, stop the grace timer, and return the root cause or first
   component error.

If the grace timer fires, the orchestrator cancels coordinator, writer, and
publisher contexts, forbids another store call, joins them, and releases all
remaining leases without trying to preserve optional output. The first
component error is retained through a capacity-one nonblocking error channel
and `context.WithCancelCause`: the component runner wraps publication in
`sync.Once.Do` and a nonblocking send. Later errors cannot block shutdown.

Tests use barrier channels to prove each stage has stopped. A test-only context
deadline is a failure guard, never the synchronization mechanism.

The component runner is the only 1A panic-recovery boundary. A recovered panic
becomes a redacted internal-invariant cause, stops intake, and cancels every
sibling; that component does not continue. Queue helpers, per-record policy,
and store methods do not recover and pretend an invariant still holds.
Every worker delegates one claimed item/batch to a bounded helper scope. That
helper installs deferred cleanup immediately after claiming its lease, fixed
batch, timer, or history-category token and returns before the next loop
iteration, so defers cannot accumulate for the worker's lifetime. The outer
component runner's panic recovery runs only after those helper defers, so panic
cannot orphan accounting. The panic test asserts all queue budgets, waiter
counts, mailbox leases, and source category tokens return to zero as well as
asserting no post-panic work.

## 10. In-memory store and retention

`store.Memory` is a deterministic test/offline implementation, not an
in-memory imitation of the future SQL schema. It owns no goroutine. Its only
mutable state is protected by one mutex:

```go
type MemoryOptions struct {
    MaxChats              int // mapped from config retention, never above 10,000
    MaxQueryMessages      int // fixed 200; applies only to MessagesBefore
    MaxRetentionSummaries int // count + batch + 1, never above hard 551
    MaxBatchOperations    int // mapped from validated batch, never above 50
}

func NewMemory(MemoryOptions) (*Memory, error)
```

`NewMemory` accepts `MaxChats` 1–10,000, `MaxQueryMessages` 1–200,
`MaxRetentionSummaries` 1–551, and `MaxBatchOperations` 1–50, which lets tests
use tiny bounds while production wiring remains within config ranges; it also
requires `MaxRetentionSummaries >= MaxBatchOperations + 1`.
`cmd/walite` performs this mapping; store never imports config or service.

```text
chats       map[ChatID]Chat
messages    map[ChatID]map[MessageID]Message
anchors     map[ChatID]MessageAnchor       # at most one per retained chat
usage       deterministic counters/bytes
```

There are no tables, migrations, indexes, WAL concepts, media rows, mutation
rows, or unbounded secondary caches.

### Store behavior

- Message identity is `(ChatID, MessageID)`.
- Before the first body-retained message or bodyless anchor candidate for a chat
  is applied, the same bounded operation inserts one placeholder `Chat`. Better
  synthetic chat metadata may replace its name/placeholder flag later; an
  empty name cannot erase a good name.
- All chats, placeholder or named, share the configured global cap
  (default/hard initial cap 10,000). `UpsertChats` may create a named chat only
  while that same cap has room; it can always replace an existing placeholder.
  An unseen chat beyond the cap returns a content-free `store.Error` whose
  `MetadataLimitKind() model.MetadataLimitKind` reports `MetadataChatLimit`; service
  recognizes that method structurally and publishes degradation without
  importing store. It never expands a side collection.
- Duplicate upsert is idempotent. A nonempty body may fill an empty body; an
  empty duplicate cannot erase a body. Conflicting nonempty bodies retain the
  first complete value and increment a content-free ambiguity counter because
  1A has no authoritative mutation/version semantics.
- `MessagesBefore` requires `1 <= limit <= 200`, allocates at most `limit`
  results, and returns `sent_at DESC, message_id DESC`. The exclusive cursor
  uses the same pair, so equal timestamps are deterministic.
- `Chat` is the deliberately minimal chat query: it returns one immutable chat
  plus `found`, never a growing list. Chat-list paging belongs to 1B/SQLite.
- `RetentionSnapshot` has a separate constructor-validated limit no larger
  than `MemoryOptions.MaxRetentionSummaries`. Wiring sets that to validated
  `MessagesPerChat + MaxBatchOperations + 1`, whose hard maximum is 551; a
  valid count setting above 200 therefore does not conflict with query paging.
- Store methods return defensive immutable values; callers cannot mutate maps
  or retained storage through returned slices.
- `ApplyBatch` inserts body-retained messages into `messages`. A
  bodyless/`KeepMetadata` message is an anchor candidate: it replaces the
  chat's single anchor only when its `(sent_at, message_id)` key is older than
  the existing anchor and is not inserted as a bodyless message row.
  `ApplyPrune` keeps the older of its bounded replacement and the existing
  anchor, then deletes every
  message row named by `PrunePlan`; it does not leave stripped rows behind.
  Thus a 100,000-record old-history walk can retain at most one anchor per chat,
  not 100,000 metadata rows.
- `ApplyBatch` also preflights the resulting body-row count for every affected
  chat and rejects the whole batch if it would exceed
  `MaxRetentionSummaries`. In normal service use, the previous policy count plus
  one max-50 batch is below that bound and the following `ApplyPrune` returns it
  to the configured count; a direct store caller cannot grow rows without bound.
- Batches are capped by validated `MemoryOptions.MaxBatchOperations`, never
  above 50; `UpsertChats` applies the same slice-length cap before inspecting or
  copying elements. Immutable input validation occurs before locking, but deduplication
  and global-chat-capacity preflight are performed/rechecked under the same
  mutex immediately before commit. If the context is already cancelled,
  nothing changes. Once a valid bounded batch starts under the mutex, it
  completes atomically rather than exposing a half-batch; worker cancellation
  is checked between calls. Mutex acquisition itself is not claimed
  context-cancellable.
- Read/query/snapshot methods check context before locking and again before
  returning their bounded defensive result; cancellation discards that result.

Only metadata needed by 1A is retained: one placeholder chat for an unknown
chat and, when pruning requires paging continuity in a test, one message anchor
per retained chat. Bodyless mutation placeholders, tombstones, contacts,
edits/deletes, and the architecture's broader metadata tables are deferred.
The one-per-chat anchor and global chat cap make the implemented metadata
finite.

### Pure retention behavior

`syncpolicy.Policy` has no clock read, mutex, goroutine, or I/O. The caller
constructs it with `syncpolicy.New(config.Retention) (*Policy, error)`; the
validated policy stores the static message-count, age, and cache design limits.
The caller passes only changing state explicitly:

- `Now`;
- number of newer retained bodies in the chat;
- current conservative cache usage;
- event origin (`Realtime` or `History`) only for overload shedding policy.

`Decide` follows this order:

1. malformed/zero identity or time is rejected before policy;
2. `SentAt.Before(Now.Add(-MaxAge))` returns `KeepMetadata`; a timestamp exactly
   at the cutoff remains body-eligible;
3. a body with `NewerBodies >= MessagesPerChat` returns `KeepMetadata`;
4. optional history returns `KeepMetadata` before live data when the cache
   estimator reaches the design limit;
5. otherwise return `KeepBody`; malformed policy state returns a typed error.

`PlanPrune` receives an already bounded snapshot (at most configured count plus
one batch and one anchor), containing IDs/timestamps/body-size summaries rather
than message bodies. It sorts by the same deterministic key, returns IDs of
message rows to delete, and selects at most one replacement anchor. It never mutates
input or reads store state itself. Continuous history cannot make the snapshot
unbounded because service requests a capped candidate window after every batch.
Both policy methods return a typed model validation error for an invalid
state/enum instead of manufacturing an invalid opaque result; valid configured
inputs have no I/O-dependent failure path.

The in-memory estimator is intentionally deterministic and conservative:

```text
chat      = 192 + ID bytes + name bytes
message   = 256 + chat-ID bytes + message-ID bytes + retained-text bytes
anchor    = 96  + chat-ID bytes + message-ID bytes
map slack = 25% of the subtotal, rounded upward
```

These numbers are test accounting, not exact heap measurements and not the
future on-disk 250 MiB enforcement. Tests assert monotonic accounting, exact
formula application, history-first shedding, and return below the configured
estimate after prune.

## 11. Textual demonstration

`cmd/walite` is a finite plain-output demonstration. It uses only standard
library packages including `context`, `fmt`, `io`, `os/signal`, and `sync`, plus
the offline walite packages. All fixtures use identifiers such as `chat-a`,
`live-1`, and `history-1`, with generated text such as `synthetic live message`;
no personal or protocol-shaped data is accepted.

`demo.go` defines exact bounded seams: `demoScenario` owns a fake source,
capacity-one `startHistory`, `historyStoreEntered`, and `releaseHistory` gates;
it also owns a distinct never-closed `keepSourceOpen` gate;
`gatedStore` wraps `store.Memory`, delegates every interface method, and uses
`WriteBatch.Origin` plus `sync.Once` to block only the first synthetic history
store call on `releaseHistory` or context. The wrapper starts no goroutine and
is used only by this offline demo/test. The source script ends with
`NewBarrierStep(keepSourceOpen)`, so releasing the history store call cannot
complete the source; only root cancellation unblocks that final barrier and
closes its outputs.

Scenario:

1. construct validated defaults, the fake source, memory store, policy, and
   service, and start `Core.Run`;
2. wait for `UpdateReady`, which Core emits only after all fixed workers report
   their start barriers, then print `walite offline demo ready` before closing
   `startHistory`;
3. the finite lazy history job spans several chunks and reaches `gatedStore`;
   wait for `historyStoreEntered` rather than sleeping;
4. inject a fake live message, wait for its coordinator `UpdateLive`, and print
   it before closing `releaseHistory`;
5. release history, print coalesced progress rather than one line per record;
6. query and print only aggregate retained chats/messages, discarded bodies,
   estimated bytes, and degradation state;
7. with `keepSourceOpen` still blocked, cancel the root context, observe the
   source unblock through context, join components, print `clean shutdown`, and
   exit.

The demo demonstrates coordinator responsiveness while one bounded history
store call is in progress; it does not claim that a writer can preempt an
already-running store call. The writer's next scheduling boundary still
selects live first. The demo has no interactive input, full-screen control
sequences, refresh ticker, continuous redraw, network API, send action, media
action, or external files. Output is bounded to a small fixed number of status
lines regardless of history size. Its exact seam is
`runDemo(ctx context.Context, out io.Writer, scenario demoScenario) error`,
testable without executing a subprocess; `main` supplies signal cancellation
and `os.Stdout`.

## 12. Tests

All fixtures are synthetic and all ordering uses barriers, bounded channels,
or a manually advanced clock. Wall-clock timeouts appear only around a whole
test as deadlock failure guards. Tests do not authenticate, access the network,
or invoke a WhatsApp package.

| Requirement | Exact test file | Planned proof |
|---|---|---|
| valid normalized values | `internal/model/normalize_test.go` | exact-limit IDs/text/event construct and access unchanged |
| invalid normalized values | `internal/model/normalize_test.go` | empty/control/oversized IDs plus zero message/event timestamps return typed errors without content; legal maximum charge stays below event ceiling |
| oversized UTF-8 text | `internal/model/normalize_test.go` | multi-byte rune boundary, invalid prefix replacement, <=16 KiB valid output, truncation flag |
| huge synthetic strings | `internal/model/normalize_test.go` | 1, 8, and 32 MiB inputs retain/copy only <=16 KiB and event charge remains <=32 KiB |
| config ranges/aggregate | `internal/config/values_test.go` | every boundary, overflow, zero-as-unlimited rejection, defaults sum 6.5 MiB |
| model/config limit alignment | `internal/config/limits_external_test.go` | model 32-KiB event and 32-record/128-KiB chunk maxima fit every accepted/default lane |
| history/update model bounds | `internal/model/history_test.go`, `update_test.go` | class/status access, exact chunk/update limits, invalid kinds, indexed access, and defensive ownership |
| store/retention model bounds | `internal/model/store_test.go`, `retention_test.go` | cursor sentinel, 50/551 collection bounds, negative counters, invalid enums, prune-result accounting |
| fake source ownership | `internal/wa/fake_source_test.go` | producer closes three category outputs once; admission closes while delivered cursor/release remain valid; full/closed/cancel outcomes |
| fake job identity | `internal/wa/fake_source_test.go` | duplicate admitted ID is rejected for source lifetime; failed category admission does not consume ID; 256-ID bound is finite |
| interface placement | `internal/service/interfaces_external_test.go` | structural assignments compile while implementations have no service import |
| queue entry saturation | `internal/service/queue_test.go` | free bytes plus full entries rejects without byte leak |
| byte-budget saturation | `internal/service/budget_test.go` | free entry plus insufficient bytes rejects/blocks as documented; used never exceeds cap |
| permit consume release | `internal/service/queue_test.go` | lease remains charged in flight, then returns exactly once |
| permit drop/drain release | `internal/service/queue_test.go` | rejected enqueue and shutdown drain return to zero |
| permit cancellation release | `internal/service/budget_test.go` | barrier between acquire/enqueue, cancel, no reservation remains |
| permit replacement release | `internal/service/mailbox_test.go` | atomic old/new charge and generation, failed replace preserves old |
| publisher lost wake | `internal/service/mailbox_test.go` | full public output plus two same-key replacements consumes one wake; post-send synchronous rescan publishes newest generation |
| double release | `internal/service/budget_test.go` | `errPermitReleased` sentinel, no underflow or second wake |
| no hidden overflow | `internal/service/queue_test.go` | entries/depth never exceed fixed capacity under concurrent producers |
| auxiliary bounds | `internal/service/coordinator_test.go` | status/result/error/wake/update-output paths saturate at 8/8/1/1/1 without spill goroutines |
| category isolation | `internal/wa/fake_source_test.go`, `internal/service/history_test.go` | full bulk leaves metadata/on-demand usable; metadata rejects nonzero records; transformer rechecks metadata/on-demand before the next bulk chunk; unknown drops/degrades |
| four-reference maximum | `internal/wa/fake_source_test.go` | 2/1/1 queued-or-transformer-active tokens; fifth same-category job is shed |
| exactly-once history walk | `internal/wa/fake_source_test.go`, `internal/service/history_test.go` | completed job: cursor opened once/every record once/release once; cancellation: no record more than once and release once |
| real-time precedence | `internal/service/coordinator_test.go` | preloaded live/history lanes; live update/store call wins next scheduling boundary |
| startup ordering | `internal/service/coordinator_test.go` | barriers cancel before commit (no ready, source closes) and after commit (ready then stopping); both paths open/abort gate and join |
| full history-path isolation | `internal/service/history_test.go` | prefilled one-entry test lane reaches `Stats.EntryWaiters == 1`; coordinator publishes live before dequeue/release |
| live writer preference | `internal/service/writer_test.go` | both lanes ready initially and live arriving after a 25-op history quantum runs next |
| duplicate messages | `internal/store/memory_test.go` | same composite ID remains one row; completeness merge is deterministic |
| chat query | `internal/store/memory_test.go` | known/unknown ID returns one immutable value/found flag; cancellation returns no value |
| deterministic ordering | `internal/store/memory_test.go` | equal timestamps page by descending message ID without repeat/gap |
| retention count | `internal/syncpolicy/policy_test.go` and `internal/store/memory_test.go` | exactly newest 100 eligible bodies remain |
| retention age boundary | `internal/syncpolicy/policy_test.go` | one nanosecond older drops; exact cutoff stays |
| finite placeholders/anchors | `internal/store/memory_test.go` | named/placeholder creation shares global cap; 100,000 bodyless candidates leave one anchor/no rows; direct body rows cannot exceed snapshot bound |
| cache estimator/prune | `internal/store/memory_test.go` | formula is stable; optional history prunes before live at limit |
| cancelled store call | `internal/store/memory_test.go` | pre-cancelled call makes no change; bounded started batch is atomic |
| fake clock | `internal/service/coordinator_test.go`, `writer_test.go`, `shutdown_test.go` | each test waits for timer-registration count, then busy/batch/grace fires only through `manualClock.Advance`; stop is idempotent |
| root cancellation | `internal/service/shutdown_test.go` | every blocking queue/source/store barrier unblocks through context |
| shutdown ownership | `internal/service/shutdown_test.go` | channels close in documented order; update channel closes last |
| goroutine termination | `internal/service/shutdown_test.go` | start/stop repetitions join explicit done barriers; no goroutine-count equality assumption |
| deliberate panic boundary | `internal/service/shutdown_test.go` | component panic cancels siblings/intake, runs in-flight cleanup, leaves every budget/token at zero, and does no post-panic work |
| textual demo | `cmd/walite/demo_test.go` | ready first, live before history continuation, finite summary, clean stop, synthetic strings only |
| 100,000 records | `internal/service/core_integration_test.go` | lazy fixed chunks, bounded peak queue stats, cancellation and live injection |
| race safety | all concurrency tests | `go test -race ./...` under repeated saturation/cancellation |

The active history-category token remains occupied after a job leaves its
admission slot and until traversal completes or is cancelled. This makes the
2/1/1 test count queued plus active jobs and prevents a transient fifth
reference. The fake 100,000-record producer generates one bounded chunk at a
time rather than prebuilding a large slice.

Allocation assertions avoid fragile absolute RSS checks. Tests prove retained
length, queue charges, fixed depths, and detached bounded output;
benchmarks report allocation bytes for increasing source size so regressions
remain visible.

## 13. Benchmarks

Every benchmark calls `b.ReportAllocs()` where allocations matter and consumes
results so the compiler cannot remove work. Inputs are deterministic.

| Benchmark | File | Cases/metrics |
|---|---|---|
| queue admission | `internal/service/queue_benchmark_test.go` | available/full entry/full bytes; `TryPut` ns/op and allocs/op |
| maximum normalized event | `internal/model/normalize_benchmark_test.go` | exact 16 KiB valid, invalid UTF-8, 1/8/32 MiB source; ns/op, B/op, allocs/op |
| 100,000 history records | `internal/service/history_benchmark_test.go` | lazy 32-record chunks through policy and fake sink; throughput, peak queue entries/bytes, retained count |
| retention evaluation | `internal/syncpolicy/policy_benchmark_test.go` | eligible, too old, count full, cache full; ns/op and zero/known allocations |
| in-memory upsert | `internal/store/memory_benchmark_test.go` | batches 1/25/50, unique and duplicate; ns/op, B/op, allocs/op |
| in-memory prune | `internal/store/memory_benchmark_test.go` | 100, 1,000, and 10,000 synthetic messages across bounded chats; time and allocations |

The 100,000-record benchmark reports observed memory/allocation behavior; it
does not turn fake results into a promise about server-decoded HistorySync.
Benchmark thresholds are recorded in `docs/milestone-1a-results.md` after
running on the target-like machine. CI guards correctness, not machine-specific
nanoseconds.

## 14. Acceptance checklist

| Milestone 1A requirement | Planned implementation | Planned evidence | Validation command | Status in 1A |
|---|---|---|---|---|
| `internal/model` | `model.go`, `normalize.go`, later bounded model files | model normalization tests | `go test ./internal/model` | planned |
| service-owned interfaces | `service/interfaces.go` | external structural assignments | `go test ./internal/service` | planned |
| config value validation | `config/values.go` | full boundary/aggregate table tests | `go test ./internal/config` | planned; parser/XDG deferred |
| fake event source | `wa/fake_source.go` | ownership/full/cancel tests | `go test ./internal/wa` | planned |
| in-memory store | `store/memory.go` | dedup/order/placeholder/usage tests | `go test ./internal/store` | planned; SQLite deferred |
| pure retention | `syncpolicy/policy.go` | exact count/age/cache decisions | `go test ./internal/syncpolicy` | planned |
| bounded entry and bytes | `service/budget.go`, `queue.go`, `mailbox.go` | every saturation/release path | `go test ./internal/service` | planned |
| cancellation | all `Run`/blocking APIs | cancellation and shutdown barriers | `go test -race ./internal/service` | planned |
| textual demo | `cmd/walite/main.go`, `demo.go` | captured finite output | `go test ./cmd/walite`; `go run ./cmd/walite` | planned |
| offline tests | package and integration `_test.go` files | no network-capable dependency exists | `go test ./...` | planned |
| benchmarks | five benchmark files | `-benchmem` results record | benchmark command in section 3 | planned |
| acyclic package graph | imports in section 4 | `go list` plus review | `go list -deps ./...` | planned |
| demo ready before history | demo prints before history gate | `demo_test.go` | `go test ./cmd/walite` | planned |
| live precedence | coordinator and writer loops | coordinator/writer barrier tests | `go test ./internal/service` | planned |
| full history path cannot delay live | separate history ingester | saturated-path barrier test | `go test ./internal/service` | planned |
| category admission <=4 | `wa/fake_source.go` fixed 2/1/1 tokens | queued+active token test | `go test ./internal/wa ./internal/service` | planned lazy fake analogue; real enum/payload deferred |
| newest 100 / 90 days | policy plus prune plan/store | boundary and integration tests | `go test ./internal/syncpolicy ./internal/store` | planned |
| finite 1A metadata | placeholder chat and one anchor/chat | cap/overflow tests | `go test ./internal/store` | planned; mutations/contacts deferred |
| huge strings respect queue memory | bounded normalization and verified charges | huge-input and budget tests | `go test ./internal/model ./internal/service` | planned |
| deterministic fake clock | service test helper | manually advanced batch/cancel tests | `go test ./internal/service` | planned |
| zero leaked owned goroutines | WaitGroup/done barriers | repeated shutdown test | `go test -race ./internal/service` | planned |
| no media call | no media type/interface/package in graph | dependency/API inventory | `go list -deps ./...` | planned; all media deferred |
| standard library only | unchanged `go.mod` | module/dependency listing | `go list -m all` | planned |
| required quality gates | all packages | vet/unit/race | `gofmt`; `go vet ./...`; `go test ./...`; `go test -race ./...` | planned |

Items intentionally deferred are tcell/TUI work (1B), SQLite/filesystem cache
enforcement (2), whatsmeow/authentication (3), real messages (4), real
HistorySync payload classification/lifetime (5), sending (6), media (7), and
remote older-history requests (8).

## 15. Proposed first coding increment

The smallest first coding increment is model construction only. It introduces
no goroutine, channel, service interface, store, policy, configuration,
demonstration, or dependency.

It creates exactly:

```text
internal/model/model.go
internal/model/normalize.go
internal/model/normalize_test.go
```

`model.go` defines opaque `ChatID`, `MessageID`, `Message`, `MessageInput`, and
`Event` plus read-only accessors. `normalize.go` defines hard
limits, typed validation errors, bounded UTF-8 normalization, constructors, and
the overflow-safe conservative byte-charge formula. `normalize_test.go` covers
all boundaries, immutability, malformed UTF-8, huge strings, and exact charge
behavior.

Questions that do not block this first increment but must be resolved at their
own review boundary are:

- whether target measurements justify changing conservative fixed accounting
  constants while keeping the public 16/32/128 KiB hard bounds unchanged;
- how the later SQLite milestone will translate the deterministic 250 MiB
  estimator into filesystem accounting without changing retention semantics;
- whether the then-pinned whatsmeow source supports reliable category
  classification, ON_DEMAND request correlation, and safe retained-payload
  lifetime; these remain **UNKNOWN** until milestone 5 and the fallback is
  application drop/degrade rather than a deep copy;
- which mutation metadata beyond the deliberately deferred 1A placeholder
  chat and one-per-chat anchor will be required by later protocol work.

The first increment's completion command set is:

```text
gofmt -w internal/model/*.go
go vet ./internal/model
go test ./internal/model
go test -race ./internal/model
git diff --check
```
