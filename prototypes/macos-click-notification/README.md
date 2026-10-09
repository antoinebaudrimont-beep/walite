# macOS clickable-notification prototype

This isolated prototype tests whether a local notification can return to the exact iTerm2 session that posted it. It does not modify or launch production Walite.

## Findings on macOS 27.0.1

The original compiled AppleScript droplet reached its `on open` handler, but macOS `usernoted` denied its legacy notification connection. A stable bundle identifier and Launch Services registration alone did not resolve the denial. Direct `osascript` notifications were delivered under **Script Editor**, so they did not test notification delivery for the droplet. The old `on run` handler also failed on a cold launch before a session was registered.

The replacement uses macOS `UserNotifications` to request permission and post the notification. Each notification carries the normalized iTerm2 session UUID in its own local request. Clicking it runs the previously validated iTerm2 window/tab/session traversal through the system AppleScript framework. It uses no terminal-notifier or other third-party runtime component. The original `.applescript` file remains as a reference to the earlier experiment; the build uses `WaliteNotificationPrototype.swift`.

## Build and test

Run from the iTerm2 session to restore:

```sh
cd prototypes/macos-click-notification
./build-prototype.sh
./post-prototype-notification.sh
```

The build requires the installed Apple Command Line Tools and creates `~/Applications/Walite Click Notification Prototype.app`. It refuses to overwrite an existing app. The posting script keeps the request file private under `${TMPDIR}/walite-click-notification-prototype/`.

On the first post, allow notifications for **Walite Click Notification Prototype**. To test the click:

1. Move to a different iTerm2 session and minimize the window containing the original session.
2. Click the newest **Walite click prototype** notification in Notification Centre.
3. If prompted, allow this prototype to control iTerm2.
4. Confirm that the original window is unminimized and its exact tab and session are selected.

The prototype stays running after posting so it can receive notification responses. It has not been integrated into Walite, tested after logout/reboot, or tested with multiple simultaneous Walite instances. The legacy AppleScript file is retained only for comparison.
