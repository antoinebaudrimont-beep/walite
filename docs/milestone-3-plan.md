# Milestone 3 Plan

Milestone 3 introduces WhatsApp authentication and connection without changing
the offline message pipeline completed in Milestone 2.

## Roadmap

- **Milestone 1 — offline core and TUI:** complete.
- **Milestone 2 — cache, integration, and service-backed TUI:** complete.
- **Milestone 3 — WhatsApp authentication and connection:** 3A ready for
  review.
- **Milestone 4 — real WhatsApp message traffic:** not started.

## Milestone 3A — QR authentication and connection

### Upstream and platform

Walite uses the maintained `go.mau.fi/whatsmeow` module at
`v0.0.0-20260828224850-0fadda796019` (upstream commit `0fadda796019`). The
checked API requires Go 1.26.0; the implementation was developed with Go
1.26.5 on Linux/amd64. Neither whatsmeow nor the selected existing
`modernc.org/sqlite` driver requires CGO.

The integration relies on these verified upstream contracts:

- `sqlstore.Container.GetFirstDevice` loads an existing device or returns a new
  unlinked device;
- `client.Store.ID == nil` identifies an unlinked device;
- `GetQRChannel(ctx)` is called before `ConnectContext(ctx)` for an unlinked
  device;
- successful pairing is persisted by whatsmeow's SQL device store;
- a stored linked device reconnects without requesting a QR channel;
- `Connected`, `Disconnected`, `ConnectFailure`, and `LoggedOut` events provide
  the connection lifecycle required by this increment; and
- `Disconnect` is safe to call repeatedly.

### Private session storage

Linked-device credentials live at:

```text
${XDG_DATA_HOME:-~/.local/share}/walite/whatsmeow-session.db
```

This database is private application data, not the disposable application
cache at `~/.cache/walite/walite-cache.db`. Walite uses whatsmeow's supported
SQL store and the existing pure-Go SQLite driver; it does not serialize
credentials itself. The path must be absolute. Symlinked path components and
non-regular or foreign-owned database files are rejected. Walite creates or
tightens the directory to mode 0700 and the database file to mode 0600.

Whatsmeow is constructed with its no-op logger. This is important because its
QR helper can include the raw pairing token in debug output. Walite does not
log QR values, device keys, tokens, or database contents.

### Startup flows

Unlinked startup is:

```text
private session probe (closed) -> unlinked
-> one-shot xfce4-terminal -> walite --pair owns the session store
-> pairing view -> QR channel -> connect -> phone scan and persisted link
-> helper disconnects/exits -> pairing terminal closes
-> parent session probe (closed) -> linked
-> new long-lived connection -> Connected
-> construct/start the existing offline Core -> normal chat TUI
```

Linked startup is:

```text
configuration -> private session store -> linked whatsmeow client
-> initialize tcell once -> connect without a QR -> Connected
-> construct/start the existing offline Core -> normal chat TUI
```

The application composition root owns authentication around the existing Core;
the Core has no authentication knowledge and is not constructed until the real
connection reaches `Connected`. On unlinked MX Linux/XFCE systems, pairing and
normal chat use separate terminal processes and separate tcell lifecycles. The
parent does not initialize or modify the normal terminal while the helper owns
pairing. Each process finalizes only its own successfully initialized screen.

### Target-platform pairing window

The installed target is `xfce4-terminal` 1.1.4-1. Its installed manual supports
per-window `--font`, `--geometry`, `--disable-server`, and `--execute` options.
Walite launches this deterministic one-shot command shape:

```text
xfce4-terminal --disable-server --geometry=80x46 --font='Monospace 8' \
  --title='walite — WhatsApp setup' --hide-menubar --hide-toolbar \
  --hide-scrollbar --execute /path/to/current/walite --pair
```

Arguments are passed directly to the process rather than through a shell.
`--disable-server` gives the setup window an independent process, font, and
geometry; no saved XFCE preference or existing window is changed. The 80x46
grid comfortably contains the measured 69x69-module WhatsApp QR as 69x35
half-block cells plus the existing pairing UI. Omitting `--hold` makes the
window close when `walite --pair` exits.

The parent opens the session database only for a synchronous linked-state
probe and closes it before launching the helper. After the helper exits, the
parent probes and closes the store again; only then does it construct the
long-lived connection. Thus parent and helper never intentionally own the
credential database concurrently. A linked first probe skips the helper.

