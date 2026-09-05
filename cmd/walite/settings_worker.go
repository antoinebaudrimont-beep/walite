package main

import (
	"context"
	"sync"

	"github.com/antoinebaudrimont-beep/walite/internal/config"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/gdamore/tcell/v2"
)

// settingsWorker owns config I/O. Exactly one save may be outstanding, including
// its unread result. Explicitly accepted saves finish on shutdown; no retry or
// goroutine per setting is created.
type settingsWorker struct {
	store    config.UIStore
	requests chan tui.Options
	results  chan error
	done     chan struct{}
	mu       sync.Mutex
	busy     bool
	stopped  bool
}

func newSettingsWorker(store config.UIStore) *settingsWorker {
	w := &settingsWorker{store: store, requests: make(chan tui.Options, 1), results: make(chan error, 1), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		for options := range w.requests {
			err := store.Save(config.UI{Theme: config.Theme(options.Theme), ShowTimestamps: options.ShowTimestamps,
				ConfirmQuit: options.ConfirmQuit, SendReadReceipts: options.SendReadReceipts})
			w.mu.Lock()
			w.results <- err
			w.busy = false
			w.mu.Unlock()
		}
	}()
	return w
}

func (w *settingsWorker) admit(options tui.Options) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped || w.busy || len(w.results) != 0 {
		return false
	}
	w.busy = true
	w.requests <- options
	return true
}

func (w *settingsWorker) stop() {
	w.mu.Lock()
	if !w.stopped {
		w.stopped = true
		close(w.requests)
	}
	w.mu.Unlock()
	<-w.done
}

func runConfiguredTUI(ctx context.Context, screen tcell.Screen, input tui.Input, store config.UIStore,
	runTUI func(context.Context, tcell.Screen, tui.Input) error) error {
	worker := newSettingsWorker(store)
	defer worker.stop()
	input.SaveOptions = worker.admit
	input.OptionsResults = worker.results
	return runTUI(ctx, screen, input)
}
