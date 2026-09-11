package main

import (
	"context"
	"errors"
	"os/exec"
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

type platformCapabilities struct {
	pairing       pairingPresenter
	opener        systemOpener
	clipboard     clipboard
	inlineImage   mediaPreviewer
	newExternal   func() externalMediaPreviewer
	mediaFallback bool
	systemPDF     bool
	platformError error
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

// defaultPlatformCapabilities is the only runtime OS selection point in the
// executable. Optional tools are probed independently and never gate startup.
func defaultPlatformCapabilities() platformCapabilities {
	return selectPlatformCapabilities(runtime.GOOS, exec.LookPath)
}

func selectPlatformCapabilities(goos string, lookup executableLookup) platformCapabilities {
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
		if command, err := lookup("osascript"); err == nil {
			capabilities.pairing = darwinPairingPresenter{command: command, run: runPairingCommand}
		}
		if command, err := lookup("open"); err == nil {
			capabilities.opener = commandSystemOpener{command: command, run: runLinkCommand}
			capabilities.mediaFallback = true
			capabilities.systemPDF = true
		}
		if command, err := lookup("pbcopy"); err == nil {
			capabilities.clipboard = commandClipboard{command: command, run: runLinkCommand}
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
	return capabilities
}
