# Walite architecture

## Evidence and decision status

This document is an architecture proposal grounded in `docs/reference-analysis.md`. Statements about the checked-out whatsmeow source retain the classifications **CONFIRMED**, **INFERENCE**, or **UNKNOWN** used there. Proposed walite behavior is a **DESIGN DECISION**, not a confirmed WhatsApp capability. In particular, walite will not claim that initial history transfer can be disabled, precisely limited, ordered globally, or made complete on demand.

## 1. Goals and non-goals

### First usable version goals

Walite is one lightweight WhatsApp-only terminal process for a personal account. It should provide:

- QR authentication and a persistent linked-device session through maintained `go.mau.fi/whatsmeow`;
- a chat interface that becomes interactive before initial HistorySync finishes;
- recent incoming and explicitly sent outgoing text messages;
- a bounded recent-message cache with defaults of 100 messages per chat, 90 days, and 250 MB total;
- explicit, best-effort requests for older messages in one chat;
- explicit attachment download with no prefetch;
- graceful, context-driven shutdown;
- visible connection, reconnect, backlog, degradation, and synchronization state;
- deterministic offline tests through fakes.

“Immediate” means the terminal event loop starts after configuration and opening the local cache, without waiting for connection, authentication, contact discovery, or history import. An empty or stale cache is still usable for navigation and diagnostics.

### Initial non-goals

- A complete WhatsApp archive or guaranteed recovery of all history.
- Bulk, automated, scheduled, shell-triggered, or unsolicited messaging.
- Status posting, voice/video calls, stories, newsletters, business automation, bots, or exact feature parity.
- Multi-account support or support for Telegram, Signal, or other protocols.
- Automatic media download, thumbnails, link previews, or background attachment retries.
- Browser embedding, WhatsApp Web automation, Electron, Chromium, or protocol reimplementation.
- Cross-device reconciliation beyond events and APIs exposed by whatsmeow.
- Full-text search or indexing years of history in the first version.

## 2. Package structure

### Responsibilities

`cmd/walite`

- Parses flags, loads configuration, derives the root signal context, constructs implementations, and owns top-level shutdown.
- Contains no WhatsApp, retention, SQL, or widget logic.

`internal/config`

- Defines configuration and validates safe ranges.
- Resolves XDG config/data/cache paths and creates/checks private paths.
- Has no dependency on the other internal packages.

`internal/model`

- Owns shared application values such as IDs, chats, messages, events, commands, errors, history requests, cursors, and view updates.
- Contains validation helpers for bounded value construction, but no I/O, interfaces, goroutines, or application dependencies.
- Has no internal-package dependencies.

`internal/wa`

- Is the only package importing whatsmeow or its protobufs.
- Owns the whatsmeow client, pairing, connection, event handler registration, minimal event copying, incremental HistorySync traversal, older-history request construction, and explicit media download.
- Does not import SQLite or terminal packages.
- Imports only `internal/model` from the application packages. It structurally implements interfaces declared by `internal/service` without importing `service`.

`internal/store`

- Supplies in-memory and later SQLite implementations of the persistence interfaces owned by `internal/service`.
- Owns schema, migrations, transactions, query pagination, byte accounting, pruning, and database permissions.
- Does not import whatsmeow or the TUI.
- Imports only `internal/model` from the application packages. Concrete stores structurally implement service-owned interfaces without importing `service`.

`internal/syncpolicy`

- Contains pure retention, priority, and pruning decisions using `internal/model`, `internal/config`, and an injected clock.
- Does not perform I/O and imports neither whatsmeow nor UI packages.

`internal/service`

- Owns application lifecycle, the interfaces it consumes, and bounded application queues.
- Coordinates protocol events, retention, prioritized writes, queries, sends, downloads, and view updates.
- Imports `internal/model` and `internal/syncpolicy`; it does not import concrete `internal/wa` or `internal/store` implementations.

`internal/tui`

- Owns terminal state, input, layout, view models, and incremental redraws.
- Calls `ApplicationService`; consumes immutable view updates.
- Never imports whatsmeow, protobufs, `database/sql`, or a SQLite driver.
- Imports `internal/model` and `internal/service` only.

### Dependency direction

```text
  cmd/walite ──> config, service, wa, store, tui
  tui        ──> model, service
  service    ──> model, syncpolicy
  syncpolicy ──> model, config
  wa         ──> model, whatsmeow
  store      ──> model, SQLite implementation
  model      ──> (no internal package)
  config     ──> (no application package)

Dependency direction:
  model: no internal dependencies
  config: no application dependencies
  syncpolicy -> model, config
  service -> model, syncpolicy
  wa -> model, plus external whatsmeow
  store -> model, plus SQLite implementation dependency
  tui -> model, service
  cmd/walite -> config and all selected implementations for wiring

Forbidden:
  service -> concrete wa or store packages
  tui -> wa, whatsmeow, store implementation, database/sql
  store -> tui, service, or wa
  wa -> service, store, or tui
  wa callback -> terminal widgets or SQLite
```

`cmd/walite` performs compile-time assignments such as `var client service.WhatsAppClient = waClient` when wiring structural implementations. The interface owner therefore depends on abstractions and model values, while implementations do not import the consumer. This graph is acyclic and does not require a generic “common” package.

### Adapted architectural ideas

- Dependency-injected client/store seams are adapted conceptually from vicentereig/whatsapp-cli, `internal/commands/interfaces.go`, interfaces `WAClient` and `MessageStore` (project declared MIT in README; no standalone licence in checkout). Walite independently designs smaller consumer-owned interfaces in `internal/service`, shared values in dependency-free `internal/model`, and structural implementations that do not import their consumer.
- A protocol boundary and paged cache reads are adapted conceptually from d99kris/nchat, `lib/common/src/protocol.h::Protocol` and `src/uimodel.cpp::UiModel::Impl::RequestMessages` (MIT core). No code is copied.
- Opt-in media is adapted conceptually from normen/whatscli, `messages/session_manager.go::downloadCommand` (README-declared MIT). No code is copied.

## 3. Domain model

These application-owned values live in `internal/model`. Exact field types may be refined before implementation, but protocol objects never escape `internal/wa`.

```go
type AccountState struct {
    Linked      bool
    SelfID      ChatID       // empty until known
    DisplayName string       // sanitized for display
}

type ConnectionState uint8
const (
    ConnectionOffline ConnectionState = iota
    ConnectionConnecting
    ConnectionOnline
    ConnectionReconnecting
    ConnectionAuthRequired
    ConnectionLoggedOut
)

type ChatID string
type MessageID string
type DownloadRef string // immutable; may contain arbitrary binary bytes

type Chat struct {
    ID          ChatID
    Name        string
    IsGroup     bool
    LastMessage time.Time
    Unread      uint32
    Muted       bool
    Archived    bool
}

type Contact struct {
    ID          ChatID
    DisplayName string
}

type Message struct {
    ID          MessageID
    ChatID      ChatID
    SenderID    ChatID
    SentAt      time.Time
    FromMe      bool
    Text        string
    BodyTruncated bool
    Kind        MessageKind
    Revision    uint64       // application revision, not a WhatsApp version
    EditedAt    time.Time
    Deleted     bool
    Attachment *Attachment
}

type Attachment struct {
    ID           string
    MessageID    MessageID
    Kind         AttachmentKind
    DisplayName  string
    MIME         string
    Size         int64
    Available    bool
    Downloaded   bool
    LocalPath    string      // returned only to authorized service/UI flow
    DownloadRef  DownloadRef // opaque, bounded, sensitive reference; see storage/security
}

type QRFrame struct {
    Width     uint16
    Height    uint16
    DarkCells []byte    // packed one-bit raster, never the QR token/string
    ExpiresAt time.Time
}

type Event struct {
    Kind       EventKind
    Priority   EventPriority
    ReceivedAt time.Time
    Message    *Message
    Chat       *Chat
    Contact    *Contact
    Progress   *SyncProgress
    Mutation   *MessageMutation
    Error      *AppError
}

type SyncProgress struct {
    Phase          SyncPhase
    Category       string     // known enum rendered as text, no protobuf
    ServerProgress uint32     // advisory only
    Accepted       uint64
    Discarded      uint64
    Degraded       bool
}

type RetentionDecision struct {
    KeepBody       bool
    KeepMetadata   bool
    Reason         RetentionReason
    EstimatedBytes int64
}

type HistoryRequest struct {
    ChatID         ChatID
    BeforeID       MessageID
    BeforeTime     time.Time
    BeforeFromMe   bool
    Count          int
}
```

