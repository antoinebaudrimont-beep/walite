// Package tui owns walite's terminal lifecycle and rendering.
package tui

import (
	"context"
	"errors"

	"github.com/gdamore/tcell/v2"
)

const title = "walite"

// Run initializes screen, displays the first frame, and processes terminal
// events until the context is canceled or the user requests exit. Run always
// finalizes a successfully initialized screen before returning.
func Run(ctx context.Context, screen tcell.Screen) error {
	if ctx == nil || screen == nil {
		return errors.New("tui rejected")
	}
	if err := screen.Init(); err != nil {
		return err
	}
	defer screen.Fini()

	screen.Clear()
	screen.HideCursor()
	screen.PutStr(0, 0, title)
	screen.Show()

	events := make(chan tcell.Event, 1)
	stopEvents := make(chan struct{})
	eventsDone := make(chan struct{})
	go func() {
		defer close(eventsDone)
		screen.ChannelEvents(events, stopEvents)
	}()
	defer func() {
		close(stopEvents)
		<-eventsDone
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-events:
			if !ok {
				return nil
			}
			key, ok := event.(*tcell.EventKey)
			if ok && (key.Key() == tcell.KeyEscape || key.Key() == tcell.KeyCtrlC) {
				return nil
			}
		}
	}
}
