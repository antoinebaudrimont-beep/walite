package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

func TestConnectedBootstrapPersistsMetadataAndHistoryWithoutLiveUnread(t *testing.T) {
	cache := openConnectedTestCache(t)
	application := &connectedApplicationService{store: cache, cacheUpdates: make(chan struct{}, 1)}
	now := time.Date(2100, 9, 2, 12, 0, 0, 0, time.UTC)
	chat, err := model.NewChat(model.ChatInput{
		ID: "family@g.us", DisplayName: "Family 家族", IsGroup: true, UnreadCount: 7,
		LastMessageAt: now, Archived: true, Muted: true, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	quote, _ := model.NewTextQuote("quoted", "original Café 👋", true)
	messages := []model.Message{
		mustBootstrapMessage(t, chat.ID().String(), "incoming", now.Add(-time.Minute), false, "incoming 日本語", "peer@lid", true).WithQuote(quote),
		mustBootstrapMessage(t, chat.ID().String(), "outgoing", now, true, "outgoing ❤️", "", true),
	}
	record, err := model.NewBootstrapRecord(model.BootstrapInitial, chat, messages)
	if err != nil {
		t.Fatal(err)
	}
	metadataChat, _ := model.NewChat(model.ChatInput{ID: "metadata-only@lid", DisplayName: "Metadata Only", LastMessageAt: now.Add(-time.Hour), UnreadCount: 3, UpdatedAt: now})
	metadataOnly, _ := model.NewBootstrapRecord(model.BootstrapInitial, metadataChat, nil)
	complete, _ := model.NewBootstrapComplete(model.BootstrapInitial)
	records := make(chan model.BootstrapRecord, 3)
	records <- record
	records <- metadataOnly
	records <- complete
	close(records)
	if err := application.runBootstrap(context.Background(), records); err != nil {
		t.Fatal(err)
	}

	chats, err := cache.ListChats(context.Background(), model.MaxChatSummaries)
	if err != nil || len(chats) != 2 {
		t.Fatalf("summaries=%d err=%v", len(chats), err)
	}
	persisted, err := cache.Chat(context.Background(), chat.ID())
	if err != nil || persisted.UnreadCount() != 7 || persisted.DisplayName() != chat.DisplayName() || !persisted.IsGroup() || !persisted.Archived() || !persisted.Muted() {
		t.Fatalf("persisted chat=%+v err=%v", persisted, err)
	}
	page, _, err := cache.Page(context.Background(), chat.ID(), model.NoCursor(), 32)
	if err != nil || len(page) != 2 || !page[0].FromMe() || page[1].SenderID().String() != "peer@lid" || page[1].Quote() != quote {
		t.Fatalf("history page=%+v err=%v", page, err)
	}
	metadataPage, _, err := cache.Page(context.Background(), metadataChat.ID(), model.NoCursor(), 32)
	if err != nil || len(metadataPage) != 0 {
		t.Fatalf("metadata-only page=%v err=%v", metadataPage, err)
	}
	if len(application.cacheUpdates) != 1 {
		t.Fatalf("completion updates=%d want coalesced one", len(application.cacheUpdates))
	}

	// Replaying the same transport-neutral delivery is idempotent and does not
	// increment authoritative unread once per historical message.
	replay := make(chan model.BootstrapRecord, 2)
	replay <- record
	replay <- complete
	close(replay)
	if err := application.runBootstrap(context.Background(), replay); err != nil {
		t.Fatal(err)
	}
	persisted, _ = cache.Chat(context.Background(), chat.ID())
	page, _, _ = cache.Page(context.Background(), chat.ID(), model.NoCursor(), 32)
	if persisted.UnreadCount() != 7 || len(page) != 2 || len(application.cacheUpdates) != 1 {
		t.Fatalf("replay unread=%d messages=%d updates=%d", persisted.UnreadCount(), len(page), len(application.cacheUpdates))
	}
	absentUnreadChat, _ := model.NewChat(model.ChatInput{ID: chat.ID().String(), DisplayName: chat.DisplayName(), IsGroup: true, LastMessageAt: now, UpdatedAt: now.Add(time.Minute)})
	absentUnread, _ := model.NewBootstrapRecord(model.BootstrapRecent, absentUnreadChat, nil)
	absentUnread = absentUnread.WithUnreadAuthoritative(false)
	missing := make(chan model.BootstrapRecord, 1)
	missing <- absentUnread
	close(missing)
	if err := application.runBootstrap(context.Background(), missing); err != nil {
		t.Fatal(err)
	}
	persisted, _ = cache.Chat(context.Background(), chat.ID())
	if persisted.UnreadCount() != 7 {
		t.Fatalf("absent upstream unread reset cached value to %d", persisted.UnreadCount())
	}
	if err := cache.MarkChatLocallyRead(context.Background(), chat.ID(), now); err != nil {
		t.Fatal(err)
	}
	staleChat, _ := model.NewChat(model.ChatInput{
		ID: chat.ID().String(), DisplayName: chat.DisplayName(), IsGroup: true, LastMessageAt: now,
		UnreadCount: 9, UpdatedAt: now.Add(2 * time.Minute),
	})
	staleRecord, _ := model.NewBootstrapRecord(model.BootstrapRecent, staleChat, nil)
	stale := make(chan model.BootstrapRecord, 1)
	stale <- staleRecord
	close(stale)
	if err := application.runBootstrap(context.Background(), stale); err != nil {
		t.Fatal(err)
	}
	persisted, _ = cache.Chat(context.Background(), chat.ID())
	if persisted.UnreadCount() != 0 {
		t.Fatalf("equal-activity history resurrected local unread=%d", persisted.UnreadCount())
	}
	newerChat, _ := model.NewChat(model.ChatInput{
		ID: chat.ID().String(), DisplayName: chat.DisplayName(), IsGroup: true, LastMessageAt: now.Add(time.Minute),
		UnreadCount: 2, UpdatedAt: now.Add(3 * time.Minute),
	})
	newerRecord, _ := model.NewBootstrapRecord(model.BootstrapRecent, newerChat, nil)
	newer := make(chan model.BootstrapRecord, 1)
	newer <- newerRecord
	close(newer)
	if err := application.runBootstrap(context.Background(), newer); err != nil {
		t.Fatal(err)
	}
	persisted, _ = cache.Chat(context.Background(), chat.ID())
	if persisted.UnreadCount() != 2 {
		t.Fatalf("newer authoritative history unread=%d", persisted.UnreadCount())
	}
	if err := cache.MarkChatLocallyRead(context.Background(), chat.ID(), newerChat.LastMessageAt()); err != nil {
		t.Fatal(err)
	}

	// Realtime traffic after bootstrap uses the same cached chat identity.
	// Incoming increments from the locally cleared unread value;
	// outgoing activity does not increment it or create another summary.
	incoming := mustBootstrapMessage(t, chat.ID().String(), "realtime-in", now.Add(2*time.Minute), false, "new incoming", "peer@lid", true)
	outgoing := mustBootstrapMessage(t, chat.ID().String(), "realtime-out", now.Add(3*time.Minute), true, "new outgoing", "", true)
	for index, message := range []model.Message{incoming, outgoing} {
		batch, batchErr := model.NewWriteBatch(model.WriteRealtime, []model.Message{message})
		if batchErr != nil {
			t.Fatal(batchErr)
		}
		committed, writeErr := cache.WriteRealtime(context.Background(), batch)
		if writeErr != nil || committed.Len() != 1 {
			t.Fatalf("realtime write %d committed=%d err=%v", index, committed.Len(), writeErr)
		}
	}
	updatedChats, err := cache.ListChats(context.Background(), model.MaxChatSummaries)
	if err != nil || len(updatedChats) != 2 || updatedChats[0].ID() != chat.ID() || updatedChats[0].UnreadCount() != 1 {
		t.Fatalf("post-bootstrap summaries=%+v err=%v", updatedChats, err)
	}
	page, _, err = cache.Page(context.Background(), chat.ID(), model.NoCursor(), 32)
	if err != nil || len(page) != 4 || page[0].MessageID().String() != "realtime-out" {
		t.Fatalf("post-bootstrap page=%+v err=%v", page, err)
	}
}

func TestConnectedBootstrapCancellationStopsCleanly(t *testing.T) {
	cache := openConnectedTestCache(t)
	application := &connectedApplicationService{store: cache, cacheUpdates: make(chan struct{}, 1)}
	records := make(chan model.BootstrapRecord)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- application.runBootstrap(ctx, records) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("bootstrap shutdown err=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bootstrap consumer did not stop")
	}
}

func TestConnectedBootstrapInsertionOrderDoesNotChangeActivityOrder(t *testing.T) {
	cache := openConnectedTestCache(t)
	application := &connectedApplicationService{store: cache, cacheUpdates: make(chan struct{}, 1)}
	base := time.Date(2026, 9, 1, 7, 0, 0, 0, time.UTC)
	values := []struct {
		id string
		at time.Time
	}{
		{id: "yesterday", at: time.Date(2026, 8, 31, 23, 50, 0, 0, time.UTC)},
		{id: "z-tie", at: base.Add(-48 * time.Hour)},
		{id: "today", at: base},
		{id: "a-tie", at: base.Add(-48 * time.Hour)},
	}
	records := make(chan model.BootstrapRecord, len(values))
	for _, value := range values {
		chat, err := model.NewChat(model.ChatInput{ID: value.id, DisplayName: value.id, LastMessageAt: value.at, UpdatedAt: base})
		if err != nil {
			t.Fatal(err)
		}
		record, _ := model.NewBootstrapRecord(model.BootstrapFull, chat, nil)
		records <- record
	}
	close(records)
	if err := application.runBootstrap(context.Background(), records); err != nil {
		t.Fatal(err)
	}
	chats, err := application.InitialChats(context.Background(), len(values))
	if err != nil {
		t.Fatal(err)
	}
	for index, want := range []string{"today", "yesterday", "a-tie", "z-tie"} {
		if chats[index].ID().String() != want {
			t.Fatalf("order[%d]=%q want=%q", index, chats[index].ID().String(), want)
		}
	}
}

func mustBootstrapMessage(t *testing.T, chatID, messageID string, at time.Time, fromMe bool, text, sender string, group bool) model.Message {
	t.Helper()
	message, err := model.NewMessage(model.MessageInput{
		ChatID: chatID, MessageID: messageID, SentAt: at, FromMe: fromMe, Text: text, SenderID: sender, IsGroup: group,
	})
	if err != nil {
		t.Fatal(err)
	}
	return message
}
