# walite

walite is a lightweight, keyboard-driven WhatsApp client for the terminal. Written in Go, it connects as a linked device through [whatsmeow](https://github.com/tulir/whatsmeow) and uses a bounded SQLite cache for fast startup and predictable resource use.

The project was developed and tested on an old Core 2 Duo MacBook Pro running Linux, but it is designed as a general terminal-native client rather than for one machine. walite is unofficial and is not affiliated with WhatsApp or Meta.

## Why walite?

walite began as an experiment to make WhatsApp usable on an older Linux laptop without keeping a heavyweight browser client open. Its priorities are keyboard-first interaction, fast cached startup, bounded queues and working sets, modest resource use, and a workflow that feels at home in a terminal.

## Screenshots

Four themes, with settings available through `Ctrl-P`.

| Terminal (default) | Dark |
| --- | --- |
| [![walite Terminal theme with a demo conversation](docs/screenshots/walite-terminal.png)](docs/screenshots/walite-terminal.png) | [![walite Dark theme with the settings popup](docs/screenshots/walite-dark.png)](docs/screenshots/walite-dark.png) |
| **Light** | **High contrast** |
| [![walite Light theme with the settings popup](docs/screenshots/walite-light.png)](docs/screenshots/walite-light.png) | [![walite High contrast theme with the settings popup](docs/screenshots/walite-high-contrast.png)](docs/screenshots/walite-high-contrast.png) |

These screenshots were AI-edited for privacy using demo names and messages. Text rendering and spacing may differ from the application.

## Features

- Linked-device QR pairing and persistent reconnects
- A one-time dedicated pairing window on the validated Linux/XFCE setup
- Broad HistorySync conversation bootstrap
- Persistent pure-Go SQLite cache with no CGO requirement
- Up to 10,000 lightweight chat summaries and bounded recent message history
- Cached startup without waiting for a complete HistorySync
- Explicit bounded older-history paging
- Incoming and outgoing text messages
- Outgoing images, video, audio, documents, and WhatsApp-compatible WebP stickers from a pasted or typed local path
- Compact incoming/outgoing reactions and direct/group quoted replies
- Bounded HTTP(S) link opening and clipboard copy from a focused message
- Image, video, document, audio, and sticker messages shown as lightweight placeholders
- Explicit media download/save, plus inline image/GIF and WebP sticker preview through optional `ueberzugpp` X11 overlays
- Text replies—including group and media-placeholder replies—and incoming quote rendering
- Contact, group, and group-participant names when authoritative local metadata is available
- Readable phone-number fallbacks plus PN/LID identity handling and deduplication
- Directional incoming/outgoing layout and date separators
- Persistent local unread state and WhatsApp read receipts on explicit chat selection, reply, or compose/send interaction
- Unicode-safe composition and rendering
- Emoji picker with persistent recent emoji
- Functional `Ctrl-P` settings with four themes: Terminal, Dark, Light, and High contrast
- Toggles for timestamps, quit confirmation, and read receipts

## Installation and build

### Prebuilt releases

Tagged releases provide unsigned archives for Linux and macOS on both amd64 and arm64. No Go installation is needed. Download the archive matching your system from [GitHub Releases](https://github.com/antoinebaudrimont-beep/walite/releases), along with `SHA256SUMS`, then verify, unpack, and pair it. For example:

```sh
sha256sum -c SHA256SUMS --ignore-missing
tar -xzf walite_v0.5.0_linux_amd64.tar.gz
./walite --pair
./walite
```

The archive preserves the executable bit, so `chmod` is not normally needed. If an unusual download or extraction tool removes it, run `chmod +x walite` once. Use `./walite --version` to verify the binary or `./walite --help` for the concise command summary.

For first-time pairing, run `./walite --pair` in a terminal large enough for the complete QR code; later `./walite` launches reuse the linked session. On macOS, verify a downloaded archive with the built-in checksum tool, for example `grep 'walite_v0.5.0_darwin_arm64.tar.gz' SHA256SUMS | shasum -a 256 -c -`.

Each archive contains `walite`, `LICENSE`, and `README.md`. The macOS binaries are not code-signed or notarized. If macOS blocks the first launch, make that launch attempt, then open **System Settings → Privacy & Security** and use **Open Anyway** for walite; this approves only that binary rather than weakening Gatekeeper globally.

| Operating system | Architectures |
| --- | --- |
| Linux | amd64, arm64 |
| macOS | amd64, arm64 |

Here, amd64 means Intel/AMD x86-64, while arm64 means Apple Silicon or another ARM64 system.

### Build from source

Building walite requires Go 1.26.0 or newer. MX Linux with XFCE/X11 on linux/amd64 and macOS on Apple Silicon are the currently validated runtime environments.

```sh
git clone https://github.com/antoinebaudrimont-beep/walite.git
cd walite
go build ./cmd/walite
./walite
```

You can also run it directly from the source tree:

```sh
go run ./cmd/walite
```

### Optional Linux desktop helpers

Core messaging requires only walite, a compatible terminal, and network access; it does not require a media viewer or clipboard helper. On Linux/X11, optional integrations use `ueberzugpp` for inline image/GIF/sticker previews, `mpv` for video/audio, `zathura` for PDF, `xdg-open` for URLs and desktop-default files, `xclip` or `xsel` for clipboard copy, and `xfce4-terminal` for the validated automatic first-pairing window. When a helper is unavailable, walite reports the unavailable optional action without blocking normal startup or messaging.

macOS uses native `open`, `pbcopy`, and Terminal.app integration. iTerm2 and `mpv` are not dependencies; explicit first-time pairing works with `./walite --pair` in any sufficiently large terminal.

## First pairing

On first launch, walite links to WhatsApp using a QR code. Linux/XFCE opens a temporary `xfce4-terminal`; macOS can use a temporary built-in Terminal.app window. Neither affects the terminal used for normal chat operation. On macOS, the reliable explicit path is `./walite --pair` in a terminal enlarged enough to display the complete QR. On your phone, open **WhatsApp → Linked devices → Link a device**, then scan the code.

After pairing succeeds, normal `./walite` launches reuse the persisted session and reconnect without another QR code. If the automatic helper cannot be launched, walite prints the same manual `walite --pair` fallback command.

## Usage

### Chat navigation

| Key | Action |
| --- | --- |
| `j` / `k` | Select the next / previous chat |
| `↑` / `↓` | Scroll message history |
| `PageUp` / `PageDown` or `Ctrl-U` / `Ctrl-D` | Scroll history by a page |
| `Home` / `End` | Jump to the oldest / newest loaded message |
| `O` | Load one bounded page before the oldest loaded message |
| `Enter` | Start composing |
| `F` | Enter local file-path send mode |
| `Ctrl-R` | Select a message to reply to |
| `P` | Preview the newest visible supported media placeholder |
| `S` | Save the newest visible media item to `~/Downloads/walite` |
| `Ctrl-P` | Open settings |
| `Esc` | Quit, or ask for confirmation when enabled |
| `Ctrl-C` | Quit immediately |

### Composing and popups

| Key | Action |
| --- | --- |
| `Enter` | Send the draft |
| `←` / `→`, `Backspace`, `Delete` | Edit the draft |
| `↑` / `↓`, `PageUp` / `PageDown`, `End` | Browse history without losing the draft |
| `Ctrl-R` | Choose a reply target |
| `Ctrl-E` | Open or close the emoji picker |
| `Ctrl-P` | Open settings |
| `Esc` | Cancel the current mode or close a popup |

In reply selection, use `↑`/`↓` or `j`/`k`, then `Enter` to confirm. Press `L` on the focused message to open its sole HTTP(S) link, or choose among multiple links with `↑`/`↓` and `Enter`; `C` copies the selected link through `xclip` or `xsel` on Linux and `pbcopy` on macOS. Logical URLs remain intact when visually wrapped, and other URL schemes are ignored. Press `R` on a focused text or media message to open the emoji picker for an own reaction; choosing another emoji changes it, and `Delete` removes it. In the emoji picker, use the arrow keys or `h`/`j`/`k`/`l`; `Tab` and `Shift-Tab` switch categories, and `Enter` inserts the selected emoji.

Press `F`, type or safely paste a local path, then press `Enter` to send an image, video, audio file, document, or compatible WebP sticker. Regular outgoing media is limited to 100 MiB and is shown locally only after successful WhatsApp transport and cache commit. Stickers must already be valid 512×512 WebP files: static stickers are limited to 100 KiB; animated stickers are limited to 500 KiB, 10 seconds, and structurally valid bounded frames. walite does not convert ordinary images into stickers.

Media is never downloaded in the background. Scroll until the intended media placeholder is the newest visible media row, or focus it with `Ctrl-R` and the arrow keys, then press `P` to preview or `S` to save. On Linux/X11, images, ordinary GIFs, and WebP stickers prefer an inline `ueberzugpp` overlay; video (including WhatsApp GifPlayback MP4) and audio use `mpv`; PDFs use `zathura`. On macOS, every previewable image, sticker, GIF, video, audio file, and PDF uses the system `open` handler with no optional viewer dependency. Ambiguous cached names such as `.bin` are presented through a private MIME-typed cache artifact without redownloading or changing a durable save. Playback and looping depend on the selected macOS application, and animated WebP may appear as a static frame. Other document types remain save-only. Inline overlays close with `P`, `Esc`, navigation, resize, or application shutdown. walite-owned Linux viewers can be closed with `Esc`; applications launched through macOS `open` are not terminated because walite does not own them. Missing optional Linux viewer helpers do not block startup.

## Settings

Press `Ctrl-P` to configure:

- **Theme:** Terminal, Dark, Light, or High contrast
- **Timestamps:** show or hide message times
- **Confirm quit:** require confirmation when quitting with `Esc`
- **Send read receipts:** enable or suppress new remote read receipts

Use `↑`/`↓` or `j`/`k` to move between settings. `Enter` or `Space` changes the selected value. Choose **Save and close** to persist and apply the changes; `Esc` or `Ctrl-P` closes the panel and discards unsaved edits.

Settings are stored in `$XDG_CONFIG_HOME/walite/config.json`—normally `~/.config/walite/config.json`. Timestamps and read receipts default to On; quit confirmation defaults to Off. Disabling read receipts does not stop walite from clearing its own local unread badge when you explicitly select, reply in, or compose/send in a chat.

## Chat history and cache

HistorySync seeds a broad conversation list during initial setup. Chat summaries and bounded message pages are stored in SQLite, allowing later launches to display cached conversations immediately.

The cache keeps at most 10,000 chat summaries. Background chats retain at most 32 in-memory messages; only the selected chat expands to a fixed 256-message browsing buffer, so the summary list never becomes a 10,000 × 256 allocation. At the oldest loaded boundary, press `O` to prepend one page of up to 50 older messages without discarding the recent conversation. The request is explicit and never chains automatically; imported messages are deduplicated and remain in SQLite across restarts. When the selected buffer reaches 256, new committed messages evict the oldest retained rows, while loading still-older rows never evicts newer context. Explicitly downloaded decrypted media uses a separate private cache under `$XDG_CACHE_HOME/walite/media` (normally `~/.cache/walite/media`), bounded to 256 files and 256 MiB. Durable saves under `~/Downloads/walite` are never evicted with that cache.

## Performance

These development measurements were recorded on an Intel Core 2 Duo T9900 MacBook Pro running Linux/amd64. They are target-machine observations, not universal benchmarks.

| Operation | Time |
| --- | ---: |
| List 1,000 cached chats | 11.20 ms |
| List 10,000 cached chats | 118.59 ms |
| Load the newest 50-message page | 0.761 ms |
| Build and draw the first frame from 10,000 cached summaries | 14.94 ms |

Bounded chat/message working sets, fixed-capacity asynchronous workers and queues, and SQLite-backed persistence keep resource use predictable.

## Architecture

```text
terminal UI
    ↓
service/core
    ├── SQLite cache
    └── WhatsApp adapter (whatsmeow)
```

The UI and service use transport-neutral application data. WhatsApp-specific types stay inside the adapter, and bounded workers keep network and storage operations out of the terminal event loop. Optional terminal and desktop helpers are selected through a capability boundary; core chat operation does not depend on a named terminal emulator.

## Current limitations

- Inline image/GIF and WebP sticker overlays remain Linux/X11-specific and require `ueberzugpp`; macOS uses system viewers. Animated WebP playback depends on the selected viewer and may appear as a static frame. Other document formats remain save-only.
- Expired WhatsApp media references are reported as unavailable; media-retry refresh is not implemented yet.
- Typing indicators and presence are not implemented.
- Status, broadcast-list, newsletter, and other unsupported special chats remain visible from cache but are read-only; sending text, files, replies, or reactions is rejected locally.
- Some identities remain opaque when WhatsApp provides no usable authoritative local metadata.
- Release binaries are provided for Linux and macOS on amd64 and arm64; hands-on validation currently covers MX Linux/XFCE/X11 on amd64 and macOS on Apple Silicon.
- Automatic pairing uses the validated XFCE helper on Linux and built-in Terminal.app on macOS; iTerm2 is optional and is not required.
- macOS/Apple Silicon core operation and explicit `--pair` linking have been manually validated; the terminal must be large enough to display the complete pairing QR.

## Roadmap

### v0.3.0

- Image/GIF and sticker download, save, and inline preview through `ueberzugpp` (v0.3B1)
- Video/audio preview through `mpv` and PDF preview through `zathura` (v0.3B2)
- Text replies to media messages using bounded placeholder metadata
- On-demand older-history paging with a 256-message selected-chat working set (v0.3C)
- Read receipts for explicit interaction with an already-selected unread chat

Planned image flow:

```text
image message
  → visible media placeholder in walite
  → user requests preview
  → walite downloads and decrypts the image
  → bounded private cache
  → inline ueberzugpp X11 overlay opens
```

### v0.4.0

- Outgoing images, video, audio, documents, and static/animated WebP stickers
- Persistent bounded reactions, including adding, changing, and removing an own reaction
- Bounded HTTP(S) link opening and clipboard copy from focused messages
- Direct and group quoted replies with typed text/media quote presentation
- Visible read-only handling for unsupported status, broadcast-list, newsletter, and other special chats

### v0.5

- Terminal-agnostic platform/capability boundary with graceful optional-helper fallback (v0.5A)
- macOS portability and runtime integration (v0.5B)
- Reproducible Linux and macOS release archives for amd64 and arm64 (v0.5C)
- Packaging and installation improvements

### v1.0

- Stable, documented release
- Dependable core messaging and cache behavior
- Easy installation
- Clearly defined supported and unsupported features

No release dates are promised.

## Development

The SQLite backend is pure Go and does not require CGO. The main validation commands are:

```sh
go vet ./...
go test ./...
go test -race ./...
git diff --check
```

## Disclaimer

walite is an unofficial project and is not affiliated with, endorsed by, or sponsored by WhatsApp or Meta. It uses the independent [whatsmeow](https://github.com/tulir/whatsmeow) library to connect to WhatsApp.

walite is released under the [MIT License](LICENSE).
