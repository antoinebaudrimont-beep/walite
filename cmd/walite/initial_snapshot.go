package main

import (
	"context"
	"errors"
	"sort"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/service"
	"github.com/antoinebaudrimont-beep/walite/internal/store"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
)

type offlineApplicationService struct {
	core  *service.Core
	store *store.Memory
	chats []model.Chat
}

func (application *offlineApplicationService) Run(ctx context.Context) error {
	return application.core.Run(ctx)
}

func (application *offlineApplicationService) Updates() <-chan model.Update {
	return application.core.Updates()
}

func (application *offlineApplicationService) LiveEvents() <-chan model.LiveEvent {
	return application.core.LiveEvents()
}

func (application *offlineApplicationService) InitialChats(ctx context.Context, limit int) ([]model.Chat, error) {
	if ctx == nil || ctx.Err() != nil || limit <= 0 {
		return nil, errors.New("initial chats rejected")
	}
	if limit > len(application.chats) {
		limit = len(application.chats)
	}
	return append([]model.Chat(nil), application.chats[:limit]...), nil
}

func (application *offlineApplicationService) InitialMessages(ctx context.Context, chatID model.ChatID, limit int) ([]model.Message, error) {
	messages, _, err := application.store.Page(ctx, chatID, model.NoCursor(), limit)
	return messages, err
}

func buildInitialTUIState(ctx context.Context, source applicationService) (tui.InitialState, error) {
	chats, err := source.InitialChats(ctx, tui.MaxInitialChats)
	if err != nil {
		return tui.InitialState{}, err
	}
	if len(chats) > tui.MaxInitialChats {
		return tui.InitialState{}, errors.New("initial chat snapshot exceeded bound")
	}
	sort.Slice(chats, func(i, j int) bool {
		if chats[i].LastMessageAt().Equal(chats[j].LastMessageAt()) {
			return chats[i].ID().String() < chats[j].ID().String()
		}
		return chats[i].LastMessageAt().After(chats[j].LastMessageAt())
	})
	result := tui.InitialState{Chats: make([]tui.InitialChat, len(chats))}
	for index, chat := range chats {
		messages, err := source.InitialMessages(ctx, chat.ID(), tui.MaxInitialMessagesPerChat)
		if err != nil {
			return tui.InitialState{}, err
		}
		if len(messages) > tui.MaxInitialMessagesPerChat {
			return tui.InitialState{}, errors.New("initial message snapshot exceeded bound")
		}
		// Store pages are newest-first. The renderer consumes oldest-first.
		sort.Slice(messages, func(i, j int) bool {
			if messages[i].SentAt().Equal(messages[j].SentAt()) {
				return messages[i].MessageID().String() < messages[j].MessageID().String()
			}
			return messages[i].SentAt().Before(messages[j].SentAt())
		})
		initialChat := tui.InitialChat{
			ID:           chat.ID().String(),
			Title:        chat.DisplayName(),
			IsGroup:      chat.IsGroup(),
			UnreadCount:  chat.UnreadCount(),
			ActivityTime: chat.LastMessageAt(),
			Messages:     make([]tui.InitialMessage, len(messages)),
		}
		for messageIndex, message := range messages {
			initialChat.Messages[messageIndex] = tui.InitialMessage{
				ID:           message.MessageID().String(),
				SentAt:       message.SentAt(),
				FromMe:       message.FromMe(),
				Text:         message.Text(),
				BodyRetained: message.BodyRetained(),
			}
		}
		result.Chats[index] = initialChat
	}
	return result, nil
}