Normalized constructors enforce hard limits before a value can enter an application queue:

- maximum encoded normalized `Event`: 32 KiB, including strings, slices, attachment reference, and fixed accounting overhead;
- maximum retained message body: 16 KiB of UTF-8; larger text is truncated at a valid UTF-8 boundary for display/cache, marked `BodyTruncated`, and never copied in full by walite;
- maximum normalized attachment/contact/chat metadata per event: 8 KiB; each ID 512 bytes, display name 1 KiB, MIME/name 1 KiB each, and `DownloadRef` 4 KiB;
- maximum command including compose text: 32 KiB, with the sendable text limit further constrained by the source-confirmed WhatsApp adapter at that milestone.

Oversized required identity fields make the event malformed and it is discarded with a redacted counter. Oversized optional metadata is omitted and marked truncated. Oversized text is bounded while reading/copying from the protocol value; code must not first clone the full string into another application object. These are application-allocation limits and cannot bound memory already allocated inside whatsmeow's decoded event.

`DownloadRef` is an immutable string representation, not an exported mutable byte slice. Its bytes are opaque and may contain arbitrary binary data, including NUL and invalid UTF-8. Only a validating constructor may create one, and only after confirming the source is at most 4 KiB; callers cannot mutate its backing storage through the model API.

`MessageMutation` describes edit, delete, reaction, receipt, or metadata changes without pretending all mutations have a WhatsApp version. Unknown fields are ignored and counted; raw protobuf data is not persisted for convenience.

### Data copied before a whatsmeow callback returns

For a real-time message, `internal/wa` copies only stable identity/source fields, timestamp, direction, normalized text, mutation fields, and minimal attachment metadata/download reference. It does not copy the full protobuf, quoted-message trees, media bytes, QR contents, keys, or complete unknown payloads.

For connection/account/progress events, it copies enum/status values and redacted error categories.

For HistorySync, copying every message in the callback would violate the duration and memory goals. The callback classifies the event using its confirmed enum plus the actual presence of message arrays, then retains at most four immutable payload references inside `internal/wa`: two metadata-only, one confirmed `ON_DEMAND`, and one other message-heavy/bulk reference. Protobuf ownership never crosses the package. A history worker walks each admitted payload exactly once and incrementally transforms one conversation/message at a time into byte-budgeted domain chunks. A full category slot sheds the new application import and marks synchronization degraded. Admission is category-specific and bounded as specified in sections 5 and 6; it does not assume undocumented category order or importance.

The immutability/lifetime assumption for a retained HistorySync pointer is an implementation precondition to verify against the pinned whatsmeow source and race tests. If reliable classification or retained-pointer safety cannot be established, the safe fallback is to discard that application-history event, mark synchronization degraded, and let whatsmeow retain its internal session processing rather than deep-copy a whole batch.

## 4. Interfaces

Signatures are design contracts, not implementation.

```go
type WhatsAppClient interface {
    EventSource
    Account(ctx context.Context) (model.AccountState, error)
    Connect(ctx context.Context) error
    Pair(ctx context.Context, show func(model.QRFrame) error) error
    Disconnect() error
    SendText(ctx context.Context, chat model.ChatID, text string) (model.MessageID, error)
    RequestHistory(ctx context.Context, req model.HistoryRequest) error
    DownloadAttachment(ctx context.Context, ref model.DownloadRef, dst io.Writer) error
}

type EventSource interface {
    RealtimeEvents() <-chan model.Event
    HistoryEvents() <-chan model.Event
    StatusEvents() <-chan model.Event
}

type MessageStore interface {
    PutMessageBatch(ctx context.Context, messages []model.Message) error
    ApplyMutationBatch(ctx context.Context, changes []model.MessageMutation) error
    MessagesBefore(ctx context.Context, chat model.ChatID, before model.Cursor, limit int) ([]model.Message, error)
    OldestMessage(ctx context.Context, chat model.ChatID) (model.MessageAnchor, error)
    Prune(ctx context.Context, plan model.PrunePlan) (model.PruneResult, error)
    CacheUsage(ctx context.Context) (model.CacheUsage, error)
}

type ChatStore interface {
    PutChatBatch(ctx context.Context, chats []model.Chat) error
    PutContactBatch(ctx context.Context, contacts []model.Contact) error
    ListChats(ctx context.Context, cursor model.Cursor, limit int) ([]model.Chat, error)
    Chat(ctx context.Context, id model.ChatID) (model.Chat, error)
}

type RetentionPolicy interface {
    Decide(message model.Message, state model.RetentionState) model.RetentionDecision
    PlanPrune(state model.CacheSnapshot) model.PrunePlan
}

type ApplicationService interface {
    Start(ctx context.Context) error
    Snapshot(ctx context.Context) (model.ViewSnapshot, error)
    Updates() <-chan model.ViewUpdate
    Submit(ctx context.Context, command model.Command) error
}

// This wiring interface may live in internal/tui; service does not import it.
type View interface {
    Run(ctx context.Context, app ApplicationService) error
}

type Clock interface {
    Now() time.Time
    Wait(ctx context.Context, d time.Duration) error
}

type Logger interface {
    Debug(msg string, fields ...model.LogField)
    Info(msg string, fields ...model.LogField)
    Warn(msg string, fields ...model.LogField)
    Error(msg string, fields ...model.LogField)
}
```

`WhatsAppClient`, `EventSource`, `MessageStore`, `ChatStore`, `RetentionPolicy`, `Clock`, and `Logger` are declared in `internal/service`, because service consumes them. Their method signatures use only standard-library and `internal/model` types, so structural `wa`/`store` implementations do not import service. `ApplicationService` is the service API consumed by `internal/tui`. A small `View` wiring interface may be declared in `internal/tui` for `cmd/walite`; service never imports it. Interfaces are split by role so a TUI fake need not implement storage and a retention test need not construct WhatsApp. The pairing callback receives a bounded raster `model.QRFrame`, never the QR token/string. The producer owns the backing bytes until the callback returns; the TUI path copies into its one-frame protected mailbox if it must retain them, and both copies are zeroed/released on replacement, expiry, success, cancellation, or shutdown. Maximum QR dimensions and byte length are validated before display.

### Ownership rules

- Channel producers create and close their own channels. Consumers never close received channels.
- `ApplicationService.Submit` validates the command's 32 KiB normalized bound and attempts a bounded enqueue. It returns typed `Busy` immediately when command capacity is unavailable, `Cancelled` when `ctx` ends, and `ShuttingDown` after intake closes. It never spawns an overflow goroutine and the TUI retains compose text on failure.
- The service exclusively owns and closes the update channel. The TUI only receives it; a closed update channel means service termination.
- Events and updates are immutable values after send. Slices returned by stores are caller-owned copies capped by `limit`; stores do not retain them.
- Every variable-size queued value acquires weighted bytes from the relevant byte-budget semaphore before enqueue and releases them exactly once after consumption/drop. Ownership of the permit travels with the envelope, not the domain value.
- Batches passed to stores remain caller-owned but must not be mutated until the call returns.
- `io.Writer` for attachment download is opened by the service using a safe temporary file and is closed by the service, not `internal/wa`.
- Every blocking method accepts context. `Disconnect` must be idempotent and bounded; top-level shutdown still has a deadline.

## 5. Event flow

### Existing linked session startup

1. `cmd/walite` opens configuration/cache, creates the service and starts the TUI event loop immediately.
2. Service coordinator loads one bounded chat page and the selected chat page through store queries, then sends a replaceable snapshot update.
3. WA owner goroutine calls `Connect(ctx)`. Status goes through an eight-slot keyed status mailbox at control priority.
4. On success, UI receives `Online`; failures become persistent connection state and reconnect scheduling.

Source goroutines are main, TUI, coordinator, and WA owner. No HistorySync or database result blocks first paint. Cancellation stops connect and queries; failure leaves an offline-readable UI.

### No linked session and QR authentication

1. Account inspection reports `AuthRequired`; the status area offers an explicit authenticate command.
2. The coordinator forwards that command to the WA owner. `Pair` obtains QR events from whatsmeow.
3. A QR frame is rendered in a modal through the bounded pairing callback and protected one-frame mailbox; QR contents are never logged, cached, copied to clipboard, or persisted.
4. Expiry replaces the frame; success clears it and transitions to connecting/online.

