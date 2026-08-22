# walite

A lightweight and responsive personal WhatsApp terminal client for Linux.

## Main goals

- Open the interface immediately.
- Remain responsive during WhatsApp history synchronization.
- Keep only a bounded recent-message cache.
- Download media only when requested.
- Run well on older hardware.
- Use the maintained whatsmeow library rather than implementing the protocol.

## Current status

Milestone 1A provides a dependency-free offline core and a compiled synthetic
textual demonstration:

```sh
go run ./cmd/walite
```

The demo makes no network connection and uses no WhatsApp account. Real
WhatsApp authentication, sending, and the terminal UI remain later milestones.
