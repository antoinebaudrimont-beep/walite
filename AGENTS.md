# Walite agent instructions

## Project goal

Build a lightweight, responsive personal WhatsApp terminal client named
`walite`.

The primary development machine is an old Intel Core 2 Duo MacBook running
MX Linux XFCE on linux/amd64. CPU use, memory use, terminal redraw frequency
and disk writes must remain modest.

## Reference repositories

Read-only reference repositories are located in:

- ../references/whatscli
- ../references/whatsapp-cli
- ../references/go-whatsapp-cli
- ../references/nchat
- ../references/whatsmeow

Do not modify these repositories.

Do not mechanically combine or copy complete files from them.

Whenever code or an architectural idea is adapted, record:

- originating repository
- exact file and symbol
- applicable licence
- what was adapted rather than copied

## Protocol rules

Use the maintained `go.mau.fi/whatsmeow` library.

Do not:

- implement the WhatsApp protocol directly
- reverse-engineer packet formats
- use Chromium or Electron
- use Puppeteer
- use whatsapp-web.js
- embed WhatsApp Web
- automate bulk or unsolicited messaging

Before relying on any whatsmeow behavior, confirm it against the checked-out
source under `../references/whatsmeow`.

Do not claim that WhatsApp history transfer can be disabled or limited unless
the current whatsmeow source confirms it.

## Responsiveness requirements

The interface must become usable before initial history synchronization ends.

The application must not:

- block the UI while importing history
- load an unlimited message archive into memory
- use unbounded channels or queues
- continuously redraw the full terminal
- busy-wait
- download media automatically
- index years of history before showing chats

New incoming messages must take priority over historical messages.

## Default cache policy

Initial proposed defaults:

- newest 100 messages per chat
- maximum message age of 90 days
- maximum application cache size of 250 MB
- media prefetch disabled
- media downloaded only after explicit request
- older history requested only after explicit user action

These values must eventually be configurable.

If WhatsApp supplies more historical data than the retention policy allows,
process only the metadata required for correct operation and discard unwanted
message bodies.

## Package boundaries

Keep these packages separate:

- `cmd/walite`: executable and CLI wiring
- `internal/model`: dependency-free owner of shared application values, bounded
  constructors and validation; no I/O, goroutines, interfaces or dependency on
  another internal package
- `internal/config`: configuration and XDG paths
- `internal/wa`: whatsmeow adapter
- `internal/store`: persistence and queries
- `internal/syncpolicy`: retention and prioritization
- `internal/service`: application coordination
- `internal/tui`: terminal interface

Package dependency direction:

- `internal/model` has no internal dependencies.
- `internal/config` has no application dependencies.
- `internal/syncpolicy` may import `internal/model` and `internal/config`.
- `internal/service` may import `internal/model` and `internal/syncpolicy`.
- `internal/wa` imports `internal/model` but not `internal/service`.
- `internal/store` imports `internal/model` but not `internal/service`.
- `internal/tui` may import `internal/model` and `internal/service`.
- `cmd/walite` imports selected concrete implementations and performs wiring.

The TUI must not access SQLite or whatsmeow directly.

Interfaces must permit:

- fake WhatsApp clients
- in-memory stores
- deterministic tests
- operation without a network connection

## Concurrency rules

- Every goroutine must have a clear owner.
- Every goroutine must stop through context cancellation or explicit closure.
- Channels must be bounded.
- Backpressure behavior must be documented.
- Network callbacks must not perform slow database or UI work directly.
- SQLite writes should use bounded batches and transactions.
- No data races are acceptable.

## Security

- Store user data under the XDG data directory.
- Private directories must use mode 0700.
- Private files and databases must use mode 0600.
- Never log QR contents.
- Never log session keys or encryption material.
- Never log complete private messages by default.
- Never commit session databases or downloaded media.
- Never include real phone numbers or messages in tests.
- Sending must require explicit human action.
- Do not authenticate or connect to WhatsApp during automated tests.

## Development workflow

For every milestone:

1. inspect relevant reference code
2. document confirmed behavior
3. distinguish facts from inference
4. propose the smallest implementation
5. add or update tests
6. implement
7. format and validate
8. summarize changes and unresolved limitations

Required validation:

```text
gofmt
go vet ./...
go test ./...
go test -race ./...
```