The WA owner performs pairing. QR display uses the dedicated one-frame fixed mailbox: a newer frame atomically replaces and zeroes the older raster and signals the bounded wake channel. It is not an attempted replacement inside an ordinary Go channel. Cancellation clears the modal and disconnects. Authentication failures persist until acknowledged or retried.

### Real-time incoming message

1. A whatsmeow dispatch goroutine invokes the adapter callback.
2. Adapter copies/normalizes at most 32 KiB, acquires the event's exact byte charge from the 2 MiB real-time budget, and attempts a nonblocking send to `realtimeQ` (capacity 128).
3. Coordinator drains real-time before history, applies retention, and submits accepted writes to the dedicated live lane with cancellation and a bounded busy deadline. The writer's strict live preference prevents history saturation from causing this wait; failure degrades the cache rather than creating pending overflow.
4. Store writer commits a live-first bounded transaction and reports a store result.
5. Coordinator emits a coalescible chat-list delta and, if visible, a message append/update.

If `realtimeQ` is full, the callback does not block or spawn a goroutine. It records a dropped-event counter, sets persistent `CacheDegraded`, and returns. The protocol/session continues, but local cache completeness is not promised. This exceptional loss is logged without content. Reconnection/history may later redeliver duplicates, which are upserted.

### Outgoing text message

1. TUI creates `SendText` only after explicit Enter/confirmation and calls `Submit(ctx, command)`; capacity/byte-budget exhaustion returns typed `Busy`, cancellation returns `Cancelled`, and closed intake returns `ShuttingDown`, while compose text remains.
2. Coordinator validates selected chat, non-empty text, connection state, and size; it sends one request to the WA owner.
3. WA owner calls whatsmeow with command context. On server acknowledgement, it returns stable ID/time.
4. Coordinator enqueues the resulting message on `liveWriteQ` and updates the view. Echoed real-time events deduplicate by `(chat_id, message_id)`.

Cancellation leaves text in compose if no acknowledgement. An ambiguous send timeout is shown persistently; walite does not blindly retry and risk duplication.

### Initial HistorySync batch

1. whatsmeow downloads/parses the blob and performs its internal history-derived storage. **CONFIRMED:** current source stores mappings, push names, message secrets/privacy tokens, NCT salt and global settings independently of walite's cache callback.
2. Adapter callback reads `HistorySyncType` and inspects whether conversations/status arrays actually contain messages. It admits a payload reference to one of three independent fixed queues: metadata-only capacity 2, confirmed `ON_DEMAND` capacity 1, or other message-heavy capacity 1. Total retained payload references are therefore at most four.
3. Metadata-only means the event has no conversation messages and no status-message bodies; it may still contain chats, contacts, mappings, names, settings, or progress. It never competes for the message-heavy slot. `ON_DEMAND` is distinguishable by the current confirmed enum, although correlation to a particular request remains **UNKNOWN**. Known non-ON_DEMAND message-heavy events use the bulk slot. An unknown enum/category is not assigned importance: it is dropped from walite application import and marks synchronization degraded.
4. The history transformer services metadata, ON_DEMAND, then bulk as an application policy, not a claim about WhatsApp importance or delivery order. It walks a retained payload incrementally, converts records into chunks bounded by both 32 records and 128 KiB, and acquires history byte-budget permits before enqueue.
5. A separate bounded history-ingestion worker—not the coordinator—consumes `historyQ`, applies retention, and sends to `historyWriteQ`. If that write path is full, only this worker blocks cancellably; the coordinator continues draining real-time, control, commands, and view work.
6. The store writer checks `liveWriteQ` before every history sub-batch and commits at most 50 rows or 25 ms worth.
7. UI receives throttled/coalesced sync progress through fixed mailboxes at no more than 4 Hz and view deltas only for affected visible data.

If an admission queue is full, optional bulk message history is shed first simply because it has only its own one slot; it can never consume or displace metadata/ON_DEMAND slots. A full metadata or ON_DEMAND slot also causes that new event to be dropped rather than violating bounds, with a category-specific degraded marker. No queue evicts an in-process reference because doing so would complicate ownership. There is no overflow slice, goroutine spill, or deep copy. Cancellation stops iteration between individual records/chunks. A corrupt record is counted and skipped; a corrupt whole event is reported and released. If current whatsmeow cannot reliably expose the enum or message-array inspection at implementation time, all unclassifiable events follow the unknown-category fallback: no application import, degraded status, normal whatsmeow internal processing.

### Chat/contact metadata discovery

Minimal metadata from status, app state, push-name, message, and HistorySync events enters control/history paths. Chat/contact upserts are separately batched. Before inserting a message whose chat is absent, the same transaction first upserts a placeholder chat keyed by `chat_id`; later better metadata replaces the placeholder, while an empty later name never erases a good name. This ordering satisfies the message foreign key without disabling it. UI chat deltas are keyed by chat ID and coalesced in fixed mailboxes.

Exact mapping of all metadata to HistorySync categories is **UNKNOWN**, so no category is assumed to be the sole source.

### Explicit older-message request

1. User invokes “older” in one chat. Coordinator obtains the oldest local anchor from the store.
2. It constructs `HistoryRequest` with chat, ID, timestamp, direction and configurable request count default 50.
3. WA owner calls current `BuildHistorySyncRequest`/peer send internally.
4. Any ON_DEMAND response follows the normal bounded history path and is tagged to the request where possible.
5. A request deadline ends the visible pending state without polling.

**UNKNOWN:** delivery, exact count, access, completeness, response timing, and correlation guarantees. Failure is `HistoryUnavailable`, does not clear cached messages, and is not automatically retried.

### Explicit attachment download

The user selects a message and invokes download. Coordinator checks attachment metadata, creates a sanitized destination below the configured directory using a 0600 temporary file, and WA owner streams bytes only for this request. Service fsyncs if configured, closes, atomically renames, verifies containment/mode, updates attachment metadata, and notifies UI. Cancellation removes the temporary file. Expired/unavailable media is persistent on the message but not retried automatically.

### Reconnect

Disconnected status transitions to reconnecting. The WA owner alone schedules capped exponential backoff with jitter using `Clock` timers and context; there is one pending timer, no sleep loop. Control commands can request immediate retry. Successful reconnect cancels/reset backoff. Rate limits honor a server delay when exposed; otherwise retry is conservative. UI remains usable from cache.

### Shutdown

TUI sends Quit or root signal cancels context. Coordinator stops accepting commands, emits “shutting down,” asks WA owner to disconnect, stops history iteration, permits the writer a bounded final live batch, cancels remaining work, waits for owned goroutines, closes stores, then allows TUI to restore the terminal. Failure to finish inside the shutdown goal is logged and returned; no `os.Exit` occurs inside packages.

## 6. Concurrency model

### Long-running goroutines

| Goroutine | Owner | Count | Purpose |
|---|---|---:|---|
| whatsmeow internals/callbacks | `internal/wa`/library | library-defined | network and event dispatch; callbacks only copy/enqueue |
| WA owner | `internal/wa` | 1 | connect, pair, send, request, download command serialization where needed |
| history transformer | `internal/wa` | 1 | incrementally traverse one opaque history job |
| history ingestion | `internal/service` | 1 | retention and cancellable history-write submission; may block without blocking coordinator |
| application coordinator / real-time worker | `internal/service` | 1 | prioritize, apply policy, update view state |
| store writer | `internal/store` implementation | 1 | live-first batched transactions and pruning commands |
| TUI event loop | `internal/tui` | 1 | input, terminal events, incremental rendering |

No goroutine is started per event, redraw, overflow, media item, or retry. Optional notification delivery, if later added, must have one owned bounded worker.

### Queue policy

| Queue | Capacity | Producer -> consumer | Full behavior |
|---|---:|---|---|
| status mailboxes | 8 fixed keyed slots | adapter -> coordinator | producer overwrites only its key under a small mutex/atomic generation; terminal errors have dedicated slots |
| `realtimeQ` | 128 events + 2 MiB byte budget | adapter -> coordinator | nonblocking reject, count, persistent degraded state; never spawn/block |
| metadata history jobs | 2 payload references | callback -> transformer | independent of message-heavy admission; drop new event and degrade when full |
| ON_DEMAND history jobs | 1 payload reference | callback -> transformer | confirmed enum only; drop new response and mark request/degraded when full |
| bulk history jobs | 1 payload reference | callback -> transformer | optional message-heavy history shed when full |
| `historyQ` | 4 chunks, each ≤32 records and ≤128 KiB; 512 KiB byte budget | transformer -> history ingestion | transformer blocks cancellably; retained payload references remain ≤4 |
| `commandQ` | 32 commands + 512 KiB byte budget | TUI -> coordinator | `Submit` returns typed busy/cancelled/shutdown; compose text remains |
| `liveWriteQ` | 128 operations + 1 MiB byte budget | coordinator -> writer | cancellable bounded busy deadline; sustained full degrades cache and stops history admission, never creates pending overflow |
| `historyWriteQ` | 256 operations + 2 MiB byte budget | history ingestion -> writer | only history-ingestion worker blocks cancellably; coordinator is never the sender |
| view mailboxes | 64 fixed keyed slots + 512 KiB byte budget | coordinator -> TUI | chat/message/status keys overwrite their own fixed slot; persistent errors have reserved slots |

