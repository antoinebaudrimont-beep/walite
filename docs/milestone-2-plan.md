# Milestone 2 Plan

Milestone 2 prepares and installs the offline application cache before any
WhatsApp transport or authentication work.

## Sequence

1. **2.1 — application wiring and dependency cleanup (complete):** load UI
   configuration in `cmd/walite`, inject plain TUI options, and own the existing
   offline service/TUI lifecycle from the production composition root.
2. **2.2A — SQLite cache foundation (complete):** add the
   concrete driver, secure cache path, connection lifecycle, schema-v1
   migration, corruption/version gates, bounded message round trips, and
   foundation benchmarks without switching the production store.
3. **2.2B1 — bounded SQLite batch writer (complete):** add one
   serialized writer, bounded request admission, 50-write/25-ms transactions,
   commit acknowledgements, controlled busy errors, and draining shutdown.
4. **2.2B2 — keyset pagination (complete):** add bounded,
   indexed message-page reads and deterministic composite cursors without
   OFFSET queries.
5. **2.2B3 — contact and chat ingestion (complete):** make
   contacts and full chat metadata first-class cache entities, upgrade
   placeholders, and maintain idempotent activity/unread state.
6. **2.2C — pruning and cache budget (complete):** retain the
   newest and recent message bodies, account for physical SQLite files, enforce
   one bounded pressure cycle, and expose controlled degradation states without
   switching production storage.
7. **2.3 — prototype JSON chat persistence retirement (complete):** remove TUI chat/message serialization, ignore obsolete prototype
   files, and rebuild the temporary synthetic state in memory on every start.
8. **2.4A — service-backed initial TUI snapshot (complete):**
   replace TUI-owned demo construction with one bounded application snapshot
   after service readiness.
9. **2.4B0 — committed live-event contract (complete):** add
   a distinct bounded, lossless stream for newly committed realtime messages
   and their authoritative unread/activity metadata.
10. **2.4B1 — live existing-chat TUI updates (current, ready for review):**
    consume committed live events for chats already present in the initial
    snapshot.
11. **2.4B2 — dynamic new-chat insertion (pending):** add newly discovered
    chats without evicting existing UI state implicitly.
12. **2.4B3 — outgoing service path (pending):** route local sends through the
    application service and committed-event path.
13. **2.5 — offline integration validation:** verify startup ordering,
   cancellation, store failures, retention, pagination, race safety, and target
   performance with only offline fakes.
14. **2.6 — transport readiness boundary:** only after the offline cache and UI
   adapter pass may WhatsApp transport and authentication work begin.

## Remaining temporary deviations after 2.1

- Emoji preference path and file I/O remain in `internal/tui`.
- The production offline fixture remains synthetic, but it is now owned by the
  composition root and reaches the TUI through the store/service application
  boundary.
- Initial chat/message data is service-backed, and committed events update
  chats already present in that bounded snapshot. Unknown-chat insertion
  remains deferred to 2.4B2.
- Changed terminal events still trigger a measured full-screen redraw.

## Increment 2.2A foundation

- Driver: `modernc.org/sqlite` v1.45.0. It is a pure-Go `database/sql`
  implementation, supports the Linux/amd64 target and required SQLite pragmas,
  and is the newest checked release compatible with the module's Go 1.24 line.
- Location: `${XDG_CACHE_HOME:-~/.cache}/walite/walite-cache.db`, resolved by
  `os.UserCacheDir`. Only absolute paths are accepted by the concrete store.
- Protection: the cache directory is verified and tightened to 0700; the main
  database and present WAL/SHM files are verified as regular non-symlink files
  and tightened to 0600. Unexpected path types fail without rebuilding data.
- Connection: one pooled connection, `foreign_keys=ON`, `journal_mode=WAL`,
  `synchronous=NORMAL`, a 1,000 ms default busy timeout, and a modest 2 MiB
  negative `cache_size`. No mmap, vacuum, or checkpoint tuning is introduced.
- Schema: transactional `user_version` 0-to-1 migration containing the six
  architecture tables and all five proposed indexes. Future versions and
  failed integrity checks are rejected without deleting, renaming, or
  recreating the database.