If the terminal process cannot be launched or exits without persisting a link,
the original terminal prints:

```text
WhatsApp pairing requires a larger terminal grid.
Run:
    walite --pair
```

### Bounded state and QR flow

The walite-owned lifecycle has only `disconnected`, `waiting for QR`,
`connecting`, `connected`, and `failed` states. Connection status uses a
capacity-one latest-state channel. The upstream QR channel is bounded at eight
items and is consumed serially; walite immediately converts a raw QR token into
a fixed-size raster and exposes no token outside `internal/wa`. Both the WA-to-
application and application-to-TUI mailboxes retain only the newest bounded
frame, so rotating codes replace rather than accumulate.

The TUI renders the QR directly with upper-half block cells and explicit black
and white colors while preserving the encoder's quiet zone. Each source column
uses one terminal column, and each pair of source rows uses one terminal row,
so an N-by-N source is rendered as N-by-ceiling(N/2) terminal cells without
scaling or cropping. It clears the screen before each changed frame, removes
the QR on the transition to `connecting`, redraws only for a changed state/QR
or resize, and asks the user to enlarge a terminal that cannot fit the full
matrix. There is no browser, temporary image, polling loop, ticker, or
goroutine per event.

### Lifecycle ownership

One `internal/wa.Connection` may be run once. Context cancellation covers both
QR waiting and `ConnectContext`. Normal application shutdown cancels and joins
the connection owner, and `Disconnect` plus session-store `Close` are guarded
for idempotence. Pairing cancellation, upstream channel closure, connection
failure, logout, and unexpected client closure return controlled errors.

Transient disconnections after a successful connection remain visible to the
adapter while whatsmeow owns its normal reconnect behavior. Irrecoverable
failures cancel the application composition.

### Explicit 3A exclusions

- The event handler deliberately ignores `events.Message`, HistorySync,
  receipts, typing, media, and all other non-lifecycle protocol events. No real
  event enters `model.Event`, the realtime writer, `LiveEvents`, or the TUI.
- Production still constructs `newOfflineApplicationService`, whose Core uses
  `wa.NewOfflineTextSender`. No whatsmeow send method is wired, so running or
  testing 3A cannot send a real WhatsApp message.
- History sync, contact/chat synchronization, application-cache migration,
  media, receipts, and a settings/account UI remain deferred.

### Manual pairing procedure

Automated tests use narrow fakes and never contact WhatsApp. A manual test needs
a phone and normal linked-device authorization:

1. Run `go run ./cmd/walite` in the normal terminal.
2. Confirm a separate 80x46 pairing window appears with the smaller temporary
   font. The normal terminal must remain unchanged.
3. On the phone, open **WhatsApp -> Linked devices -> Link a device** and scan
   the terminal QR.
4. Confirm the setup window closes after `Connected` and the parent enters the
   normal offline chat UI in the original terminal. Do not deliberately send a
   message during this milestone.
5. Confirm the private session database exists at the path above.
6. Run `go run ./cmd/walite` again and confirm it reconnects without displaying
   a QR before entering the normal UI.

If no phone/user interaction is available, both real-pairing and second-launch
reconnect results must be reported as pending rather than simulated.

The underlying QR/session path has already been manually validated: the
69x69-module Low-recovery QR scanned successfully, pairing persisted, and a
second launch reconnected without another QR. The dedicated pairing-window UX
remains the final manual retest before commit.

### Validation and dependency impact

Milestone 3A passes formatting, module-tidiness, vet, the full test suite, the
full race-enabled suite, diff whitespace checks, and focused authentication,
QR, lifecycle, dependency-boundary, and first-frame regressions. Real phone
pairing and the second-launch reconnect remain manual checks and are not
simulated by automated tests.

The two added direct modules are whatsmeow and `github.com/skip2/go-qrcode`.
The `go.mod` indirect list grows from 14 to 28 entries. In one unstripped
Linux/amd64 development build, the binary grew from 11,347,910 bytes to
27,148,474 bytes (15,800,564 bytes, about 15.1 MiB). One warm-cache build sample
grew from 1.66 seconds to 3.51 seconds. These are informational single-machine
measurements, not performance thresholds. The resulting executable remains an
x86-64 Linux ELF, and the dependency path remains CGO-free.
