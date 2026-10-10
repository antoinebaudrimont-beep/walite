package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/antoinebaudrimont-beep/walite/internal/tui"
)

var errCapabilityUnavailable = errors.New("platform capability unavailable")

// pairingPresenter owns the platform-specific mechanism used to display a QR
// at a readable size. Application startup only depends on this purpose, not on
// a particular terminal emulator.
type pairingPresenter interface {
	Present(context.Context, string) error
}

// systemOpener delegates one URL or local file to the user's default handler.
type systemOpener interface {
	Open(context.Context, string) error
}

// clipboard copies exact text without exposing a shell command to callers.
type clipboard interface {
	Copy(context.Context, string) error
}

type desktopNotifier interface {
	Notify(context.Context, tui.Notification) error
}

type platformCapabilities struct {
	pairing           pairingPresenter
	opener            systemOpener
	clipboard         clipboard
	notifier          desktopNotifier
	newChatActivation func(context.Context) (*chatActivationReceiver, error)
	inlineImage       mediaPreviewer
	newExternal       func() externalMediaPreviewer
	systemMedia       bool
	platformError     error
}

type commandPairingPresenter struct {
	command string
	run     pairingCommandRunner
}

func (presenter commandPairingPresenter) Present(ctx context.Context, executable string) error {
	return launchPairingTerminal(ctx, presenter.command, executable, presenter.run)
}

type unavailablePairingPresenter struct{ cause error }

func (presenter unavailablePairingPresenter) Present(context.Context, string) error {
	return errors.Join(errCapabilityUnavailable, presenter.cause)
}

type darwinPairingPresenter struct {
	command string
	run     pairingCommandRunner
}

func (presenter darwinPairingPresenter) Present(ctx context.Context, executable string) error {
	return launchDarwinPairingWindow(ctx, presenter.command, executable, presenter.run)
}

type commandSystemOpener struct {
	command string
	run     linkCommandRunner
}

func (opener commandSystemOpener) Open(ctx context.Context, target string) error {
	if opener.command == "" || opener.run == nil {
		return errCapabilityUnavailable
	}
	return opener.run(ctx, opener.command, []string{target}, nil)
}

type commandClipboard struct {
	command string
	args    []string
	run     linkCommandRunner
}

func (clipboard commandClipboard) Copy(ctx context.Context, value string) error {
	if clipboard.command == "" || clipboard.run == nil {
		return errCapabilityUnavailable
	}
	return clipboard.run(ctx, clipboard.command, append([]string(nil), clipboard.args...), strings.NewReader(value))
}

type unavailableInlinePreviewer struct{}

func (unavailableInlinePreviewer) Show(context.Context, string, tui.MediaRequest) error {
	return errCapabilityUnavailable
}

func (unavailableInlinePreviewer) Close() error { return nil }

type executableLookup func(string) (string, error)

const darwinNotificationHelperAppName = "Walite Notifications.app"

type platformCapabilityOptions struct {
	darwinNotificationHelperPath string
	linuxNotificationIconPath    string
	notificationRunner           notificationCommandRunner
}

// defaultPlatformCapabilities is the only runtime OS selection point in the
// executable. Optional tools are probed independently and never gate startup.
func defaultPlatformCapabilities() platformCapabilities {
	options := platformCapabilityOptions{notificationRunner: runNotificationCommand}
	if runtime.GOOS == "darwin" {
		if home, err := os.UserHomeDir(); err == nil {
			options.darwinNotificationHelperPath = filepath.Join(home, "Applications", darwinNotificationHelperAppName)
		}
	} else if runtime.GOOS == "linux" {
		options.linuxNotificationIconPath = defaultLinuxNotificationIcon()
	}
	return selectPlatformCapabilitiesWithOptions(runtime.GOOS, exec.LookPath, options)
}

func selectPlatformCapabilities(goos string, lookup executableLookup) platformCapabilities {
	return selectPlatformCapabilitiesWithOptions(goos, lookup, platformCapabilityOptions{
		notificationRunner: runNotificationCommand,
	})
}

func selectPlatformCapabilitiesWithOptions(
	goos string,
	lookup executableLookup,
	options platformCapabilityOptions,
) platformCapabilities {
	if options.notificationRunner == nil {
		options.notificationRunner = runNotificationCommand
	}
	capabilities := platformCapabilities{
		pairing:     unavailablePairingPresenter{cause: errors.New("pairing presenter unavailable")},
		inlineImage: unavailableInlinePreviewer{},
		newExternal: func() externalMediaPreviewer { return newProcessExternalPreviewer() },
	}
	if lookup == nil {
		capabilities.platformError = errors.New("capability probe unavailable")
		return capabilities
	}
	if goos == "darwin" {
		var fallback desktopNotifier
		if command, err := lookup("osascript"); err == nil {
			capabilities.pairing = darwinPairingPresenter{command: command, run: runPairingCommand}
			fallback = darwinNotificationBackend{command: command, run: options.notificationRunner}
			capabilities.notifier = fallback
		}
		openAvailable := false
		if command, err := lookup("open"); err == nil {
			capabilities.opener = commandSystemOpener{command: command, run: runLinkCommand}
			capabilities.systemMedia = true
			openAvailable = true
		}
		if command, err := lookup("pbcopy"); err == nil {
			capabilities.clipboard = commandClipboard{command: command, run: runLinkCommand}
		}
		if openAvailable && options.darwinNotificationHelperPath != "" {
			helper, err := newDarwinHelperNotificationBackend(
				options.darwinNotificationHelperPath,
				options.notificationRunner,
			)
			if err == nil {
				capabilities.newChatActivation = newChatActivationReceiver
				capabilities.notifier = helper
				if fallback != nil {
					capabilities.notifier = fallbackNotificationBackend{preferred: helper, fallback: fallback}
				}
			}
		}
		return capabilities
	}
	if goos != "linux" {
		capabilities.platformError = errors.New("platform capabilities are not implemented")
		return capabilities
	}
	if command, err := lookup(pairingTerminalCommand); err == nil {
		capabilities.pairing = commandPairingPresenter{command: command, run: runPairingCommand}
	}
	if command, err := lookup("xdg-open"); err == nil {
		capabilities.opener = commandSystemOpener{command: command, run: runLinkCommand}
	}
	if command, err := lookup("xclip"); err == nil {
		capabilities.clipboard = commandClipboard{command: command, args: []string{"-selection", "clipboard", "-in"}, run: runLinkCommand}
	} else if command, err := lookup("xsel"); err == nil {
		capabilities.clipboard = commandClipboard{command: command, args: []string{"--clipboard", "--input"}, run: runLinkCommand}
	}
	if command, err := lookup("ueberzugpp"); err == nil {
		capabilities.inlineImage = &ueberzugPreviewer{binary: command}
	}
	if command, err := lookup("notify-send"); err == nil {
		capabilities.notifier = notifySendBackend{command: command, iconPath: options.linuxNotificationIconPath, run: options.notificationRunner}
	}
	return capabilities
}
