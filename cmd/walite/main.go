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

func main() { os.Exit(runMain()) }

func runMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
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
	if err == nil || errors.Is(err, context.Canceled) {
		return 0
	}
	return reportMainError(os.Stderr, err)
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