An ordinary Go channel cannot replace an existing element by key. Status/view coalescing therefore uses fixed arrays of keyed slots plus a bounded wake channel of capacity 1. Updating a slot replaces and releases the previous envelope's byte permit under the mailbox lock, increments a generation, and nonblockingly signals wake. Consumers snapshot only dirty generations. Capacities are defaults subject to offline measurement, with hard safe ranges in configuration.

Entry counts alone do not bound memory. Every normalized real-time event, history chunk, command, write operation, and view update carries its exact bounded encoded/string/slice byte charge plus conservative fixed overhead. A weighted byte-budget semaphore is acquired before enqueue and released on consume, replacement, or drop. Values that exceed the per-value maximum are truncated/omitted/rejected before budget acquisition. The `realtimeQ` (2 MiB), `historyQ` (512 KiB), `commandQ` (512 KiB), `liveWriteQ` (1 MiB), `historyWriteQ` (2 MiB), and view-mailbox (512 KiB) budgets total 6.5 MiB. The remaining approximately 1.5 MiB of the 8 MiB target is reserved for status mailboxes, envelopes, and conservative accounting overhead. These limits exclude up to four server-provided decoded HistorySync payloads, whose individual sizes are server-controlled and **UNKNOWN**; admission prevents walite from multiplying each payload into an unbounded second representation.

### Priority and starvation

The coordinator uses strict checks in this order: cancellation/control, all currently ready real-time events up to a fairness cap of 32, store results/commands, then history progress/results. It never consumes `historyQ` or sends `historyWriteQ`; the separate history-ingestion worker owns that potentially blocking path. The writer drains up to 50 live operations first, then at most 25 history operations before rechecking live. History cannot fill the live queue and cannot monopolize a transaction.

Continuous real-time traffic may delay or abandon history; that is intentional. A fairness cap prevents commands/status from being permanently starved without granting history precedence over live messages.

### Shutdown and ownership

`cmd/walite` creates a root context and one top-level `errgroup`/WaitGroup owner. Each component exposes `Run(ctx) error` and starts internal workers only inside that lifetime. Shutdown order is:

1. cancel command intake and TUI actions;
2. unregister/disable WA intake and disconnect;
3. cancel history traversal and release payload reference;
4. drain a bounded final live-write batch, discard pending history writes;
5. wait for WA/history/coordinator/writer;
6. close message and session stores;
7. stop/restore terminal.

Only producers close channels, after their producer goroutines stop. Receivers always select on context. Tests assert termination with deadlines.

### Panic and race containment

Panic recovery exists only at deliberate component boundaries: the top-level component runner and the external-library callback boundary. A recovered panic becomes `InternalInvariant`, records a redacted stack, cancels the owning component/root context, and prevents further intake; execution never silently resumes after possible invariant corruption. Ordinary helper functions and per-record processing do not recover. Shared counters use atomics; mutable connection state belongs to WA owner; application view state belongs to coordinator; database connection belongs to writer; terminal state belongs to TUI. Immutable events cross queues. Race tests cover saturation and cancellation.

## 7. Ordering and deduplication

- Stable identity is `(chat_id, message_id)`, not timestamp or message ID alone. Message IDs may only be assumed stable within their chat for walite storage.
- Database uniqueness enforces that pair. Repeated HistorySync, live echo, reconnect redelivery, and outgoing acknowledgement use idempotent upsert.
- Timestamps sort the presentation but are not trusted as causal versions: clocks can collide, drift, or arrive out of order. Tie-breaker is message ID for deterministic pagination.
- Each accepted application event receives a monotonic local `ingest_seq` from the coordinator. It orders local processing only and is not a WhatsApp version.
- Base message fields use completeness-aware merge: non-empty parsed content may fill an empty placeholder; an empty later event does not erase good content; a delete tombstone hides body; attachment download state is locally monotonic.
- Deletes are tombstones even if the original has not arrived. A later base message does not resurrect a tombstoned record unless a protocol-confirmed restoration semantic is discovered.
- Edits store `edited_at` when available. A newer nonzero edit time wins; with missing/equal times, first accepted non-empty edit wins and ambiguity is logged. This is conservative, not a confirmed server rule.
- Reactions are keyed by message, sender, and reaction identity where source data permits; updates are idempotent. Exact conflict semantics remain **UNKNOWN** until current whatsmeow event fields are inspected for that milestone.
- Receipts store the furthest known application state per participant using a monotonic application rank only where semantics are confirmed. Unknown transitions are retained as metadata and do not alter body order.
- Database writes can complete after a later in-memory view delta, so UI marks uncommitted outgoing/live messages as pending and reconciles on writer result.

**UNKNOWN:** global dispatch ordering, authoritative versions for all edits/deletes/reactions/receipts, whether all mutation timestamps are comparable, and redelivery behavior after overload. Architecture makes operations idempotent but does not solve absent server semantics.

## 8. Synchronization and retention policy

### Separation of responsibility

**CONFIRMED:** current whatsmeow downloads/parses HistorySync and schedules its internal storage of session-relevant mappings, names, secrets/tokens and settings. Walite leaves automatic history download/internal processing enabled.

Walite retains:

- chat/contact metadata needed to display a bounded chat list;
- the newest 100 eligible message bodies per chat;
- messages no older than 90 days;
- bounded tombstones/placeholders needed for supported out-of-order mutations;
- exactly one history anchor/checkpoint row per chat rather than keeping unlimited old message rows;
- attachment metadata, never bytes unless explicitly downloaded;
- at most 250 MB of application cache files under its accounting policy.

Walite discards application bodies that fail age/count/size policy, unsupported payloads, automatic media bytes, and raw protobufs. Compact metadata is explicitly finite:

| Metadata class | Per-chat limit | Global limit | Overflow behavior |
|---|---:|---:|---|
| bodyless old-message identity rows | 32 | 10,000 | oldest non-placeholder identity rows deleted |
| delete tombstones | 16 | 5,000 | oldest tombstones deleted; sync marked degraded |
| edit/delete placeholders awaiting a base message | 16 combined | 5,000 combined | oldest placeholder deleted; sync marked degraded |
| history anchors/checkpoints | 1 | one per retained chat, at most 10,000 | replace the chat's prior anchor; reject new inactive-chat metadata over global chat cap |
| chats | n/a | 10,000 | shed least-recent inactive placeholder chats with no retained messages |
| contacts/names | n/a | 20,000 | shed least-recent unreferenced contacts |

Tombstones/placeholders count within the 32 bodyless-message limit as well as their narrower class limit; limits therefore cannot be added to evade the per-chat bound. One `sync_checkpoints` row holds the oldest usable `(message_id, sent_at, from_me)` anchor for a chat. Pruning an old body updates/replaces that row and deletes the old message row unless it fits a bounded tombstone/placeholder need. Exactly which metadata is required for all future WhatsApp features is **UNKNOWN**; milestone scope restricts this to supported text/edit/delete and explicit-history anchors.

### Incremental algorithm

History transformer yields at most 32 records and 128 KiB after normalized-size checks. Policy first rejects older-than-cutoff records without copying large bodies. For each chat it uses a fixed-size candidate structure capped slightly above 100 and a byte cap until database state is known; it never accumulates a full chat or batch. Store upserts accepted records, then prunes that chat to count/age/metadata limits in the same or a following bounded transaction. Total-size pruning runs incrementally after batches.

### Exceptional behavior

