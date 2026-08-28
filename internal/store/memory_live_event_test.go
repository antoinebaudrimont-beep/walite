package store

import (
	"context"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

func TestMemoryRealtimeCommitReturnsAuthoritativeLiveEvents(t *testing.T) {
	ctx := context.Background()
	memory, err := NewMemory(4)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2100, 4, 5, 6, 7, 8, 0, time.UTC)
	chat, err := model.NewChat(model.ChatInput{
		ID: "live-metadata", DisplayName: "Synthetic live metadata",
		LastMessageAt: base, UnreadCount: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.EnsureChat(ctx, chat); err != nil {
		t.Fatal(err)
	}

	incoming := mustLiveMessage(t, "live-metadata", "incoming", base.Add(time.Minute), false, "ASCII")
	fromMe := mustLiveMessage(t, "live-metadata", "from-me", base.Add(2*time.Minute), true, "Café 東京 ❤️ 👍🏽 👨‍👩‍👧‍👦")
	delayed := mustLiveMessage(t, "live-metadata", "delayed", base.Add(-time.Minute), false, "older")
	bodyless := mustLiveMessage(t, "live-metadata", "bodyless", base.Add(3*time.Minute), false, "discarded body").WithoutBody()
	batch, err := model.NewWriteBatch(model.WriteRealtime, []model.Message{incoming, fromMe, delayed, bodyless})
	if err != nil {
		t.Fatal(err)
	}
	events, err := memory.WriteRealtime(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []string{"incoming", "from-me", "delayed", "bodyless"}
	wantUnread := []uint32{3, 3, 4, 5}
	wantActivity := []time.Time{base.Add(time.Minute), base.Add(2 * time.Minute), base.Add(2 * time.Minute), base.Add(3 * time.Minute)}
	if events.Len() != len(wantIDs) {
		t.Fatalf("event count=%d", events.Len())
	}
	for index := range wantIDs {
		event, ok := events.At(index)
		if !ok || event.Message().MessageID().String() != wantIDs[index] || event.UnreadCount() != wantUnread[index] || !event.ActivityTime().Equal(wantActivity[index]) {
			t.Fatalf("event %d=%+v open=%t", index, event, ok)
		}
	}
	unicodeEvent, _ := events.At(1)
	if !unicodeEvent.Message().FromMe() || unicodeEvent.Message().Text() != fromMe.Text() {
		t.Fatalf("unicode/from_me fidelity=%+v", unicodeEvent.Message())
	}
	bodylessEvent, _ := events.At(3)
	if bodylessEvent.Message().BodyRetained() || bodylessEvent.Message().Text() != "" {
		t.Fatalf("bodyless fidelity=%+v", bodylessEvent.Message())
	}
	storedChat := memory.chats[chat.ID().String()].chat
	if storedChat.UnreadCount() != 5 || !storedChat.LastMessageAt().Equal(base.Add(3*time.Minute)) {
		t.Fatalf("stored chat unread=%d activity=%v", storedChat.UnreadCount(), storedChat.LastMessageAt())
	}
}

func TestMemoryRealtimeDuplicateAndBodyImprovementDoNotEmitAgain(t *testing.T) {
	ctx := context.Background()
	memory, _ := NewMemory(2)
	now := time.Date(2100, 4, 5, 6, 7, 8, 0, time.UTC)
	bodyless := mustLiveMessage(t, "duplicate-chat", "duplicate-message", now, false, "later body").WithoutBody()
	batch, _ := model.NewWriteBatch(model.WriteRealtime, []model.Message{bodyless, bodyless})
	first, err := memory.WriteRealtime(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	if first.Len() != 1 {
		t.Fatalf("duplicate batch events=%d", first.Len())
	}
	improved := mustLiveMessage(t, "duplicate-chat", "duplicate-message", now, false, "later body")
	improvement, _ := model.NewWriteBatch(model.WriteRealtime, []model.Message{improved})
	second, err := memory.WriteRealtime(ctx, improvement)
	if err != nil {
		t.Fatal(err)
	}
	if second.Len() != 0 {
		t.Fatalf("body improvement events=%d", second.Len())
	}
	chatID, _ := model.NewChatID("duplicate-chat")
	storedChat := memory.chats[chatID.String()].chat
	if storedChat.UnreadCount() != 1 {
		t.Fatalf("duplicate unread=%d", storedChat.UnreadCount())
	}
	page, _, err := memory.Page(ctx, chatID, model.NoCursor(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || !page[0].BodyRetained() || page[0].Text() != "later body" {
		t.Fatalf("stored messages=%+v", page)
	}
}

func mustLiveMessage(t *testing.T, chatID, messageID string, sentAt time.Time, fromMe bool, text string) model.Message {
	t.Helper()
	message, err := model.NewMessage(model.MessageInput{
		ChatID: chatID, MessageID: messageID, SentAt: sentAt, FromMe: fromMe, Text: text,
	})
	if err != nil {
		t.Fatal(err)
	}
	return message
}
