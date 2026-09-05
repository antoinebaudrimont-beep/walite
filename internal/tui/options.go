package tui

import (
	"errors"
	"fmt"
)

const ThemeDefault = "default"

var ErrInvalidOptions = errors.New("invalid TUI options")

// Options contains user-facing values supplied by application
// wiring. It owns no persistence and performs no I/O.
type Options struct {
	Theme            string
	ShowTimestamps   bool
	ConfirmQuit      bool
	SendReadReceipts bool
}

// DefaultOptions returns the initial terminal preferences.
func DefaultOptions() Options {
	return Options{
		Theme:            ThemeDefault,
		ShowTimestamps:   true,
		ConfirmQuit:      false,
		SendReadReceipts: true,
	}
}

func (options Options) validate() error {
	if options.Theme != ThemeDefault {
		return fmt.Errorf("%w: unsupported theme %q", ErrInvalidOptions, options.Theme)
	}
	return nil
}