- Very large blob: whatsmeow's decoded blob is unavoidable and server-controlled; walite retains at most four references across metadata-only (2), confirmed `ON_DEMAND` (1), and bulk (1) slots. Each admitted payload is walked exactly once using fixed chunks. A full category slot sheds the new application import and marks synchronization degraded. UI/live traffic continues.
- One chat with years of messages: age check rejects old bodies immediately; at most one anchor plus the finite metadata rows above survive.
- 250 MB reached: the cap includes every application table/index, SQLite main database, WAL, SHM, temporary application-cache files, and media managed under walite's download directory. Stop accepting historical bodies first, prune oldest eligible rows/media globally while preserving newest-per-chat targets where possible, checkpoint WAL, then reject new optional history. A user-selected destination outside walite's managed tree is not counted or managed and is clearly reported as external. Live text may trigger immediate bounded pruning. The configured 250 MiB is a hard managed-footprint target; operations reserve estimated headroom before writing and fail with `CacheLimit` rather than intentionally exceeding it. Filesystem allocation/checkpoint races remain an **UNKNOWN** requiring testing.
- Write backlog: pause history transformer through bounded backpressure; preserve live lane; mark sync paused/degraded. Never add an overflow collection.
- Disk full: rollback transaction, stop all cache writes/history import, retain only the visible bounded in-memory page, show persistent error, and continue network/UI if safe. Sending remains explicit and may work, but outgoing cache persistence failure is prominent.
- Corrupt item: skip, count, redacted log; corrupt batch releases its job. No retry loop.
- Cancellation: check between records and transactions; committed batches remain valid, open transaction rolls back, payload reference releases.
- Repeated initial sync: uniqueness merges duplicates; category-specific bounded admission may shed a new overlapping application import when its category slot is full and marks synchronization degraded; progress never implies completeness.

## 9. Storage design

The application cache is a separate SQLite database from whatsmeow's session database. It must never reuse, attach, query, migrate, or prune whatsmeow tables.

### Proposed schema

```sql
CREATE TABLE app_meta (
    key TEXT PRIMARY KEY,
    value BLOB NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE contacts (
    contact_id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL,
    ingest_seq INTEGER NOT NULL
);

CREATE TABLE chats (
    chat_id TEXT PRIMARY KEY,
    contact_id TEXT REFERENCES contacts(contact_id) ON DELETE SET NULL,
    display_name TEXT NOT NULL DEFAULT '',
    is_group INTEGER NOT NULL DEFAULT 0 CHECK (is_group IN (0,1)),
    last_message_at INTEGER NOT NULL DEFAULT 0,
    unread_count INTEGER NOT NULL DEFAULT 0,
    muted INTEGER NOT NULL DEFAULT 0 CHECK (muted IN (0,1)),
    archived INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0,1)),
    placeholder INTEGER NOT NULL DEFAULT 0 CHECK (placeholder IN (0,1)),
    updated_at INTEGER NOT NULL,
    ingest_seq INTEGER NOT NULL
);

CREATE TABLE messages (
    chat_id TEXT NOT NULL REFERENCES chats(chat_id) ON DELETE CASCADE,
    message_id TEXT NOT NULL,
    sender_id TEXT NOT NULL DEFAULT '',
    sent_at INTEGER NOT NULL,
    from_me INTEGER NOT NULL CHECK (from_me IN (0,1)),
    kind INTEGER NOT NULL,
    body TEXT,
    body_bytes INTEGER NOT NULL DEFAULT 0,
    body_truncated INTEGER NOT NULL DEFAULT 0 CHECK (body_truncated IN (0,1)),
    deleted INTEGER NOT NULL DEFAULT 0 CHECK (deleted IN (0,1)),
    edited_at INTEGER NOT NULL DEFAULT 0,
    local_revision INTEGER NOT NULL DEFAULT 0,
    ingest_seq INTEGER NOT NULL,
    retained_body INTEGER NOT NULL DEFAULT 1 CHECK (retained_body IN (0,1)),
    PRIMARY KEY (chat_id, message_id)
);

CREATE TABLE attachments (
    chat_id TEXT NOT NULL,
    message_id TEXT NOT NULL,
    attachment_id TEXT NOT NULL,
    kind INTEGER NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    mime_type TEXT NOT NULL DEFAULT '',
    declared_bytes INTEGER NOT NULL DEFAULT 0,
    download_ref BLOB,
    local_path TEXT,
    local_bytes INTEGER NOT NULL DEFAULT 0,
    downloaded_at INTEGER NOT NULL DEFAULT 0,
    available INTEGER NOT NULL DEFAULT 1 CHECK (available IN (0,1)),
    PRIMARY KEY (chat_id, message_id, attachment_id),
    FOREIGN KEY (chat_id, message_id)
      REFERENCES messages(chat_id, message_id) ON DELETE CASCADE
);

CREATE TABLE sync_checkpoints (
    scope TEXT PRIMARY KEY,
    chat_id TEXT,
    anchor_message_id TEXT,
    anchor_sent_at INTEGER NOT NULL DEFAULT 0,
    anchor_from_me INTEGER NOT NULL DEFAULT 0,
    category TEXT NOT NULL DEFAULT '',
    server_progress INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL,
    degraded INTEGER NOT NULL DEFAULT 0 CHECK (degraded IN (0,1))
);

CREATE INDEX chats_recent_idx
    ON chats(archived, last_message_at DESC, chat_id);
CREATE INDEX messages_page_idx
    ON messages(chat_id, sent_at DESC, message_id DESC);
CREATE INDEX messages_prune_idx
    ON messages(retained_body, sent_at, chat_id, message_id);
CREATE INDEX attachments_local_idx
    ON attachments(downloaded_at, local_bytes)
    WHERE local_path IS NOT NULL;
CREATE UNIQUE INDEX sync_checkpoint_chat_idx
    ON sync_checkpoints(chat_id)
    WHERE chat_id IS NOT NULL;
```

Reactions and receipts are deferred until supported; adding normalized tables requires a migration. `download_ref` is a maximum-4-KiB opaque value containing only fields required for explicit download. It is persisted without application encryption in the protected local cache database and therefore relies concretely on the 0700 parent directory, 0600 database/WAL/SHM modes, local-user threat model, and log/crash exclusion. If that protection is insufficient for the actual whatsmeow fields at the media milestone, persistence is deferred and downloads work only while an in-memory reference exists; no unspecified “encrypt if needed” design is permitted. The value is removed when its message is pruned or media becomes unavailable.

### Connection and transactions

- One writer connection is owned by the store-writer goroutine. Read queries use at most one separately configured read connection, preventing a large pool on old hardware.
- Batch maximum: 50 operations or 25 ms since the first queued operation, whichever comes first. Under live pressure the writer commits smaller batches rather than waiting for size.
- A transaction includes related chat/message/attachment upserts. It always upserts a placeholder `chats` row before inserting a message for an unknown chat, then later merges better chat metadata; foreign keys remain enabled. Pruning is limited to at most 500 row changes per transaction and yields between transactions.
- `busy_timeout` design default is 1,000 ms for writer and 250 ms for UI reads. Busy errors return to service; UI reads do not retry in a loop.

### WAL decision

Use WAL initially because one short writer and one UI reader should coexist without readers blocking writes. Configure `journal_mode=WAL`, `synchronous=NORMAL`, `foreign_keys=ON`, a modest negative `cache_size`, and `wal_autocheckpoint` chosen by page count after benchmarks. Risks are extra `-wal`/`-shm` files and temporary size beyond the main DB. Cache accounting therefore includes main DB, WAL, SHM, and explicit downloaded media. If old-filesystem testing shows WAL durability/locking problems, rollback is a configuration/migration boundary to rollback-journal mode; UI isolation still requires short transactions.

### Migrations and corruption

`PRAGMA user_version` selects ordered, idempotent migrations embedded in the binary. Startup takes a private backup only when a migration is destructive and space permits; otherwise it refuses safely. It never migrates the whatsmeow session database. Integrity failure renames neither file automatically: walite opens read-only if possible, displays recovery instructions, and requires explicit user action to rebuild the application cache. Session data remains separate.

### Pruning and size accounting

1. After history batches, delete bodies/attachments older than age cutoff, retaining only explicitly justified compact tombstones/anchors.
2. Per affected chat, use `ROW_NUMBER()` or a keyset subquery to remove bodies after newest 100; benchmark against a portable keyset loop if window functions are costly.
3. Enforce finite bodyless/tombstone/placeholder/contact/chat counts and replace the one-row-per-chat checkpoint; never retain an old message solely as an anchor.
4. Compute logical byte estimates for every table/index row, body, opaque download reference, and managed media file, with conservative fixed overhead stored in `app_meta`.
5. Stat the entire managed footprint: main DB, WAL, SHM, cache temp/backup files, and managed media. At 95% of 250 MiB begin proactive pruning/checkpoint; reserve space before a write; at the hard limit stop history acceptance and prune oldest eligible data in 500-row transactions.
6. Never delete whatsmeow session files. Managed explicit downloads count against the application cap; if a requested file would exceed it, ask the user to choose an external destination or prune.