- Timestamps: every application-cache timestamp is a signed SQLite INTEGER in
  Unix milliseconds and message reads reconstruct deterministic UTC values.
- Store boundary: chat metadata insertion, bounded batch UPSERT, automatic
  placeholder-chat creation, and composite-ID message lookup are available for
  tests and later composition. SQLite is not yet wired into `cmd/walite` and
  does not yet claim the complete `service.MessageStore` interface.
- Deferred from this foundation to later increments: pagination, pruning,
  cache-size accounting, batching policy, JSON migration, TUI adaptation,
  WhatsApp/session storage, networking, and WAL checkpoint tuning.

## Increment 2.2B1 bounded batch writer

- Ownership: one SQLite writer goroutine, one reusable timer, the existing
  single-connection database pool, and at most one active transaction. The
  idle writer blocks on channels; it has no ticker, polling loop, worker pool,
  or per-write goroutine.
- Bounds: a fixed 64-request channel feeds fixed active storage. A transaction
  contains at most 50 logical chat/message writes and is flushed immediately at
  50, when its oldest write reaches 25 ms, or during shutdown. Requests remain
  individually bounded by `model.WriteBatch`; the queue never expands.
- Admission: a full queue applies cancellable backpressure without allocating
  overflow storage or dropping a request. Cancellation before enqueue rejects
  the write. After enqueue, cancellation ends only that caller's wait; the
  writer still commits or rolls back the accepted request and uses a buffered,
  one-result acknowledgement so it cannot become blocked on an absent caller.
- Results: callers receive success only after transaction commit. Any SQL
  failure rolls back the whole active batch and gives every included request a
  controlled error. SQLite BUSY/LOCKED codes map to `ErrBusy` without custom
  retry loops; a later healthy batch can proceed.
- Shutdown: `Close` first stops new admission, waits for any in-progress queue
  admissions, drains and flushes every accepted request, publishes their
  acknowledgements, joins the writer, and finally closes SQLite. Repeated close
  is safe and post-close writes are rejected.
- Initial Core 2 Duo benchmark: end-to-end batches measured 27.08 ms for one
  write (36.93 rows/s), 31.15 ms for ten writes (321.1 rows/s), and 21.86 ms
  for 50 writes (2,288 rows/s). The first two include the intentional 25-ms
  batch-age wait; 50 writes flush immediately.
- Transaction-only latency harness, 20 samples per size: observed p95 was
  0.595 ms for one write, 6.765 ms for ten, and 17.195 ms for 50. These are
  development measurements, not CI thresholds.
- Still deferred from this writer increment: keyset pagination,
  retention/pruning, cache accounting, JSON migration, production wiring, TUI
  adaptation, WhatsApp/session storage, and networking.

## Increment 2.2B2 keyset pagination

- Contract: `SQLiteStore.Page` implements the existing service-owned message
  page shape. The zero `model.Cursor` means both “start from newest” on input
  and “no continuation” on output; no second cursor or page abstraction was
  added.
- Bounds: non-positive limits default to 50 and values above 100 clamp to 100.
  Each query requests only one lookahead row, so the application decodes at
  most 101 rows and returns at most 100. Empty and unknown chats return a
  non-nil empty page without creating placeholder metadata.
- Order: pages use `sent_at DESC, message_id DESC`, with an exclusive
  `(sent_at, message_id)` continuation boundary. Message ID is only the
  deterministic tie-breaker for equal timestamps; it does not imply causal or
  insertion order.
- SQL: first and continuation pages are parameterized keyset queries using
  `messages_page_idx`. There is no OFFSET path. Tests inspect both SQL texts and
  `EXPLAIN QUERY PLAN` output.
- Concurrent insertion semantics: a row inserted newer than a returned cursor
  is not revisited by that cursor's next page. A row inserted older than the
  cursor can appear on a later page. Pagination does not claim snapshot
  isolation across calls.
- Integration: reads retain the existing WAL configuration and one-connection
  pool and work after multiple bounded writer transactions. No writer,
  production wiring, JSON persistence, TUI, or pruning behavior changed.
