# Milestone 2 Plan

Milestone 2 prepares and installs the offline application cache before any
WhatsApp transport or authentication work.

## Sequence

1. **2.1 — application wiring and dependency cleanup (complete):** load UI
   configuration in `cmd/walite`, inject plain TUI options, and own the existing
   offline service/TUI lifecycle from the production composition root.
2. **2.2A — SQLite cache foundation (current, ready for review):** add the
   concrete driver, secure cache path, connection lifecycle, schema-v1
   migration, corruption/version gates, bounded message round trips, and
   foundation benchmarks without switching the production store.
3. **2.2B — complete SQLite store contracts:** add keyset pagination, retention
   snapshots, bounded pruning, usage accounting, busy handling, and the
   remaining failure tests behind the existing service-owned contract.
4. **2.3 — replace prototype JSON chat persistence:** make an explicit
   migration or retirement decision for `~/.local/share/walite/state.json`;
   do not silently merge its schema into SQLite.
5. **2.4 — service-to-TUI data adapter:** replace synthetic TUI-owned chat data
   with bounded snapshots and immutable service updates without moving storage
   ownership into the TUI.
6. **2.5 — offline integration validation:** verify startup ordering,
   cancellation, store failures, retention, pagination, race safety, and target
   performance with only offline fakes.
7. **2.6 — transport readiness boundary:** only after the offline cache and UI
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
- Deferred to 2.2B or later: pagination, pruning, cache-size accounting,
  batching policy, JSON migration, TUI adaptation, WhatsApp/session storage,
  networking, and WAL checkpoint tuning.
