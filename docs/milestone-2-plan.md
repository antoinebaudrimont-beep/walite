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
4. **2.2B2 — keyset pagination (current, ready for review):** add bounded,
   indexed message-page reads and deterministic composite cursors without
   OFFSET queries.
5. **2.2C — pruning and cache budget (pending):** add retention snapshots,
   bounded pruning, usage accounting, and the remaining failure tests behind
   the existing service-owned contract.
6. **2.3 — replace prototype JSON chat persistence:** make an explicit
   migration or retirement decision for `~/.local/share/walite/state.json`;
   do not silently merge its schema into SQLite.
7. **2.4 — service-to-TUI data adapter:** replace synthetic TUI-owned chat data
   with bounded snapshots and immutable service updates without moving storage
   ownership into the TUI.
8. **2.5 — offline integration validation:** verify startup ordering,
   cancellation, store failures, retention, pagination, race safety, and target
   performance with only offline fakes.
9. **2.6 — transport readiness boundary:** only after the offline cache and UI
   adapter pass may WhatsApp transport and authentication work begin.

## Remaining temporary deviations after 2.1

- JSON chat/message state and its file backend remain in `internal/tui`.
- Emoji preference path and file I/O remain in `internal/tui`.
- TUI chat/message data remains synthetic and is not yet service-driven.
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
