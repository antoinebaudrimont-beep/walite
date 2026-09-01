package store

import (
	"context"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

func TestSQLiteLocalReadPersistsAndRealtimeRestartsUnread(t *testing.T) {
	ctx := context.Background()
	disk, path := openTestSQLite(t)
	base := time.Date(2100, 9, 4, 10, 0, 0, 0, time.UTC)
	first, _ := model.NewChat(model.ChatInput{ID: "first@lid", DisplayName: "First", UnreadCount: 2, LastMessageAt: base, UpdatedAt: base})
	second, _ := model.NewChat(model.ChatInput{ID: "second@lid", DisplayName: "Second", UnreadCount: 3, LastMessageAt: base, UpdatedAt: base})
	if err := disk.EnsureChat(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := disk.EnsureChat(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := disk.MarkChatLocallyRead(ctx, second.ID(), second.LastMessageAt()); err != nil {
		t.Fatal(err)
	}
	if err := disk.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenSQLite(ctx, SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	chats, err := reopened.ListChats(ctx, model.MaxChatSummaries)
	if err != nil || len(chats) != 2 {
		t.Fatalf("restart summaries=%+v err=%v", chats, err)
	}
	byID := map[string]model.Chat{}
	for _, chat := range chats {
		byID[chat.ID().String()] = chat
	}
	if byID[first.ID().String()].UnreadCount() != 2 || byID[second.ID().String()].UnreadCount() != 0 {
		t.Fatalf("restart unread first=%d second=%d", byID[first.ID().String()].UnreadCount(), byID[second.ID().String()].UnreadCount())
	}

	incoming, err := model.NewMessage(model.MessageInput{
		ChatID: second.ID().String(), MessageID: "incoming-after-clear", SentAt: base.Add(time.Minute), Text: "new 👋",
	})
	if err != nil {
		t.Fatal(err)
	}
	batch, _ := model.NewWriteBatch(model.WriteRealtime, []model.Message{incoming})
	committed, err := reopened.WriteRealtime(ctx, batch)
	if err != nil || committed.Len() != 1 {
		t.Fatalf("first realtime committed=%d err=%v", committed.Len(), err)
	}
	duplicate, err := reopened.WriteRealtime(ctx, batch)
	if err != nil || duplicate.Len() != 0 {
		t.Fatalf("duplicate realtime committed=%d err=%v", duplicate.Len(), err)
	}
	updated, err := reopened.Chat(ctx, second.ID())
	if err != nil || updated.UnreadCount() != 1 {
		t.Fatalf("post-clear realtime unread=%d err=%v", updated.UnreadCount(), err)
	}
	// A delayed worker write from the earlier selection must not clear a
	// realtime message with newer activity that committed first.
	if err := reopened.MarkChatLocallyRead(ctx, second.ID(), second.LastMessageAt()); err != nil {
		t.Fatal(err)
	}
	updated, err = reopened.Chat(ctx, second.ID())
	if err != nil || updated.UnreadCount() != 1 {
		t.Fatalf("delayed stale clear changed newer unread=%d err=%v", updated.UnreadCount(), err)
	}
	unchanged, err := reopened.Chat(ctx, first.ID())
	if err != nil || unchanged.UnreadCount() != 2 {
		t.Fatalf("other chat unread=%d err=%v", unchanged.UnreadCount(), err)
	}
}

func TestMemoryLocalReadMatchesCacheSemantics(t *testing.T) {
	memory, err := NewMemory(2)
	if err != nil {
		t.Fatal(err)
	}
	chat, _ := model.NewChat(model.ChatInput{ID: "memory-chat", UnreadCount: 4})
	if err := memory.EnsureChat(context.Background(), chat); err != nil {
		t.Fatal(err)
	}
	if err := memory.MarkChatLocallyRead(context.Background(), chat.ID(), chat.LastMessageAt()); err != nil {
		t.Fatal(err)
	}
	stored := memory.chats[chat.ID().String()].chat
	if stored.UnreadCount() != 0 {
		t.Fatalf("memory unread=%d", stored.UnreadCount())
	}
}