- Coverage: deterministic tests traverse exactly 257 messages at page sizes 1,
  10, 32, 50, and 100; exercise 60 equal-timestamp messages inserted out of
  order; preserve ASCII, accented, CJK, complex emoji, direction, timestamps,
  and bodyless records; verify cancellation, bounds, index use, mutation
  behavior, and absence of OFFSET.
- Core 2 Duo benchmark over one 5,000-message chat, 100 returned messages per
  operation: first page 1.488 ms (51,696 B/op, 1,739 allocs/op), middle page
  1.946 ms (51,896 B/op, 1,744 allocs/op), and old page 3.000 ms (50,664 B/op,
  1,641 allocs/op). Setup was excluded. The observed cost increase for older
  cursors is recorded evidence, not a CI timing threshold.
- Still deferred: retention/pruning, cache accounting, disk-full behavior, JSON
  migration, production wiring, TUI adaptation, WhatsApp/session storage,
  media, networking, and WAL checkpoint tuning.

## Increment 2.2B3 contact and chat ingestion

- Domain values: bounded immutable contacts use opaque `model.ContactID`
  values with no phone-number assumptions. Chat values now carry optional
  contact linkage, group status, activity, unread count, muted/archived flags,
  metadata time, ingest sequence, and placeholder status.
- Writer ownership: `UpsertContact` and the existing `EnsureChat` each submit
  one logical operation to the existing single writer. Queue capacity 64,
  50-operation transactions, the 25-ms batch age, commit acknowledgements, and
  draining shutdown are unchanged; no second writer or read pool was added.
- Ordering: contact and chat metadata use `(ingest_seq, updated_at)` to prevent
  deterministically older metadata from replacing newer known values. Empty
  names and absent optional contact links do not erase known values. A real
  chat event always upgrades an existing placeholder while preserving its
  message activity and unread count.
- New-chat lifecycle: both metadata-first (`contact -> chat -> message`) and
  message-first (`message -> placeholder -> contact -> chat`) ordering converge
  on one contact, one non-placeholder chat, and the original message. Groups
  store a NULL contact link and require no contact or membership records.
- Message bookkeeping: message identity remains `(chat_id, message_id)`.
  `last_message_at` advances with `MAX(existing, incoming)` so delayed messages
  cannot move activity backwards. A newly inserted, non-`from_me` real-time
  message increments unread once; duplicates, local messages, and history
  writes do not. The counter saturates at the model's `uint32` bound. Message,
  placeholder/activity, and unread changes share one transaction and roll back
  together.
- Reads: small `Contact` and `Chat` lookups support deterministic cache tests
  and a future adapter without adding chat-list pagination. Existing message
  keyset pagination and its cursor/order semantics are unchanged.
- Core 2 Duo end-to-end benchmark: a new contact, new chat, and first message
  took 83.385 ms/op (8,952 B/op, 132 allocs/op); an incoming message for an
  existing chat took 26.725 ms/op (3,438 B/op, 58 allocs/op). These include the
  intentional 25-ms writer batch window per sequential public call and are not
  CI thresholds.
- Still deferred: contact/chat list pagination, group membership, read receipt
  semantics, retention/pruning, cache accounting, disk-full behavior, JSON
  migration, production wiring, TUI adaptation, WhatsApp/session storage,
  media, networking, and WAL checkpoint tuning.

## Increment 2.2C retention, cache budget, and degradation

- Body-retention rule: a body is protected when it belongs to the newest 100
  messages in its chat, ordered by `(sent_at DESC, message_id DESC)`, or when
  its `sent_at` is at or after the injected `now - 90 days` cutoff. The cutoff
  is inclusive on the retained side. A body is eligible only when it is outside
  both protections.
- Prune semantics: `SQLiteStore.Prune` runs one explicit transaction and
  changes no more than 500 eligible bodies. It sets `body=NULL`,
  `body_bytes=0`, and `retained_body=0`; it does not delete message rows or
  change composite identity, timestamps, direction, activity, unread counts,
  contacts, chats, attachments, or paging order. Its content-free result
  reports bodies dropped and logical body bytes freed. Repeated calls continue
  bounded work when more than 500 bodies are eligible.
