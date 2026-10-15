# dunst_tray integration: repeatable Walite conversation navigation

## Version 2 — coordinated rotating-ticket extension

V2 supersedes the v1 one-shot flow below for new Linux notifications. Generic
**Show app** remains focus only, never consumes a ticket and never selects a
conversation. Only an explicit **Open discussion** click uses this exchange.
No WhatsApp message is sent. No stable ChatID or reusable IPC credential leaves
Walite. The TUI's draft/sending/paste/modal protection is unchanged.

### V2 hint, lifetime and bounds

Application name `walite`; string hint `x-walite-activation`, with exactly:

```json
{"version":2,"activation_socket":"/tmp/walite-activation-…/s-….sock","activation_id":"64 lowercase hexadecimal characters","expires_at":1234567890}
```

Each notification starts a distinct chain with a random 256-bit one-use ticket.
`expires_at` is integer Unix UTC time, **24 hours after original issuance**.
Renewal never extends expiry or changes FIFO priority. The newest 32 chains are
retained in memory; newer notifications evict the oldest. Only Walite retains
the original stable ChatID. All chains disappear on exit/restart.

### Private socket contract

Use only that notification's endpoint and ticket, never process discovery by
name/title/PID. The directory directly under `/tmp` has prefix
`walite-activation-`, mode 0700; its native `s-….sock` socket has mode 0600.
Both must belong to the current user; paths are bounded to 103 UTF-8 bytes.
Reject symlinks, incorrect types/modes/owners, traversal and unrelated paths.
Pin/revalidate endpoint identity against replacement races, and verify the Unix
peer UID (Linux `SO_PEERCRED`). The Linux tray uses a validated directory
descriptor through `/proc/self/fd` and rechecks socket inode/device before send.

Treat endpoint and ticket as private: no logs, UI text, subprocess arguments or
diagnostics. Persist only within the bounded private notification record, not
external caches/settings. The receiver-wide reusable IPC credential and stable
ChatID must never enter this protocol or D-Bus hints.

Connect as `AF_UNIX` stream, send exactly one UTF-8 object, then shut down the
write side for EOF framing:

```json
{"version":2,"activation_id":"current one-use capability"}
```

Read exactly one response through EOF and close. Requests are bounded to 4096
bytes, responses to 1024. Reject duplicate/unknown/case-altered keys, wrong
types/versions and trailing objects. The receiver bounds reads and writes to
one second each; dunst_tray bounds its full exchange to 0.5 seconds. No automatic
retries, process launch or identity/token fallback is allowed.

### Atomic one-use rotation

Walite atomically consumes a valid unexpired ticket and installs a fresh random
successor for the same original chat, absolute expiry and chain position:

```json
{"version":2,"status":"queued","activation_id":"fresh one-use capability","expires_at":1234567890}
```

`queued` acknowledges bounded TUI queue admission, **not rendered selection**.
A full queue returns the same shape with `status:"busy"`, also with a fresh
ticket. Save that successor but do not focus Walite; the user can retry later
with it. The TUI may intentionally drop an admitted request during composing,
sending, paste or modal operations. The saved successor still permits another
explicit click after leaving that state; no deferred navigation is kept.

Unknown, consumed, expired, evicted or another instance's tickets return only:

```json
{"version":2,"status":"unavailable"}
```

No successor/identity is disclosed. Malformed requests may be closed silently.
Replaying an old ticket cannot navigate, renew or recover its successor. V2
tickets cannot be consumed by v1 requests, or v1 tickets renewed through v2.

### Tray transactions and focus

Before send, atomically claim that record's exact current ticket with a unique
in-flight marker. Stale/double-queued events cannot claim twice. Validate the
response and conditionally replace only the still-claimed record with its fresh
successor. Keep history/grouping/unread fields unchanged. Clear/retention, crash
or row-ID reuse must never resurrect records. Refresh the control; each explicit
click uses a new capability. Never reuse a consumed ticket or extend its expiry.

