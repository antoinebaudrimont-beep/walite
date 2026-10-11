# Installing, upgrading and rolling back walite v1.2

These are manual, code-only procedures. Never delete, move, restore from backup,
or reset WhatsApp session databases, the SQLite cache, configuration, preferences
or macOS permission records as part of installation. Do not re-pair an existing
linked account. Quit Walite before switching binaries. No installer runs at startup.

## Verify first

Download the matching archive and `SHA256SUMS`. Use a new empty extraction
directory, not an existing installation. For a Linux amd64 candidate:

```sh
grep 'walite_v1.2.0-rc1_linux_amd64.tar.gz$' SHA256SUMS | sha256sum -c -
walite_stage=$(mktemp -d /tmp/walite-install-check.XXXXXX)
tar -xzf walite_v1.2.0-rc1_linux_amd64.tar.gz -C "$walite_stage"
"$walite_stage/walite" --version
"$walite_stage/walite" --help
```

On macOS use `shasum -a 256 -c -` instead, selecting `darwin_arm64` for Apple
Silicon or `darwin_amd64` for Intel. The expected version is `walite v1.2.0-rc1`.
These commands do not pair or connect to WhatsApp. Checksums detect corruption;
they are not a Developer ID signature or notarization.
Stop if any verification fails; do not continue with extraction or installation.

## Linux: keep executable and icon together

The four top-level files are `walite`, `walite-notification.png`, `README.md`
and `LICENSE`. The PNG is the official dedicated 48×48 notification artwork.
Do not install only the executable into a different directory and leave its
icon behind. An alternative explicit icon installation is an existing standard
XDG location such as `~/.local/share/icons/hicolor/48x48/apps/walite-notification.png`;
ordinary Walite startup never writes system icons.

After verifying, install into a new versioned directory, for example
`~/.local/opt/walite/v1.2.0-rc1`. Refuse an existing destination. Copy all four
files, preserve executable mode 0755 and file mode 0644, and run the copied
`--version` and `--help` again before changing your shell's launch command.
Keep the previous installation intact. If an existing PATH entry is a symlink,
record its old target before explicitly repointing it; if it is a real binary,
make a verified backup first rather than blindly overwriting it.

Core messaging does not need desktop helpers. Notifications optionally use
`notify-send` (`libnotify-bin`). Click actions additionally need an X11 session,
a valid inherited `WINDOWID`, `wmctrl`, `xprop`, `gdbus` and an action-capable
daemon. XFCE and i3/Dunst have prior manual validation. Use the daemon's **Open**
action; a body click may dismiss the notification. The requested expiry is
15 seconds, but daemon policy/hovering can affect visibility. Restoration
targets the owning window, not a particular terminal tab. Missing capabilities
retain ordinary notifications. Nothing is installed automatically.

Rollback: quit Walite and restore your previous launch command/symlink or use
the previous versioned binary directly. Keep both versions until acceptance.
Do not roll back or delete user data as part of the code rollback.

## macOS: native helper at one stable location

The archive contains `walite`, `README.md`, `LICENSE` and
`Walite Notifications.app`. The executable and helper are specific to the
selected architecture. For Apple Silicon, prepare a fresh stage below (substitute
`darwin_amd64` for Intel), then verify before opening any candidate helper:

```sh
grep 'walite_v1.2.0-rc1_darwin_arm64.tar.gz$' SHA256SUMS | shasum -a 256 -c -
walite_stage=$(mktemp -d /private/tmp/walite-install-check.XXXXXX)
tar -xzf walite_v1.2.0-rc1_darwin_arm64.tar.gz -C "$walite_stage"
"$walite_stage/walite" --version
"$walite_stage/walite" --help
/usr/bin/plutil -lint "$walite_stage/Walite Notifications.app/Contents/Info.plist"
/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' \
  "$walite_stage/Walite Notifications.app/Contents/Info.plist"
/usr/bin/codesign --verify --strict "$walite_stage/Walite Notifications.app"
/usr/bin/lipo -archs "$walite_stage/Walite Notifications.app/Contents/MacOS/WaliteNotifier"
```

The identifier must remain `io.github.antoinebaudrimontbeep.walite.notifier`;
architecture must be `arm64` or `x86_64`, matching the archive. Helper display
version is `1.2.0`, build `4` for this candidate. The embedded `Walite.icns`
comes from the original official PNG. Keep the helper installed at exactly:

```text
~/Applications/Walite Notifications.app
```

For a first installation, create `~/Applications` if missing, refuse an existing
helper, and copy the verified app with macOS `ditto`, preserving the whole bundle.
Keep the Walite binary in a new versioned directory as on Linux. No Homebrew,
Swift compiler, iTerm2 or third-party notification utility is needed to use the
binary or ordinary notification fallback. Click-to-chat requires a valid iTerm2
session; other terminals retain ordinary `osascript` notifications.

### Replace an existing helper safely

1. Verify the staged candidate and record the installed helper's bundle ID,
   build/version, signature and hashes. Do not launch/register the staged copy.
2. Create a private backup directory outside Applications, preserving the
   previous binary and helper. Use a verified archive or a `.bundle` backup,
   **not a second `.app` registered with Launch Services**. Retain older backups.
3. Quit all running Walite instances before stopping the helper. In Activity
   Monitor, identify **WaliteNotifier** by its exact executable path and quit
   the installed helper. Verify no old/test helper copy is still running. Do
   not use broad `killall` commands or terminate unrelated system apps.
4. Move the stopped old helper into the private backup as
   `Walite Notifications.bundle`. Copy the candidate with `ditto` into a fresh
   staging path under `~/Applications`, verify that copy's strict signature,
   then move it to `~/Applications/Walite Notifications.app`. If any step fails,
   put the backup back at the original path before starting anything.
5. Verify the final installed app again. Start only this exact path when needed;
   do not open multiple copies with the same bundle ID. Switch the Walite launch
   command to the verified new version only after installation is complete.
6. macOS may ask for notifications and iTerm2 Automation permission. Keeping
   the identifier and path stable helps continuity but **ad hoc signatures
   cannot guarantee permission preservation across updates**. Do not reset
   permissions, delete permission databases, disable Gatekeeper, strip quarantine
   globally, or re-pair WhatsApp. Use the OS's per-app **Open Anyway** approval
   if it blocks a launch. Both executable/helper lack Developer ID notarization.
7. Test fresh notifications: exact minimized window/tab/session, A/B chat
   targeting, draft protection, mute/previews and unavailable IPC. Scheduling
   acceptance is not proof of visible delivery or chat selection.

Rollback: quit the new Walite/helper, move the failed app to a separate non-`.app`
backup, restore the verified previous bundle at the original Applications path,
verify its signature, and restore the prior Walite binary/launch command. Never
modify session/cache/configuration databases as part of rollback. Code rollback
does not undo user-data writes made during normal application use.

Helper activation targets are in memory and disappear when it quits. Previously
delivered notifications cannot validate a newly installed helper's chat targeting;
generate fresh notifications. Logout/reboot, revoked permissions, helper relaunch,
multiple simultaneous live instances and every real-account mute/settings case
still need acceptance on the packaged candidate. No such acceptance is claimed
from a Linux build.