- Time and failure behavior: callers inject `now`; there is no wall-clock read
  in the pruning decision. Cancellation before commit and SQL failures roll
  back the transaction. SQLite BUSY/LOCKED remains `ErrBusy`; SQLite FULL maps
  to the content-free `ErrDiskFull`. A narrow internal hook makes these driver
  outcomes deterministic in tests without changing the public runtime API.
- Transaction ownership: pruning and checkpoints use a context-cancellable
  maintenance gate shared with the existing writer. Pruning is not submitted
  as a 50-operation writer batch. Queue capacity 64, batch limit 50, maximum
  age 25 ms, commit acknowledgements, and draining close behavior are
  unchanged; accepted writes wait boundedly while maintenance owns SQLite and
  resume afterward.
- Budget: the production physical cache budget is the named 250 MiB
  `DefaultSQLiteCacheBudgetBytes` constant. Tests can override it only through
  package-private options. `CacheSizeBytes` sums the main database, `-wal`, and
  `-shm` using filesystem metadata; missing WAL/SHM files count as zero and an
  absent or unsafe main file is a controlled error. External attachment files
  are not added because the requested budget is explicitly the three SQLite
  files.
- Pressure cycle: `EnforceCacheBudget` first measures physical size. When over
  budget, it performs one bounded prune transaction, then one serialized
  `PRAGMA wal_checkpoint(TRUNCATE)`, and remeasures. TRUNCATE was selected
  because the maintenance gate and one-connection pool prevent a concurrent
  walite transaction; it is invoked only during explicit pressure maintenance,
  not on every write. Logical pruning commits before checkpointing, so a BUSY
  or failed checkpoint is reported without undoing the valid body-retention
  result.
- Degradation: normal operation reports `CacheNormal`; a cache still above
  budget after a valid cycle reports `CachePressure` and `ErrCachePressure`;
  SQLite FULL reports `CacheWriteUnavailable`. Pressure never clears a body
  protected by the 100-message/90-day union, even when the physical file cannot
  reach the budget. Previously committed data remains readable after a failed
  write, and a later healthy writer transaction remains possible and clears
  the write-unavailable state without clearing an unrelated pressure state.
- Metadata scope: schema-v1's unique checkpoint keys continue to prevent
  duplicate per-scope/per-chat checkpoint rows, and this increment introduces
  no new auxiliary table or unbounded queue. Global chat/contact deletion and
  bodyless-message metadata caps remain a later policy decision; body expiry
  alone never deletes identity rows in 2.2C.
- Coverage: deterministic tests exercise exact 90-day boundaries, more than
  100 recent messages, mixed chats, ASCII/accented/emoji byte accounting,
  pagination before and after pruning, metadata preservation, the 500-body
  limit, idempotence, cancellation, rollback, BUSY, writer serialization,
  DB/WAL/SHM accounting, missing optional files, tiny-budget pressure,
  checkpoint failure, disk-full recovery, and a 560-message multi-chat stress
  case with duplicate ingestion.
- Core 2 Duo benchmarks: one 1,000-message chat took 40.455 ms per bounded
  pruning pass (2,764 B/op, 48 allocs/op); five chats totaling 3,000 messages
  took 69.520 ms (2,774 B/op, 48 allocs/op). Each pass dropped the bounded 500
  bodies. Physical DB/WAL/SHM accounting took 12.966 µs (944 B/op, 8 allocs/op).
  Fixture restoration and ingestion were excluded, and these observations are
  not CI timing thresholds.
- Still deferred: production SQLite composition,
  contact/chat list pagination, global metadata pruning, TUI adaptation,
  WhatsApp/session storage, media download policy, networking, and sync.

## Increment 2.3 prototype JSON persistence retirement

- Previous file and schema: `~/.local/share/walite/state.json` used version 1
  with top-level `version`, `selectedChat`, `nextMessageId`, `nextActivity`, and
  `chats`. Each chat stored `title`, `unreadCount`, `activity`, and `messages`;
  each message stored `id`, `timestamp`, `text`, and optional `replyToId` plus
  `hasReply`.