No automatic full `VACUUM` runs during normal operation. Use incremental auto-vacuum only if benchmarks justify its write cost; otherwise expose an explicit offline maintenance command after closing UI/store. Passive WAL checkpoints occur after idle/threshold, never per message.

### Permissions and UI isolation

XDG private directories are created and verified 0700. Database, WAL/SHM where controllable, configuration, temporary downloads, and metadata files are 0600. Existing unsafe modes are tightened or startup fails with a clear error. All SQL work is behind store interfaces; UI receives pages/deltas only.

## 10. TUI design

### Layout and startup

```text
+ Chats ----------------+ Messages --------------------------------+
| selected / unread     | timestamp sender: text                   |
| ...                   | attachment: [not downloaded]             |
+-----------------------+------------------------------------------+
| compose input                         | online | sync 23% | cache |
+------------------------------------------------------------------+
```

Before sync, cached chats/messages appear immediately; otherwise the message pane says “No cached messages” and remains navigable. Status distinguishes offline, connecting, QR required, online, syncing, history paused/degraded, and cache/disk errors. Server progress is advisory and never displayed as archive completeness.

### Input behavior

- `Tab`/`Shift-Tab`: move focus; arrows or `j/k`: navigate; `Enter`: open chat.
- Compose typing does nothing externally. `Enter` sends only from focused compose; a configurable confirmation is required for multiline/pasted text in the first version.
- `PgUp` or `o`: load older cached page; a separate explicit `O` requests older history from WhatsApp after confirmation/status feedback.
- `d`: explicitly download selected attachment; never on selection or view.
- `r`: clear unread locally/send read action only when that feature is deliberately implemented; viewing alone does not silently send in milestone scope.
- `q` then confirmation, or `Ctrl-C`: graceful shutdown.

Narrow terminals below roughly 80 columns switch to one pane: chat list or message view with a back key. Very short terminals collapse help/status text. Unicode width comes from the chosen terminal library; invalid text is replaced with U+FFFD. Emoji rendering is terminal-dependent; fallback shows textual short labels for recognized message kinds and never assumes double-width correctness.

### Redraw strategy

The TUI owns a retained screen model. View updates mark dirty rows/regions. Input redraws compose/status only; new visible message redraws the affected message rows; chat changes redraw only affected chat rows. Resize permits one full redraw. Coalesce bursts for up to 16 ms, cap background sync rendering at 4 Hz, and never redraw once per historical message. Idle has no refresh ticker; timers exist only for cursor/status expiry and are cancellable.

### Library comparison

| Choice | Hardware cost | Maintainability | Testability | Assessment |
|---|---|---|---|---|
| tcell/tview | tcell is moderate; tview adds widget/layout behavior and can encourage broad redraw callbacks | convenient widgets, older API patterns seen in whatscli | widget tests possible but application queue behavior complicates deterministic rendering | viable, but less control over redraw/allocation policy |
| Bubble Tea | single message/update loop is conceptually clean | active ecosystem and composable model/view style | strong pure-update testing | string-based whole-view rendering and model allocations may cost more on the target; must benchmark before choosing |
| direct tcell | smallest Go abstraction among these and supports cell-level updates | more local layout/input code to maintain | deterministic screen simulation and dirty-region tests are feasible | best control over CPU, memory and redraw frequency |
| ncurses-style/cgo | mature and efficient C terminal model | adds C toolchain/cgo/platform complexity | harder hermetic Go tests | rejected for initial single-Go-binary simplicity |

**Decision:** direct tcell for the implementation milestone, subject to an offline prototype benchmark. This is a design choice, not a dependency addition in this milestone. It trades more small local code for explicit dirty-region control, a single terminal owner, and predictable old-hardware behavior. If the prototype cannot meet accessibility/testability needs, Bubble Tea is the fallback evaluated with identical benchmarks; tview is not selected merely because whatscli uses it.

## 11. Error model

```go
type ErrorKind uint8
const (
    ErrAuthRequired ErrorKind = iota
    ErrDisconnected
    ErrReconnecting
    ErrRateLimited
    ErrHistoryUnavailable
    ErrMalformedEvent
    ErrDatabaseBusy
    ErrDiskFull
    ErrCacheLimit
    ErrAttachmentUnavailable
    ErrCancelled
    ErrInternalInvariant
    ErrBusy
    ErrShuttingDown
)

type AppError struct {
    Kind      ErrorKind
    Operation string
    RetryAt   time.Time
    Cause     error // redacted before UI/log output
}
```

| Kind | UI | Retry | Logging | Fatality |
|---|---|---|---|---|
| auth required | persistent status/modal | user initiates QR | info, redacted | nonfatal |
| disconnected/reconnecting | persistent status | capped automatic reconnect | warn on transitions | nonfatal offline mode |
| rate limited | persistent until retry time | automatic only at known/conservative deadline | warn | nonfatal |
| history unavailable | transient then chat marker | no automatic retry | info/warn | nonfatal |
| malformed event | sync degradation counter | no item retry | warn without payload | nonfatal unless invariant breaks |
| database busy | transient for reads; persistent if sustained writes | one coordinator-scheduled bounded retry | warn | cache may degrade |
| disk full | persistent blocking cache banner | no loop; user action/recheck | error | cache writes fatal, UI/network optional |
| cache limit | transient during successful prune, persistent if stuck | bounded pruning | info/warn | history import stops first |
| attachment unavailable | persistent on item | explicit retry only | info | nonfatal |
| cancelled | quiet shutdown/progress clear | none | debug | expected |
| internal invariant | persistent fatal dialog | none | error with redacted stack | component/application fatal |
| busy | transient; compose remains | user may retry | debug/counter only | nonfatal |
| shutting down | transient/quiet during quit | none | debug | expected |

Errors are matched by kind, not strings. Wrapping preserves causes internally but UI/log rendering has an allowlist of safe fields.

## 12. Security and privacy

- Config: `${XDG_CONFIG_HOME:-~/.config}/walite/config.toml`; data: `${XDG_DATA_HOME:-~/.local/share}/walite/`; cache: `${XDG_CACHE_HOME:-~/.cache}/walite/` only if cache semantics allow deletion. Session DB belongs under data, separate from `walite-cache.db` and downloads.
- Directories are 0700; regular private files/databases/temp files are 0600. Symlink-sensitive creation uses `Lstat`/safe open flags where available.
- Session keys remain exclusively in whatsmeow's store. Walite never reads or logs their tables and never places session DBs in diagnostics.
- QR data is memory-only, rendered only in the terminal, cleared on expiry/success/cancel, never logged, copied, crash-reported, or written.
- Normal logs contain event kind, opaque short hash/counter, durations, queue depth, and error category only. No body, QR, phone/JID, session key, media key/path token, contact name, or raw protobuf.
- Crash reports are local and opt-in. Automatic upload is absent. Redacted stacks/config shape may be exported explicitly; databases and messages are excluded.
- Clipboard is disabled initially. A later explicit copy action must warn and copy only the selected rendered text; IDs/phone numbers are not copied by default and clipboard clearing cannot be guaranteed.
- Attachment names use a display-only original and a generated storage filename. `filepath.Base` alone is insufficient: reject separators/control characters, normalize length, prevent `..`, open beneath a verified directory, use random temp names, and verify the final path remains contained.
- `.gitignore` must exclude session/cache databases, `-wal`, `-shm`, media, QR/debug captures, logs, and generated personal config. Runtime also refuses known repository-relative data paths unless explicitly overridden with warning.
- Tests use synthetic IDs such as `chat-a`, names such as `Person A`, and generated content. No real phone numbers, QR strings, messages, session files, or downloaded media fixtures.

## 13. Configuration

Use a small TOML file because it is human-readable and supports durations/sizes clearly. CLI flags may override paths/log level for one run. Proposed defaults:

```toml
[retention]
messages_per_chat = 100
max_age = "2160h"          # 90 days
max_cache_mib = 250

[queues]
realtime = 128
realtime_kib = 2048
history_chunks = 4
history_chunk_records = 32
history_kib = 512
commands = 32
commands_kib = 512
live_writes = 128
live_writes_kib = 1024
history_writes = 256
history_writes_kib = 2048
view_updates = 64
view_updates_kib = 512

[database]
batch_size = 50
batch_interval = "25ms"
busy_timeout = "1s"

[media]
prefetch = false
downloads_dir = ""         # resolved under XDG data by default

[notifications]
desktop = false
terminal_bell = false
show_preview = false

[logging]
level = "warn"
```

Safe user ranges: per-chat 20–500, age 1–365 days, cache 32–2048 MiB, batch 1–200, interval 5–100 ms, realtime 32–512, and realtime byte budget 512–4096 KiB. Other entry and byte budgets have similarly narrow caps. The 32-KiB event/command maximum, 16-KiB retained-body maximum, 128-KiB history-chunk maximum, metadata/ON_DEMAND/bulk payload-reference capacities (2/1/1), priority order, metadata row caps, goroutine counts, and media-prefetch-off rule are not user-configurable initially because increasing them can invalidate responsiveness/memory guarantees. “Unlimited” values are rejected; zero never means unlimited. Notification previews remain fixed off until a privacy-reviewed feature exists.

Configuration validation happens before opening network/session state. Reload is a non-goal initially; restart applies changes deterministically.

## 14. Testing strategy

All automated tests use fake clocks, fake WA sources, in-memory/temporary stores, simulated terminal screens, and synthetic content. No account/network authentication occurs.

### Required offline tests

- Retention: exactly newest 100 per chat, 90-day boundary, mixed chats, tombstone/anchor bounds, and total-size pruning order.
- Priority: preloaded history plus injected live event proves live processing precedes the next history quantum and writer checks live between history batches.
- Saturation: each queue reaches capacity; documented drop/block/coalesce result occurs; no goroutine/slice growth follows.
- Byte bounds: fill queues with maximum-size values; submit multi-megabyte strings/metadata/download references and prove normalization truncates/rejects before enqueue, byte permits never exceed budgets, replaced mailbox values release permits, and RSS/allocations do not scale with source-string size beyond whatsmeow/fake-owned input.
- History-writer isolation: hold a fake `historyWriteQ` path full indefinitely, inject a real-time event, and deterministically assert the coordinator handles and publishes it before releasing the history writer. This test uses synchronization barriers, not timing sleeps.
- History admission: independent metadata slots remain available while bulk is full; ON_DEMAND is admitted to its own slot when the confirmed enum is present; unknown categories degrade/drop; total retained payload references never exceed four.
- Dedup/order: duplicate live/history/outgoing echoes; equal timestamps; edits/deletes/reactions/receipts before base messages; deterministic page cursors.
- Cancellation: at connect, QR, enqueue, history record, transaction, retry timer, download, and TUI render. All goroutines terminate and temp files disappear.
- Store: 50-row/25-ms batches with fake clock, transaction rollback, busy timeout, disk-full injected filesystem/driver errors, migration and corruption-safe startup.
- Huge HistorySync: fake adapter cursor yields 100,000 records with old/large payloads; bounded record/byte chunks, memory plateau, real-time precedence, cancellation, category-aware admission/shedding, and no full application copy.
- TUI: interactive first frame before history source releases; dirty-region update counts; resize/narrow fallback; burst coalescing; Unicode-width fallback.
- Media: selecting/viewing never invokes downloader; only explicit command does; safe filenames/path containment and 0600 mode.
- Security: directory 0700, files 0600, unsafe existing mode correction/failure, logs contain none of seeded secret/message/ID strings.
- Race/leak: repeated start/stop, reconnect, saturation, writer failure under `go test -race`; goroutine counts return to baseline with tolerance for runtime/library-free fakes.

### Benchmarks

- Stream 100,000 synthetic history messages through policy/store fake; report allocations, peak queue occupancy, retained count and wall time.
- Adapter enqueue latency for available/full real-time and history queues.
- SQLite batch throughput for 1/25/50 operations and WAL checkpoint impact on target-like hardware.
- Per-chat and global pruning at 10k/100k rows.
- TUI single-message, 32-history-record, chat reorder, compose keystroke and resize update cost/cells touched.

Benchmarks guard regressions but do not assert desktop-specific nanoseconds in CI. A target-machine benchmark profile records results separately.

## 15. Performance budget

These are **DESIGN GOALS**, not confirmed performance:

| Measure | Goal on target Core 2 Duo |
|---|---:|
| process start to interactive offline UI | ≤ 750 ms warm cache; ≤ 1.5 s cold empty cache |
| whatsmeow callback p95 / hard diagnostic threshold | ≤ 1 ms / 5 ms, excluding library work before callback |
| real-time nonblocking enqueue p95 | ≤ 100 µs |
| application-added variable queue memory | ≤ 8 MiB hard byte-budget target, excluding at most four admitted server payloads |
| idle CPU while connected, no terminal input | < 1% of one core averaged over 60 s |
| background sync redraw rate | ≤ 4 frames/s; idle 0 frames/s |
| compose/input response | p95 ≤ 50 ms |
| SQLite transaction p95 | ≤ 25 ms; split/yield above 50 ms |
| live message visible after callback | p95 ≤ 150 ms absent disk failure |
| graceful shutdown | ≤ 2 s normal, 5 s hard deadline |
| managed application footprint | ≤ 250 MiB total; reserve 10% of that cap internally for WAL/temp headroom |

Because decoded HistorySync size is server-controlled and **UNKNOWN**, no honest total-process memory ceiling can be guaranteed by architecture alone. Acceptance records peak RSS during synthetic and later opt-in real-account testing without turning observations into a protocol guarantee.

## 16. Milestones

Each milestone is a review/rollback boundary. Every code milestone runs `gofmt`, `go vet ./...`, `go test ./...`, and `go test -race ./...` unless a documented toolchain blocker exists.

### Milestone 1A — dependency-free offline core

- Packages/files: `internal/model`; consumer-owned interfaces in `internal/service`; configuration value validation in `internal/config`; fake event source; in-memory store implementing service interfaces structurally; pure `internal/syncpolicy`; bounded entry-and-byte priority mechanism; cancellation; a plain textual fake-data demo; offline tests.
- Acceptance: package import-direction test/review proves the documented acyclic graph; demo starts without network; live precedence and every saturation/byte-budget policy pass; full history-write path cannot delay a barrier-synchronized live event; category-aware admission retains at most four fake payloads; 100/90-day/count/metadata retention; huge strings cannot exceed queue budgets; deterministic fake-clock cancellation; zero leaked goroutines; no media call.
- Required validation: `gofmt`, `go vet ./...`, `go test ./...`, and `go test -race ./...`; offline allocation/priority benchmarks.
- Non-goals: tcell or any TUI dependency, SQLite, whatsmeow, QR, authentication, real send, personal data.
- Rollback: remove the 1A package tree/text demo; architecture docs remain.

### Milestone 1B — offline direct-tcell prototype

- Packages/files: `internal/tui` direct-tcell prototype, fake-data wiring in `cmd/walite`, simulated-screen tests and target benchmark harness. It imports only `internal/model` and `internal/service`; all data remains fake.
- Acceptance: measure/report cold and warm first-frame time, cells/regions redrawn for input/live/history bursts, idle redraw count, compose latency, Unicode fallback, and one-pane narrow-terminal behavior; service starts before fake history completes; cancellation restores the terminal.
- Required validation: `gofmt`, `go vet ./...`, `go test ./...`, `go test -race ./...`, plus documented target-machine measurements.
- Non-goals: SQLite, whatsmeow, QR/authentication, real sending/media, personal data, final visual polish.
- Rollback: remove `internal/tui` and tcell wiring/dependency; retain the dependency-free 1A textual demo and core.

### Milestone 2 — SQLite cache

- Packages: concrete `internal/store` implementation importing `internal/model`, migrations and filesystem abstractions; `cmd/walite` wiring to service-owned interfaces.
- Acceptance: schema/indexes, permissions, batching, pagination, pruning, busy/disk-full/corruption tests and benchmarks.
- Non-goals: session DB, network, WhatsApp.
- Rollback: select in-memory implementation; remove disposable application cache only.

### Milestone 3 — whatsmeow authentication and connection

- Packages: `internal/wa` importing `internal/model`, `cmd/walite` structural-interface wiring, config paths; no `internal/wa -> internal/service` import.
- Acceptance: source-confirmed API adapter compiles; fake tests for QR lifecycle/reconnect; a separate manual opt-in smoke procedure, never automated.
- Non-goals: message import/send/media.
- Rollback: disable WA implementation and retain offline cache/UI.

