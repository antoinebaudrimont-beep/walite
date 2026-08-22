package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/gdamore/tcell/v2"
)

func main() { os.Exit(runMain()) }

func runMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	screen, err := tcell.NewScreen()
	if err == nil {
		err = tui.Run(ctx, screen)
	}
	if err == nil || errors.Is(err, context.Canceled) {
		return 0
	}
	_, _ = fmt.Fprintln(os.Stderr, "walite failed")
	return 1
}