- Previous field ownership: `selectedChat` was the only UI-only field, but it
  was an unstable array index. Chat titles, unread counts, activity order,
  message IDs/text/timestamps/reply relationships, and the next-ID/activity
  counters were prototype domain data. No composer, cursor, input mode,
  viewport `scrollOffset`, `unreadBoundary`, `replySelect`, active
  `replyTarget`, emoji-popup, settings-popup, terminal-size, search/filter, or
  notification state was serialized. No preference field was present.
- Decision: retire `state.json` without replacement. Persisting only an
  unstable synthetic-list index was not useful enough to justify a new
  `ui-state.json`; selected chat therefore resets to the first current demo
  chat on restart. The obsolete file is neither opened, parsed, rewritten, nor
  deleted, so valid, malformed, oversized, and unknown-field variants cannot
  break startup. The compatibility read is zero bytes and needs no size cap.
- Runtime behavior: the current four-chat synthetic data remains available for
  the offline prototype but is freshly constructed in memory on every TUI run.
  Chat selection, unread mutations, activity ordering, local messages, and
  reply relationships are never persisted. Old synthetic history is
  deliberately not imported into SQLite or any other store.
- Remaining TUI persistence: `~/.config/walite/preferences.json` remains
  independent and continues to own only the emoji MRU. Configuration remains
  owned by `internal/config`; neither is merged into SQLite.
- Boundary proof: `internal/tui` imports neither `internal/store` nor
  `internal/config`. This increment does not open SQLite, add an importer,
  alter schema v1, change `cmd/walite`, or introduce the service-to-TUI data
  adapter. SQLite production composition and real snapshots remain 2.4 work.
- Coverage: a full version-1 fixture with chats, messages, unread/activity,
  reply data, selection, and an unknown field is ignored and left byte-for-byte
  unchanged. Truncated and greater-than-64-KiB obsolete files are also ignored.
  A restart test mutates selection and messages, verifies no `state.json` or
  replacement UI-state file is created, and confirms the next run starts with
  fresh demo state. Existing TUI interaction tests continue to cover compose,
  reply, scrolling, emoji, unread separator, timestamps, resize, and terminal
  restoration.

## Increment 2.4A service-backed initial snapshot

- Startup order is configuration load, service construction and readiness,
  bounded initial snapshot loading, then TUI initialization. Later service
  updates continue to be drained; no live update loop is introduced here.
- `cmd/walite` owns the adapter. It requests at most four chats and the newest
  32 messages per chat, sorts chats by activity descending with chat ID as the
  deterministic tie-break, and converts store pages from newest-first to the
  TUI's oldest-first rendering order.
- `internal/tui` accepts plain immutable-by-convention DTOs with stable string
  IDs, chat title/group/unread/activity metadata, and message timestamp,
  direction, retained-body state, and text. It copies those slices into its
  fixed working set before screen initialization and imports no model, service,
  store, configuration, SQL, or SQLite package.
- The useful four-chat offline fixture moved to `cmd/walite` and is seeded
  through `store.Memory`; the TUI no longer constructs production chat or
  message demo data. The SQLite production switch remains deferred because it
  would require a bounded chat-list query not present in this increment.
- Empty, one-chat, multi-chat, newly known contact, group, Unicode, bodyless,
  direction, stable-ID/reply, deterministic equal-time ordering, readiness,
  and large-history bounds are covered. Local compose/send and in-session
  unread clearing remain an intentionally in-memory overlay until later
  service command/update work.

## Increment 2.4B0 committed live-event contract

- `model.Update` remains the lifecycle/progress/status contract. Its fixed
  per-kind mailbox deliberately replaces older values, so it is unsuitable for
  distinct-message delivery; the existing `UpdateLive` admission signal is
  retained temporarily for compatibility but is not a committed-message
  acknowledgement.
