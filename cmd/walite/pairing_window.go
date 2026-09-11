package main

import (
	"context"
	"errors"
	"os/exec"

	"github.com/gdamore/tcell/v2"
)

const (
	pairingTerminalCommand  = "xfce4-terminal"
	pairingTerminalGeometry = "80x46"
	pairingTerminalFont     = "Monospace 8"
	pairingWindowTitle      = "walite — WhatsApp setup"
	pairingHelperFlag       = "--pair"
	pairingFallbackMessage  = "WhatsApp pairing requires a larger terminal grid.\nRun:\n    walite --pair"
)

var (
	errPairingSessionUnlinked = errors.New("WhatsApp session remains unlinked")
	errPairingNotRequired     = errors.New("WhatsApp session is already linked")
)

type pairingWindowError struct{ cause error }

func (err *pairingWindowError) Error() string { return "pairing window failed" }
func (err *pairingWindowError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}
func (*pairingWindowError) UserMessage() string { return pairingFallbackMessage }

type pairingStartupDependencies struct {
	sessionLinked func(context.Context) (bool, error)
	launchPairing func(context.Context) error
	runLinked     func(context.Context, tcell.Screen) error
}

func runWithPairingWindow(
	ctx context.Context,
	screen tcell.Screen,
	dependencies pairingStartupDependencies,
) error {
	if ctx == nil || screen == nil || dependencies.sessionLinked == nil ||
		dependencies.launchPairing == nil || dependencies.runLinked == nil {
		return errors.New("pairing startup rejected")
	}
	linked, err := dependencies.sessionLinked(ctx)
	if err != nil {
		return err
	}
	if !linked {
		if err := dependencies.launchPairing(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return &pairingWindowError{cause: err}
		}
		linked, err = dependencies.sessionLinked(ctx)
		if err != nil {
			return err
		}
		if !linked {
			return &pairingWindowError{cause: errPairingSessionUnlinked}
		}
	}
	return dependencies.runLinked(ctx, screen)
}

type pairingCommandRunner func(context.Context, string, ...string) error

func launchXFCEPairingWindow(
	ctx context.Context,
	executable string,
	runCommand pairingCommandRunner,
) error {
	return launchPairingTerminal(ctx, pairingTerminalCommand, executable, runCommand)
}

func launchPairingTerminal(
	ctx context.Context,
	command string,
	executable string,
	runCommand pairingCommandRunner,
) error {
	if ctx == nil || command == "" || executable == "" || runCommand == nil {
		return errors.New("pairing launcher rejected")
	}
	return runCommand(ctx, command, pairingTerminalArguments(executable)...)
}

func pairingTerminalArguments(executable string) []string {
	return []string{
		"--disable-server",
		"--geometry=" + pairingTerminalGeometry,
		"--font=" + pairingTerminalFont,
		"--title=" + pairingWindowTitle,
		"--hide-menubar",
		"--hide-toolbar",
		"--hide-scrollbar",
		"--execute",
		executable,
		pairingHelperFlag,
	}
}

func runPairingCommand(ctx context.Context, name string, arguments ...string) error {
	return exec.CommandContext(ctx, name, arguments...).Run()
}
