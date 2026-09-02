# walite

walite is a lightweight, keyboard-driven WhatsApp client for Linux terminals, written in Go. It connects as a linked device through [whatsmeow](https://github.com/tulir/whatsmeow) and uses a bounded SQLite cache for responsive startup and modest resource use.

walite is an unofficial, deliberately text-focused client. It is not intended to replace every feature of the official WhatsApp applications.

## Screenshot

Screenshots have not been added to the repository yet.

## Features

- WhatsApp linked-device QR pairing and persistent reconnects
- A one-time dedicated pairing window on the current Linux/XFCE target
- Broad HistorySync-backed conversation bootstrap
- Persistent, pure-Go SQLite chat and message cache—no CGO required
- Up to 10,000 lightweight chat summaries with bounded recent message history
- Cached startup without waiting for a complete HistorySync
- Incoming and outgoing text messages
- One-to-one quoted replies and incoming quoted-reply rendering
- Contact and group names when available, with readable phone-number fallbacks for unresolved PN contacts
- PN/LID alias handling and message deduplication
- Directional incoming/outgoing layout, date separators, and group sender labels when metadata is available
- Persistent local unread state and real WhatsApp read receipts when an unread chat is opened
- Unicode-safe composition and rendering, including an emoji picker with persistent recent emoji

## Installation and build

Building walite requires Go 1.26.0 or newer. Linux/amd64 is the currently validated runtime environment.

```sh
git clone https://github.com/antoinebaudrimont-beep/walite.git
cd walite
go build ./cmd/walite
```

Run the built binary:

```sh
./walite
```

Or run directly from the source tree:

```sh
go run ./cmd/walite
```

## First pairing

The first launch links walite to WhatsApp using a QR code. On the current Linux/XFCE implementation, walite opens a temporary `xfce4-terminal` window sized so the QR code can be rendered without distortion. On your phone, open **WhatsApp → Linked devices → Link a device**, then scan the code.

The pairing window closes after a successful link. Later launches reuse the persisted session and reconnect without showing another QR code. If the helper window cannot be launched, walite prints a manual `walite --pair` fallback instruction.

## Usage and keybindings

### Chat navigation

| Key | Action |
| --- | --- |
| `j` / `k` | Select the next / previous chat |
| `↑` / `↓` | Scroll message history |
| `PageUp` / `PageDown` or `Ctrl-U` / `Ctrl-D` | Scroll history by a page |
| `Home` / `End` | Jump to the oldest / newest loaded message |
| `Enter` | Start composing |
| `Ctrl-R` | Select a message to reply to |
| `Ctrl-P` | Open the settings summary |
| `Esc` or `Ctrl-C` | Quit |

### Composing and popups

| Key | Action |
| --- | --- |
| `Enter` | Send the draft |
| `←` / `→`, `Backspace`, `Delete` | Edit the draft |
| `↑` / `↓`, `PageUp` / `PageDown`, `End` | Browse history without losing the draft |
| `Ctrl-R` | Choose a reply target |
| `Ctrl-E` | Open or close the emoji picker |
| `Ctrl-P` | Open the settings summary |
| `Esc` | Cancel the current mode or close a popup |

In reply selection, use `↑`/`↓` or `j`/`k`, then `Enter` to confirm. In the emoji picker, use the arrow keys or `h`/`j`/`k`/`l`; `Tab` and `Shift-Tab` switch categories, and `Enter` inserts the selected emoji.

## Chat history and cache

HistorySync seeds a broad conversation list on initial setup. Chat summaries and bounded message pages are stored in SQLite, so later launches can show cached conversations immediately instead of waiting for another complete sync.

The cache is intentionally bounded: walite keeps at most 10,000 chat summaries, while selected or loaded chats use a fixed 32-message presentation window. Browsing the chat list does not allocate or load a full message history for every conversation.

## Performance

walite is designed to remain usable on older hardware, including the project's Intel Core 2 Duo T9900 MacBook Pro running Linux/amd64. Current measurements on that target are:

| Operation | Time |
| --- | ---: |
| List 1,000 cached chats | 11.20 ms |
| List 10,000 cached chats | 118.59 ms |
| Load the newest 50-message page | 0.761 ms |
| Build and draw the first frame from 10,000 cached summaries | 14.94 ms |

These figures are development measurements, not cross-system guarantees. Predictable resource use comes from bounded chat/message working sets, fixed-capacity asynchronous workers and queues, and SQLite-backed persistence—there is no 10,000-chats-by-message-buffer allocation.

## Architecture

```text
terminal UI
    ↓
service/core
    ├── SQLite cache
    └── WhatsApp adapter (whatsmeow)
```

The TUI and service use transport-neutral application data. WhatsApp-specific types stay inside the adapter, while bounded workers keep network and storage operations out of the terminal event loop.

## Current limitations

- The client is text-focused; media, reactions, typing indicators, and presence are not implemented.
- Sending quoted replies is currently supported for one-to-one chats, not groups.
- On-demand paging of history older than the bounded recent window is not implemented.
- Status/broadcast filtering and presentation are still limited.
- Some LID-only contacts may still appear with opaque identifiers when no usable metadata is available.
- Linux/amd64 is the validated platform. The dedicated pairing-window helper is currently specific to XFCE's `xfce4-terminal`.
- macOS is not yet officially supported.

## Roadmap

- On-demand older-history paging
- Media and reaction support
- Improved group and status handling
- macOS portability
- Packaged releases

No release dates are currently promised.

## Platform support

Linux/amd64 is the currently validated environment. Most of walite is portable Go, but macOS support still needs platform-specific pairing, path, and terminal validation.

## Development and validation

The SQLite backend is pure Go and does not require CGO. Before review, changes are formatted with `gofmt` and checked with:

```sh
go vet ./...
go test ./...
go test -race ./...
git diff --check
```

## Disclaimer

walite is an unofficial project and is not affiliated with, endorsed by, or sponsored by WhatsApp or Meta. It uses the independent [whatsmeow](https://github.com/tulir/whatsmeow) library to connect to WhatsApp.