### Milestone 4 — real-time text events

- Packages: WA normalization and service live path.
- Acceptance: minimal callback copy benchmark, duplicate/out-of-order tests, saturation degradation, no callback SQL/UI.
- Non-goals: HistorySync retention, sending.
- Rollback: ignore message events while connection/auth remains.

### Milestone 5 — controlled HistorySync

- Packages: WA history cursor/worker, sync policy, store writer, sync UI.
- Acceptance: internal whatsmeow behavior reconfirmed at pinned revision; 100k synthetic test; metadata/ON_DEMAND/bulk admission retains at most 2/1/1 payload references; record and byte chunk bounds; full history-writer isolation from live processing; cancellation/repeated batches/disk full.
- Non-goals: completeness claims, on-demand history.
- Rollback: shed all application HistorySync events while preserving whatsmeow session processing.

### Milestone 6 — interactive sending

- Packages: service commands, WA send adapter, compose UI.
- Acceptance: explicit human action, ambiguous-timeout behavior, outgoing echo dedup, no automation API.
- Non-goals: bulk/file send, scheduled send.
- Rollback: disable send command; receive/cache remains.

### Milestone 7 — explicit media download

- Packages: WA downloader, service safe-file flow, attachment UI.
- Acceptance: explicit-only invocation, traversal/mode/cancel/unavailable tests, cache accounting.
- Non-goals: prefetch, upload, thumbnails.
- Rollback: hide download action; metadata remains.

### Milestone 8 — explicit older history

- Packages: anchor query, WA history request, request status.
- Acceptance: exact required fields from current source, no polling, timeout/unavailable/no-access tests, duplicate merge.
- Non-goals: success/completeness guarantee or automatic paging from server.
- Rollback: retain cached paging and hide remote request.

### Milestone 9 — packaging and performance tuning

- Packages: build/release docs, configuration help, target benchmarks.
- Acceptance: linux/amd64 binary, clean XDG install, old-MacBook budgets measured, licence notices and safe upgrade/rollback.
- Non-goals: cross-platform promises not tested, multi-account.
- Rollback: retain prior binary/cache schema; migrations remain backward-safe or provide explicit cache rebuild.

## 17. Open questions and risks

### Carried-forward UNKNOWN items

- Whether a companion can disable initial history transfer.
- Whether initial history can be constrained to an exact recent-message count, age, or size, including semantics of `initialSyncMaxMessagesPerChat` and other HistorySyncConfig fields.
- Whether initial HistorySync categories can be selected/suppressed, their delivery order, and which category is authoritative for each metadata type.
- Whether future whatsmeow revisions continue to expose a reliable known enum and immutable payload lifetime for category-aware admission; fallback is drop/degrade, not deep copy.
- Typical/worst decoded blob size and concurrent event arrival distribution.
- Global ordering across independent event-dispatch goroutines.
- ON_DEMAND delivery, exact count, access, correlation, retry timing and completeness for direct/group/LID chats.
- Consequences of suppressing manual-history receipts; architecture does not suppress them.
- Complete metadata needed for future edits, revocations, replies, polls and anchors when bodies are discarded.
- Authoritative conflict/version semantics for all edits, deletes, reactions and receipts.
- A proven cross-platform 250 MB accounting/pruning method including WAL, SHM and external download destinations.
- GitHub archive flags and complete transitive licence posture of reference clients; neither affects chosen upstream dependency but remains part of provenance.

### Risks

- WhatsApp is unofficial for this client and may restrict accounts or change behavior. Architecture cannot eliminate this; warn users and avoid automation.
- Protocol/server changes may require urgent whatsmeow upgrades; pin, inspect source, test adapter, and isolate changes in `internal/wa`.
- A server-controlled blob can cause high peak RSS before walite sees it. Fixed application queues prevent amplification but cannot bound library allocation.
- On-demand history may fail or be denied. UI must present best effort, never promise archive navigation.
- SQLite/WAL can temporarily exceed logical limits and disk full can make outgoing persistence ambiguous. Preserve session separation and expose persistent errors.
- Old hardware may miss proposed budgets. Measure each milestone and simplify rendering/batching rather than adding workers.
- Direct tcell increases local UI code. Keep layout/render pure and small; retain Bubble Tea fallback only after comparable benchmarks.
- Dropping an application event under saturation can make the local cache incomplete. Persistent degradation state and idempotent later merges make this visible, not solved.

## Architecture decision records

### ADR-001: whatsmeow rather than implementing the protocol

- Context: WhatsApp protocol changes and cryptographic/session correctness are specialized; current source is maintained.
- Decision: depend on `go.mau.fi/whatsmeow`; isolate it in `internal/wa`; inspect checked-out source before relying on behavior.
- Consequences: faster safe implementation and upstream compatibility work; MPL-2.0/file and GPL-linked dependency obligations require release review; API changes affect adapter.
- Rejected alternatives: direct protocol implementation, Rhymen/go-whatsapp, browser automation/embedding.
- Uncertainties: future API/protocol viability and account policy.

### ADR-002: one process initially

- Context: target hardware and deployment favor minimal IPC and memory overhead.
- Decision: one executable/process with explicitly owned goroutines.
- Consequences: simple install/lifecycle; a component panic can affect the process, so boundaries recover/cancel carefully.
- Rejected alternatives: daemon plus UI, helper processes, browser service.
- Uncertainties: whether future crash isolation justifies a daemon.

### ADR-003: bounded local cache rather than full archive

- Context: initial history is server-controlled and reference clients demonstrate unbounded retention risks.
- Decision: 100 bodies/chat, 90 days, and a 250 MiB all-managed-files cap, plus explicit per-chat/global metadata limits and one anchor row per chat.
- Consequences: predictable application disk/memory; old messages may be unavailable.
- Rejected alternatives: full archive, memory-only unlimited store, indexing all supplied history.
- Uncertainties: exact compact metadata needed and filesystem-accurate SQLite/WAL accounting.

### ADR-004: separate real-time and history priorities

- Context: callbacks may be concurrent and historical work can be enormous.
- Decision: bounded real-time/history lanes with weighted byte budgets; a separate history-ingestion worker prevents a full history write path from blocking the coordinator; fixed admission slots separate metadata-only (2), confirmed ON_DEMAND (1), and other message-heavy (1) payloads, with unknown categories dropped/degraded.
- Consequences: responsive live traffic and explicit overload behavior; local history can be incomplete.
- Rejected alternatives: one FIFO, unbounded overflow, goroutine per event, synchronous handler processing.
- Uncertainties: real workload distributions, best byte/capacity values, category correlation, and future classification reliability.

### ADR-005: no media prefetch

- Context: media dominates bandwidth/disk and reference automatic prefetch produced unbounded work.
- Decision: metadata only until an explicit user download.
- Consequences: low background cost/privacy exposure; first open has latency and old media may expire.
- Rejected alternatives: history/live prefetch, thumbnails on selection.
- Uncertainties: media availability duration is server-controlled.

### ADR-006: SQLite cache separate from session database

- Context: whatsmeow owns cryptographic/session schema; walite needs independently disposable bounded data.
- Decision: distinct files, connections, migrations, permissions, and pruning.
- Consequences: session survives cache rebuild; some duplicated metadata and two DB files.
- Rejected alternatives: adding tables to/attaching whatsmeow DB, memory-only production cache.
- Uncertainties: final driver and WAL behavior on target filesystem are milestone decisions.

### ADR-007: offline-first testing

- Context: tests must be deterministic, safe, and account-free.
- Decision: fake event sources/clocks/stores/filesystems/terminal; network only in explicit manual procedures.
- Consequences: concurrency/failure paths are reproducible; server integration remains partly manual and UNKNOWN.
- Rejected alternatives: CI authentication, recorded personal sessions, mandatory live integration tests.
- Uncertainties: fakes cannot predict every server behavior.

### ADR-008: selected TUI library

- Context: old hardware needs controlled redraws; maintainability/testability still matter.
- Decision: prototype and implement with direct tcell, using dirty regions and one terminal goroutine.
- Consequences: precise cost/control and no cgo; more local widget/layout code than tview/Bubble Tea.
- Rejected alternatives: tview for its broader widget/redraw behavior, Bubble Tea initially due whole-view/allocation concerns, ncurses-style cgo due build/test complexity.
- Uncertainties: target benchmarks may show Bubble Tea meets budgets with better maintainability; ADR may be revised only with measured evidence.
