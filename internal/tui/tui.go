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
func Run(ctx context.Context, screen tcell.Screen, options Options) error {
	if ctx == nil || screen == nil {
		return errors.New("tui rejected")
	}
	if err := options.validate(); err != nil {
		return err
	}
	preferencesPath := ""
	if path, err := defaultPreferencesPath(); err == nil {
		preferencesPath = path
	}
	return runWithDependencies(ctx, screen, options, preferencesPath)
}

func runWithPreferences(ctx context.Context, screen tcell.Screen, preferencesPath string) error {
	return runWithDependencies(ctx, screen, DefaultOptions(), preferencesPath)
}

func runWithDependencies(
	ctx context.Context,
	screen tcell.Screen,
	options Options,
	preferencesPath string,
) error {
	if ctx == nil || screen == nil {
		return errors.New("tui rejected")
	}
	if err := options.validate(); err != nil {
		return err
	}
	if err := screen.Init(); err != nil {
		return err
	}

	screen.HideCursor()
	model := viewModel{chats: newDemoChatState(), options: options}
	if selectedChat, ok := model.chats.selectedChat(); ok {
		model.chatView.unreadBoundary = unreadBoundaryForChat(selectedChat)
		selectedChat.unreadCount = 0
	}
	if preferencesPath != "" {
		model.preferencesPath = preferencesPath
		_ = loadEmojiPreferences(preferencesPath, &model.emojiPicker)
	}
	defer screen.Fini()
	draw(screen, &model)
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
			switch event := event.(type) {
			case *tcell.EventKey:
				width, height := screen.Size()
				changed, exit := handleKey(&model, event, width, height)
				if exit {
					return nil
				}
				if changed {
					draw(screen, &model)
					screen.Show()
				}
			case *tcell.EventResize:
				screen.Sync()
				width, height := screen.Size()
				clampView(&model, width, height)
				draw(screen, &model)
				screen.Show()
			}
		}
	}
}
