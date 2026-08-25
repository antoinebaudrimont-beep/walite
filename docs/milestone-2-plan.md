# Milestone 2 Plan

Milestone 2 prepares and installs the offline application cache before any
WhatsApp transport or authentication work.

## Sequence

1. **2.1 — application wiring and dependency cleanup (current):** load UI
   configuration in `cmd/walite`, inject plain TUI options, and own the existing
   offline service/TUI lifecycle from the production composition root.
2. **2.2 — SQLite cache implementation:** add the concrete store, schema,
   migrations, permissions, bounded transactions, pagination, pruning, and
   failure tests behind service-owned contracts.
3. **2.3 — replace prototype JSON chat persistence:** make an explicit
   migration or retirement decision for `~/.local/share/walite/state.json`;
   do not silently merge its schema into SQLite.
4. **2.4 — service-to-TUI data adapter:** replace synthetic TUI-owned chat data
   with bounded snapshots and immutable service updates without moving storage
   ownership into the TUI.
5. **2.5 — offline integration validation:** verify startup ordering,
   cancellation, store failures, retention, pagination, race safety, and target
   performance with only offline fakes.
6. **2.6 — transport readiness boundary:** only after the offline cache and UI
   adapter pass may WhatsApp transport and authentication work begin.

## Remaining temporary deviations after 2.1

- JSON chat/message state and its file backend remain in `internal/tui`.
- Emoji preference path and file I/O remain in `internal/tui`.
- TUI chat/message data remains synthetic and is not yet service-driven.
- Changed terminal events still trigger a measured full-screen redraw.
