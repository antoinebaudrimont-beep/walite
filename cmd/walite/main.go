package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/antoinebaudrimont-beep/walite/internal/config"
)

func main() { os.Exit(runMain()) }

func runMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	scenario, err := newDemoScenario(config.DefaultValues())
	if err == nil {
		err = runDemo(ctx, os.Stdout, scenario)
	}
	if err == nil || errors.Is(err, context.Canceled) {
		return 0
	}
	_, _ = fmt.Fprintln(os.Stderr, "walite demo failed")
	return 1
}
