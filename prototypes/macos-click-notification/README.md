# macOS clickable-notification prototype

This isolated prototype tests one unverified link in the proposed macOS design:
whether clicking a notification posted by an AppleScript application relaunches
that application and lets its `run` handler restore the registered iTerm2
session.

It does not modify or launch walite. It is not wired into the production
notification worker.

## Already validated

The `restoreSession` handler is the same traversal already tested manually on
macOS 27.0.1. Given an iTerm2 session UUID, it finds that exact session across
all windows, tabs, and sessions, unminiaturizes the owning window, selects the
window/tab/session, and activates iTerm2.

The prototype obtains the current session dynamically from iTerm2's
`ITERM_SESSION_ID` environment variable. iTerm2 formats that value like
`w0t0p0:UUID`; the AppleScript removes the pane prefix before comparing it with
each session's `unique id`.

## Still to validate on macOS

The notification is posted by a compiled AppleScript application. Apple
documents that opening a script application's notification opens the app again
and invokes its `run` handler. The manual test below must confirm that current
macOS performs that relaunch for this prototype and that the app's stored
`targetSessionID` survives between posting and clicking.

## Build and test

Run these commands in the iTerm2 session that the notification should restore:

```sh
cd /path/to/walite/prototypes/macos-click-notification
chmod +x build-prototype.sh post-prototype-notification.sh
./build-prototype.sh
./post-prototype-notification.sh
```

The build script creates only this disposable local app:

```text
${TMPDIR}/walite-click-notification-prototype/Walite Notification Prototype.app
```

The posting script writes the current `ITERM_SESSION_ID` to a private-mode
request file in the same directory and opens that file with the prototype app.
No title, message body, or command is interpolated into AppleScript source.

After the notification appears:

1. Move to another iTerm2 tab or window.
2. Minimize the window containing the original test session.
3. Click **Walite click prototype** in Notification Center.
4. Confirm the exact original window is unminimized and its original tab and
   session are selected.
5. Confirm no new terminal window and no walite process were started.

For a multiple-window check, repeat the test from a different iTerm2 window.
Each posting should capture the session belonging to the shell that ran the
posting command.

If no notification appears, enable notifications for **Walite Notification
Prototype** in macOS System Settings and post it again. Automation permission
for controlling iTerm2 may also be requested on the first click.

## Expected limitation of this first prototype

The app stores one target session UUID, so the latest post replaces the prior
target. This is sufficient to validate click activation for one running walite
instance. Production integration should not proceed until the click/relaunch
behavior is confirmed on the target Mac.
