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
func Run(ctx context.Context, screen tcell.Screen, input Input) error {
	if ctx == nil || screen == nil {
		return errors.New("tui rejected")
	}
	if err := input.Options.validate(); err != nil {
		return err
	}
	preferencesPath := ""
	if path, err := defaultPreferencesPath(); err == nil {
		preferencesPath = path
	}
	return runWithDependencies(ctx, screen, input, preferencesPath)
}

// RunInitialized displays the normal chat interface on a screen whose
// lifecycle is owned by the caller. It neither initializes nor finalizes the
// screen.
func RunInitialized(ctx context.Context, screen tcell.Screen, input Input) error {
	if ctx == nil || screen == nil {
		return errors.New("tui rejected")
	}
	if err := input.Options.validate(); err != nil {
		return err
	}
	preferencesPath := ""
	if path, err := defaultPreferencesPath(); err == nil {
		preferencesPath = path
	}
	return runInitializedWithDependencies(ctx, screen, input, preferencesPath)
}

func runWithDependencies(
	ctx context.Context,
	screen tcell.Screen,
	input Input,
	preferencesPath string,
) error {
	if ctx == nil || screen == nil {
		return errors.New("tui rejected")
	}
	if err := input.Options.validate(); err != nil {
		return err
	}
	chats, err := chatStateFromInitial(input.InitialState)
	if err != nil {
		return err
	}
	if err := screen.Init(); err != nil {
		return err
	}
	defer screen.Fini()
	return runInitialized(ctx, screen, input, preferencesPath, chats)
}

func runInitializedWithDependencies(
	ctx context.Context,
	screen tcell.Screen,
	input Input,
	preferencesPath string,
) error {
	if ctx == nil || screen == nil {
		return errors.New("tui rejected")
	}
	if err := input.Options.validate(); err != nil {
		return err
	}
	chats, err := chatStateFromInitial(input.InitialState)
	if err != nil {
		return err
	}
	return runInitialized(ctx, screen, input, preferencesPath, chats)
}

func runInitialized(
	ctx context.Context,
	screen tcell.Screen,
	input Input,
	preferencesPath string,
	chats *chatState,
) error {
	screen.HideCursor()
	model := viewModel{chats: chats, options: input.Options}
	model.asyncSend = input.SendResults != nil
	model.media, model.closeMedia = input.Media, input.CloseMedia
	model.closeExternalPreview = input.CloseExternalPreview
	model.loadOlder = input.OlderHistory
	defer closeMedia(&model)
	if input.Send != nil {
		model.send = func(request SendRequest) error { return input.Send(ctx, request) }
	}
	if selectedChat, ok := model.chats.selectedChat(); ok {
		model.chatView.unreadBoundary = unreadBoundaryForChat(selectedChat)
		if selectedChat.unreadCount > 0 {
			model.localReadRequest = LocalReadRequest{ChatID: selectedChat.id, ActivityTime: selectedChat.activityTime}
		}
		selectedChat.unreadCount = 0
	}
	if preferencesPath != "" {
		model.preferencesPath = preferencesPath
		_ = loadEmojiPreferences(preferencesPath, &model.emojiPicker)
	}
	draw(screen, &model)
	screen.Show()
	requestPendingLocalRead(&model, input.PersistLocalRead)
	requestPendingReadReceipt(&model, input.SendReadReceipt)
	requestSelectedChatLoad(&model, input.LoadChat)

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
	liveEvents := input.LiveEvents
	sendResults := input.SendResults
	displayUpdates := input.DisplayUpdates
	summaryUpdates := input.SummaryUpdates
	chatLoads := input.ChatLoads
	optionsResults := input.OptionsResults
	mediaResults := input.MediaResults
	olderResults := input.OlderResults

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err, ok := <-optionsResults:
			if !ok {
				optionsResults = nil
				err = errors.New("settings persistence unavailable")
			}
			if finishSettingsSave(&model, err) {
				draw(screen, &model)
				screen.Show()
			}
		case value, ok := <-displayUpdates:
			if !ok {
				displayUpdates = nil
				continue
			}
			if applyDisplayMetadata(&model, value) {
				draw(screen, &model)
				screen.Show()
			}
		case update, ok := <-summaryUpdates:
			if !ok {
				summaryUpdates = nil
				continue
			}
			changed := applyChatSummaries(&model, update)
			requestPendingLocalRead(&model, input.PersistLocalRead)
			if changed {
				requestSelectedChatLoad(&model, input.LoadChat)
				draw(screen, &model)
				screen.Show()
			}
		case result, ok := <-chatLoads:
			if !ok {
				chatLoads = nil
				continue
			}
			if applyChatLoad(&model, result) {
				requestPendingReadReceipt(&model, input.SendReadReceipt)
				draw(screen, &model)
				screen.Show()
			}
		case result, ok := <-sendResults:
			if !ok {
				sendResults = nil
				continue
			}
			if applySendResult(&model, result) {
				draw(screen, &model)
				screen.Show()
			}
		case result, ok := <-mediaResults:
			if !ok {
				mediaResults = nil
				continue
			}
			if applyMediaResult(&model, result) {
				draw(screen, &model)
				screen.Show()
			}
		case result, ok := <-olderResults:
			if !ok {
				olderResults = nil
				continue
			}
			if applyOlderHistoryResult(&model, result) {
				draw(screen, &model)
				screen.Show()
			}
		case event, ok := <-liveEvents:
			if !ok {
				liveEvents = nil
				continue
			}
			if applyLiveMessage(&model, event) {
				draw(screen, &model)
				screen.Show()
			}
		case event, ok := <-events:
			if !ok {
				return nil
			}
			switch event := event.(type) {
			case *tcell.EventKey:
				width, height := screen.Size()
				selectedBefore := ""
				if selected, ok := model.chats.selectedChat(); ok {
					selectedBefore = selected.id
				}
				changed, exit := handleKey(&model, event, width, height)
				requestSettingsSave(&model, input.SaveOptions)
				requestPendingLocalRead(&model, input.PersistLocalRead)
				requestPendingReadReceipt(&model, input.SendReadReceipt)
				if exit {
					return nil
				}
				if changed {
					if selected, ok := model.chats.selectedChat(); ok && selected.id != selectedBefore {
						requestSelectedChatLoad(&model, input.LoadChat)
					}
					draw(screen, &model)
					screen.Show()
				}
			case *tcell.EventResize:
				closeMedia(&model)
				screen.Sync()
				width, height := screen.Size()
				clampView(&model, width, height)
				draw(screen, &model)
				screen.Show()
			}
		}
	}
}

func requestPendingLocalRead(model *viewModel, persist func(LocalReadRequest) bool) {
	if model == nil || model.localReadRequest.ChatID == "" {
		return
	}
	if persist == nil || persist(model.localReadRequest) {
		model.localReadRequest = LocalReadRequest{}
	}
}
