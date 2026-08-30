package main

import (
	"context"
	"errors"
	"fmt"
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
	var pairingErr *pairingWindowError
	if errors.As(err, &pairingErr) {
		_, _ = fmt.Fprintln(os.Stderr, pairingErr.UserMessage())
		return 1
	}
	_, _ = fmt.Fprintln(os.Stderr, "walite failed")
	return 1
}
