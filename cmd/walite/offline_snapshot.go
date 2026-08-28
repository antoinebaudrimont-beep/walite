package main

import (
	"context"
	"fmt"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/store"
)

type offlineChatFixture struct {
	id       string
	title    string
	isGroup  bool
	unread   uint32
	activity time.Time
	count    int
	label    string
}

func seedOfflineInitialSnapshot(ctx context.Context, memory *store.Memory) ([]model.Chat, error) {
	fixtures := [...]offlineChatFixture{
		{id: "snapshot-demo", title: "Demo Chat", activity: demoStart.Add(4 * time.Minute), count: 18, label: "Demo"},
		{id: "snapshot-project", title: "Project Room", isGroup: true, unread: 3, activity: demoStart.Add(3 * time.Minute), count: 15, label: "Project"},
		{id: "snapshot-family", title: "Family Demo", unread: 1, activity: demoStart.Add(2 * time.Minute), count: 12, label: "Family-demo"},
		{id: "snapshot-contact", title: "Test Contact", unread: 12, activity: demoStart.Add(time.Minute), count: 10, label: "Test-contact"},
	}
	chats := make([]model.Chat, 0, len(fixtures))
	for fixtureIndex, fixture := range fixtures {
		chat, err := model.NewChat(model.ChatInput{
			ID:            fixture.id,
			DisplayName:   fixture.title,
			IsGroup:       fixture.isGroup,
			LastMessageAt: fixture.activity,
			UnreadCount:   fixture.unread,
			UpdatedAt:     fixture.activity,
			IngestSeq:     uint64(fixtureIndex + 1),
		})
		if err != nil {
			return nil, err
		}
		if err := memory.EnsureChat(ctx, chat); err != nil {
			return nil, err
		}
		messages := make([]model.Message, fixture.count)
		for messageIndex := range messages {
			text := fmt.Sprintf("%s synthetic message %d", fixture.label, messageIndex+1)
			if fixtureIndex == 0 {
				special := [...]string{
					"Synthetic message one",
					"Synthetic reply",
					"Synthetic terminal preview",
					"Synthetic wrapping preview that stays bounded while demonstrating a longer conversation line",
				}
				if messageIndex < len(special) {
					text = special[messageIndex]
				}
			}
			message, err := model.NewMessage(model.MessageInput{
				ChatID:    fixture.id,
				MessageID: fmt.Sprintf("%s-%03d", fixture.id, messageIndex+1),
				SentAt:    fixture.activity.Add(time.Duration(messageIndex-fixture.count) * time.Minute),
				FromMe:    messageIndex%2 == 1,
				Text:      text,
			})
			if err != nil {
				return nil, err
			}
			if fixtureIndex == 2 && messageIndex == 0 {
				message = message.WithoutBody()
			}
			messages[messageIndex] = message
		}
		batch, err := model.NewWriteBatch(model.WriteHistory, messages)
		if err != nil {
			return nil, err
		}
		if err := memory.Write(ctx, batch); err != nil {
			return nil, err
		}
		chats = append(chats, chat)
	}
	return chats, nil
}
