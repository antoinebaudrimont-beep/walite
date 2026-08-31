# Milestone 4 plan

## Milestone 4A — Real incoming plain-text messages

Status: ready for automated review and real-message manual validation. Do not
mark complete until a linked account has received and displayed real messages.

Milestone 4A connects the existing long-lived whatsmeow client to walite's
existing bounded service pipeline. Direct `events.Message` conversation text
and extended-text bodies are copied into `model.Event` values, committed by the
existing realtime writer, published through `Core.LiveEvents`, and applied by
the existing TUI live-message path. The WhatsApp chat JID is the stable chat ID
and temporary presentation title when no chat metadata is available.

The connection-owned adapter has a fixed 64-event working queue. A full queue
applies cancellable backpressure to the whatsmeow callback; it does not create
per-message goroutines, grow storage, or silently discard recognized text.
Connected production mode starts with an empty memory-backed snapshot and does
not compose `FakeSource` or `OfflineTextSender`. Walite-originated sends remain
disabled and a rejected send preserves the draft.

Explicitly excluded from 4A:

- WhatsApp history sync
- media and body placeholders
- contact names and group metadata
- real outgoing sends
- reply transport semantics
- edits, deletes, reactions, receipts, presence, typing, calls, and status
- production SQLite cache composition