- `service.Core.LiveEvents` is a separate receive-only stream with a fixed
  capacity of 64. It contains one immutable `model.LiveEvent` for each newly
  inserted realtime message and never coalesces distinct committed messages.
- The serialized writer calls the store's atomic `WriteRealtime` operation and
  publishes only the bounded result returned after that operation succeeds.
  Persistence failure produces no event. Storage has returned—and therefore no
  transaction is open—before a full live channel can apply backpressure.
- Events follow successful writer commit order. Messages returned by one
  commit retain request order; this is not a timestamp-order guarantee. The
  single writer is the only producer, and no per-event goroutines or overflow
  slices are used.
- Each event carries the committed immutable message plus the authoritative
  unread count and last-message activity after that logical insert. Incoming
  realtime messages increment unread, `from_me` messages do not, and delayed
  messages cannot move activity backward. Bodyless messages remain bodyless
  with empty text.
- Duplicate composite message IDs, including body-retention improvements, do
  not emit another new-message event or increment unread. History/backfill
  writes never enter this stream.
- A consumer may lag by 64 events without blocking the service. At capacity,
  the writer pauses after commit until the consumer drains space; events are
  neither dropped nor reordered. Consumers should drain concurrently during
  normal operation and graceful shutdown.
- The service owns the channel. Natural and graceful completion stops new
  production, preserves already buffered committed events for the receiver,
  and closes the channel once from the writer's single exit path. Forced
  context cancellation is the bounded cutoff: it unblocks a publisher when a
  consumer has stopped, closes the channel, and preserves events already
  accepted by the channel; callers must keep consuming through graceful
  shutdown when delivery of every committed event is required.
- 2.4B1 consumes this contract only for chats already in the bounded TUI
  snapshot. New-chat insertion, outgoing service commands, WhatsApp
  transport/authentication, SQLite production composition, schema changes, and
  a generic event bus remain out of scope.

## Increment 2.4B1 live updates for existing chats

- The live data path is `service.Core.LiveEvents` to one `cmd/walite` adapter
  consumer, through one fixed 64-entry application-owned presentation channel,
  into the TUI event loop. The coalesced `Updates` status/lifecycle consumer
  remains separate. Adapter and TUI perform no store queries.
- `tui.LiveMessage` carries copied string IDs/text, sent time, direction,
  retained-body state, authoritative unread count, and authoritative activity.
  `internal/tui` continues to import no model, service, store, configuration,
  SQL, or SQLite package.
- Existing chats are routed by stable chat ID. Unknown chat events are ignored
  without insertion, eviction, buffering, selection changes, or redraw; 2.4B2
  owns dynamic insertion.
- Messages remain bounded to 32 per chat and are displayed by `(sent_at ASC,
  message_id ASC)`, independent of commit arrival order. When full, the oldest
  displayed message is evicted so only the newest bounded working set remains.
  Duplicate composite IDs do not append or redraw.
- Chat unread and activity come directly from the committed event. Activity is
  never moved backward. Chats are ordered by `(activity DESC, chat ID ASC)`,
  and the selected chat is restored by stable ID after every reorder.
- Live mutation leaves compose draft/cursor, reply target, emoji/settings
  popups, and selected chat intact. A selected-chat event keeps the newest
  viewport at the bottom; while reading older history, the scroll offset is
  adjusted only when needed to anchor the visible messages and retain the newer
  message indicator.
- The TUI blocks on context, terminal events, or live presentation events. One
  visible mutation causes one normal redraw; duplicate, bounded-away, and
  unknown-chat no-ops cause none. A closed live channel is disabled in the
  select loop, leaving terminal input usable with zero polling or idle redraw.
- During service-led shutdown the application continues forwarding until the
  service live channel closes before canceling the TUI. If the TUI exits first,
  cancellation stops a blocked adapter and invokes the B0 forced-cutoff path;
  there is no replay slice or per-event goroutine.
- The temporary four-chat working set and 32-message per-chat bound remain
  prototype presentation limits, not production chat capacity. 2.4B2 must
  replace or extend the four-chat bound before new-chat insertion is complete.
  Local compose/send remains TUI-only; the outgoing service path remains 2.4B3.
