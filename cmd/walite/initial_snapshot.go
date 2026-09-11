package main

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/service"
	"github.com/antoinebaudrimont-beep/walite/internal/store"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/antoinebaudrimont-beep/walite/internal/wa"
)

type offlineApplicationService struct {
	core  *service.Core
	store *store.Memory
	chats []model.Chat
}

type reactionSummaryApplication interface {
	ReactionSummary(context.Context, model.ChatID, model.MessageID) (model.ReactionSummary, error)
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

func (application *offlineApplicationService) SendText(ctx context.Context, request service.SendTextRequest) error {
	return application.core.SendText(ctx, request)
}

func (application *offlineApplicationService) MarkChatLocallyRead(ctx context.Context, id model.ChatID, through time.Time) error {
	return application.store.MarkChatLocallyRead(ctx, id, through)
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
	chats, err := source.InitialChats(ctx, tui.ChatWorkingSetCapacity)
	if err != nil {
		return tui.InitialState{}, err
	}
	if len(chats) > tui.ChatWorkingSetCapacity {
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
		title := chat.DisplayName()
		if title == "" {
			title = wa.ReadableChatFallback(chat.ID())
		}
		initialChat := tui.InitialChat{
			ID:           chat.ID().String(),
			Title:        title,
			IsGroup:      chat.IsGroup(),
			UnreadCount:  chat.UnreadCount(),
			ActivityTime: chat.LastMessageAt(),
			ReadOnly:     !wa.ChatSendable(chat.ID()),
		}
		result.Chats[index] = initialChat
	}
	return result, nil
}

func buildChatLoadResult(ctx context.Context, source applicationService, request tui.ChatLoadRequest) (tui.ChatLoadResult, error) {
	id, err := model.NewChatID(request.ChatID)
	if err != nil {
		return tui.ChatLoadResult{}, err
	}
	messages, err := source.InitialMessages(ctx, id, tui.MaxInitialMessagesPerChat)
	if err != nil {
		return tui.ChatLoadResult{}, err
	}
	if len(messages) > tui.MaxInitialMessagesPerChat {
		return tui.ChatLoadResult{}, errors.New("chat message snapshot exceeded bound")
	}
	if requester, ok := source.(displayMetadataRequester); ok {
		// Include the selected chat itself so a cached direct PN/LID fallback can
		// improve even when its current page contains no incoming messages. The
		// transport boundary rejects group IDs and keeps this local lookup batch
		// bounded to one chat plus the fixed message-page size.
		people := make([]model.ChatID, 0, len(messages)+1)
		people = append(people, id)
		seen := make(map[model.ChatID]struct{}, len(messages))
		seen[id] = struct{}{}
		for _, message := range messages {
			person := model.ChatID(message.SenderID())
			if person.String() == "" {
				continue
			}
			if _, duplicate := seen[person]; duplicate {
				continue
			}
			seen[person] = struct{}{}
			people = append(people, person)
		}
		requester.RequestDisplayMetadata(people)
	}
	sort.Slice(messages, func(i, j int) bool {
		if messages[i].SentAt().Equal(messages[j].SentAt()) {
			return messages[i].MessageID().String() < messages[j].MessageID().String()
		}
		return messages[i].SentAt().Before(messages[j].SentAt())
	})
	result := tui.ChatLoadResult{ChatID: request.ChatID, Revision: request.Revision, Messages: make([]tui.InitialMessage, len(messages))}
	for index, message := range messages {
		result.Messages[index] = initialMessageFromModel(message)
		if reactions, ok := source.(reactionSummaryApplication); ok {
			summary, err := reactions.ReactionSummary(ctx, message.ChatID(), message.MessageID())
			if err != nil {
				return tui.ChatLoadResult{}, err
			}
			result.Messages[index].Reactions = reactionGroupsFromModel(summary)
		}
	}
	return result, nil
}

func reactionGroupsFromModel(summary model.ReactionSummary) []tui.ReactionGroup {
	groups := make([]tui.ReactionGroup, summary.Len())
	for index := range groups {
		group, _ := summary.At(index)
		groups[index] = tui.ReactionGroup{Emoji: group.Emoji(), Count: group.Count(), Own: group.Own()}
	}
	return groups
}

func initialMessageFromModel(message model.Message) tui.InitialMessage {
	return tui.InitialMessage{
		ID: message.MessageID().String(), SentAt: message.SentAt(), FromMe: message.FromMe(),
		Text: message.Text(), BodyRetained: message.BodyRetained(),
		MediaKind: message.Media().Kind().String(), MediaName: message.Media().Name(), MediaMIME: message.Media().MIMEType(),
		ReplyToID: message.Quote().MessageID().String(), ReplyToText: message.Quote().Text(), ReplyToFromMe: message.Quote().FromMe(),
		ReplyMediaKind: message.Quote().Media().Kind().String(), ReplyMediaName: message.Quote().Media().Name(), ReplyMediaMIME: message.Quote().Media().MIMEType(),
		SenderID: message.SenderID().String(), IsGroup: message.IsGroup(),
	}
}
