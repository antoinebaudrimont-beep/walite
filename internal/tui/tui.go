// Package tui owns walite's terminal lifecycle and rendering.
package tui

import (
	"context"
	"errors"
	"fmt"

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
	store, err := newDefaultChatStateStore()
	if err != nil {
		return fmt.Errorf("chat state store: %w", err)
	}
	return runWithDependencies(ctx, screen, options, preferencesPath, store)
}

func runWithPreferences(ctx context.Context, screen tcell.Screen, preferencesPath string) error {
	return runWithDependencies(ctx, screen, DefaultOptions(), preferencesPath, nil)
}

func runWithStore(ctx context.Context, screen tcell.Screen, preferencesPath string, store ChatStateStore) (runErr error) {
	return runWithDependencies(ctx, screen, DefaultOptions(), preferencesPath, store)
}

func runWithDependencies(
	ctx context.Context,
	screen tcell.Screen,
	options Options,
	preferencesPath string,
	store ChatStateStore,
) (runErr error) {
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
	state, err := loadChatStateOrDemo(store)
	if err != nil {
		screen.Fini()
		return fmt.Errorf("load chat state: %w", err)
	}
	model := viewModel{chats: state, options: options}
	if selectedChat, ok := model.chats.selectedChat(); ok {
		model.chatView.unreadBoundary = unreadBoundaryForChat(selectedChat)
		selectedChat.unreadCount = 0
	}
	if preferencesPath != "" {
		model.preferencesPath = preferencesPath
		_ = loadEmojiPreferences(preferencesPath, &model.emojiPicker)
	}
	if store != nil {
		defer func() {
			if err := store.Save(model.chats); err != nil {
				saveErr := fmt.Errorf("save chat state: %w", err)
				if runErr == nil {
					runErr = saveErr
				} else {
					runErr = errors.Join(runErr, saveErr)
				}
			}
		}()
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