For `queued`, best-effort raise the uniquely marked already-running Walite
window through existing X11 focusing. Send/validate **before focus** so a known
unavailable/failed exchange never focuses Walite. Multiple instances still use
only the exact socket; if marked windows are ambiguous, do not guess which to
raise. **Show app** remains independent.

A lost/truncated/malformed/timed-out response is ambiguous: Walite may already
have consumed the old ticket. Fail closed and discard that capability; never
replay it to recover. A crash between rotation and persistence has the same
result. Preserve history/Show app, but that record needs a new notification for
navigation. There is no reusable recovery credential or stable-ID rebinding.

### Restart, expiry and compatibility

- Completed renewal survives tray restarts, while that Walite chain is valid.
- Walite restarts use a new socket and lose all chains. Old history cannot
  navigate the replacement instance; missing endpoints disable Open discussion.
  A fresh notification starts a new chain. Never automatically rebind history.
- Local timestamp expiry disables without connecting. Server-side expiry,
  eviction or replay returns unavailable and removes only navigation metadata.
  Dunst popup expiry does not expire a chain.
- Old trays ignore unknown v2 hints and keep generic focus. The updated tray
  supports old v1 records **once only**, with no response/renewal/replay. Consumed
  or missing v1 tickets cannot be upgraded; wait for a new notification.
- The receiver's private v1 ChatID/token helper IPC remains unchanged; the tray
  must never use it. Native Dunst Open remains independent and does not consume
  the stored-notification ticket. Generic Show app behavior is unchanged.

Tests use synthetic direct/group identities, never a WhatsApp connection. Cover
rotation/replay, repeated/alternating selections, expiry/eviction, queue overflow,
instance/restart isolation, response privacy, socket checks, durable renewal,
stale UI/clear races and actual rendered X11 selection.

## Legacy version 1 — retained for compatibility only

Walite keeps generic application focus separate from notification navigation.
`dunst_tray`'s existing **Show app** action should continue to raise the Walite
terminal and must not select a conversation. Only selecting a particular Walite
message notification should use the contract below.

## Notification hint

On Linux, a Walite notification may contain the string hint
`x-walite-activation`. Its value is versioned JSON:

```json
{
  "version": 1,
  "activation_socket": "/tmp/walite-activation-…/s-….sock",
  "activation_id": "64 lowercase hexadecimal characters"
}
```

The activation ID is a random, one-time capability. Walite retains the stable
WhatsApp chat ID only in memory; neither that ID nor Walite's reusable IPC
credential is placed on D-Bus. Pending mappings are bounded to the newest 32
notifications and disappear when Walite exits.

## Selecting a notification

For a stored notification whose application name is `walite` and whose hint is
valid, `dunst_tray` should:

1. Raise the already-running Walite terminal using its existing window-focus
   implementation.
2. Connect to `activation_socket` as an `AF_UNIX` stream.
3. Write exactly one UTF-8 JSON object and close the write side:

   ```json
   {"version":1,"activation_id":"the value from the hint"}
   ```

4. Close the socket. There is deliberately no response or delivery
   acknowledgement.

The socket path and activation ID must be treated as private, ephemeral data:
do not log them, display them, persist them beyond the notification record, or
pass them to another process. Do not start a new Walite process when the socket
is absent or refuses the connection. A failed or expired activation should
leave the focused application unchanged.

Walite validates the request, resolves the opaque activation ID to the original
stable chat identity, and sends the request through its bounded TUI event
channel. Direct and group chats use the same mechanism. If Walite is composing,
sending, or showing a modal, the TUI intentionally drops the navigation request
so an unfinished draft or interaction is never disrupted.

## Compatibility

- Missing or unknown hints: retain current notification display and **Show
  app** behavior.
- Unsupported versions or malformed data: ignore the navigation metadata.
- Multiple Walite instances: use only the socket and activation ID from that
  notification; never discover a process by display name, title, or PID.
- Walite's live Dunst **Open** action remains supported independently. The
  stored-notification contract above is for `dunst_tray` selection.
