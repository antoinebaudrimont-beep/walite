package tui

import (
	"errors"
	"fmt"
)

const (
	ThemeTerminal     = "terminal"
	ThemeDark         = "dark"
	ThemeLight        = "light"
	ThemeHighContrast = "high_contrast"
	ThemeDefault      = ThemeTerminal
)

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
	if options.Theme != ThemeTerminal && options.Theme != ThemeDark && options.Theme != ThemeLight && options.Theme != ThemeHighContrast {
		return fmt.Errorf("%w: unsupported theme %q", ErrInvalidOptions, options.Theme)
	}
	return nil
}
