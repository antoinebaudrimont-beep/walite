package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/gdamore/tcell/v2"
)

var version = "dev"

const commandHelp = `Usage:
  walite             Start walite
  walite --pair      Link a WhatsApp account
  walite --version   Print the walite version
  walite --help      Show this help
`

func main() { os.Exit(runMain()) }

func runMain() int {
	if handled, code := handleMainArguments(os.Args[1:], os.Stdout, os.Stderr); handled {
		return code
	}
	ctx, stop := terminationContext(context.Background())
	defer stop()
	var err error
	if len(os.Args) == 2 && os.Args[1] == pairingHelperFlag {
		err = runPairMode(ctx)
	} else {
		var screen tcell.Screen
		screen, err = tcell.NewScreen()
		if err == nil {
			err = run(ctx, screen)
		}
	}
	return mainResult(os.Stderr, err)
}

func terminationSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGHUP, syscall.SIGTERM}
}

func terminationContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, terminationSignals()...)
}

func mainResult(destination io.Writer, err error) int {
	if err == nil || errors.Is(err, context.Canceled) {
		return 0
	}
	return reportMainError(destination, err)
}

func handleMainArguments(args []string, stdout, stderr io.Writer) (bool, int) {
	if len(args) == 0 || (len(args) == 1 && args[0] == pairingHelperFlag) {
		return false, 0
	}
	if len(args) == 1 {
		switch args[0] {
		case "--help", "-h":
			_, _ = fmt.Fprint(stdout, commandHelp)
			return true, 0
		case "--version":
			_, _ = fmt.Fprintf(stdout, "walite %s\n", version)
			return true, 0
		}
	}
	_, _ = fmt.Fprintln(stderr, "walite: unsupported arguments")
	_, _ = fmt.Fprint(stderr, commandHelp)
	return true, 2
}

func reportMainError(destination io.Writer, err error) int {
	var pairingErr *pairingWindowError
	if errors.As(err, &pairingErr) {
		_, _ = fmt.Fprintln(destination, pairingErr.UserMessage())
		return 1
	}
	// Application and transport boundaries expose content-free wrapped errors.
	// Keep that operation context instead of hiding the only startup diagnostic.
	_, _ = fmt.Fprintf(destination, "walite failed: %v\n", err)
	return 1
}
