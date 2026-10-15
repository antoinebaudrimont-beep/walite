# dunst_tray integration: open a Walite conversation

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
