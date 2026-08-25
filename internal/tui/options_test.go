package tui

import (
	"context"
	"errors"
	"testing"
)

func TestDefaultOptions(t *testing.T) {
	want := Options{Theme: ThemeDefault, ShowTimestamps: true, ConfirmQuit: false}
	if got := DefaultOptions(); got != want {
		t.Fatalf("DefaultOptions()=%+v want=%+v", got, want)
	}
}

func TestRunRejectsInvalidOptionsBeforeScreenInitialization(t *testing.T) {
	screen := newObservedScreen()
	err := Run(context.Background(), screen, Options{Theme: "unsupported"})
	if !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("Run=%v", err)
	}
	select {
	case <-screen.finalized:
		t.Fatal("screen finalized even though options failed before initialization")
	default:
	}
}
