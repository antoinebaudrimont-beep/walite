package main

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/store"
)

type capturedOlderHistoryRequest struct {
	chatID    model.ChatID
	messageID model.MessageID
	at        time.Time
	fromMe    bool
	count     int
}

type fakeOlderHistoryRequester struct {
	mu      sync.Mutex
	calls   []capturedOlderHistoryRequest
	started chan struct{}
}

func (requester *fakeOlderHistoryRequester) RequestOlderHistory(_ context.Context, chatID model.ChatID, messageID model.MessageID, at time.Time, fromMe bool, count int) error {
	requester.mu.Lock()
	requester.calls = append(requester.calls, capturedOlderHistoryRequest{chatID: chatID, messageID: messageID, at: at, fromMe: fromMe, count: count})
	started := requester.started
	requester.started = nil
	requester.mu.Unlock()
	if started != nil {
		close(started)
	}
	return nil
}

func (requester *fakeOlderHistoryRequester) count() int {
	requester.mu.Lock()
	defer requester.mu.Unlock()
	return len(requester.calls)
}

func TestOnDemandHistoryUsesExistingSQLiteImportIdempotentlyAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cache.db")
	cache, err := store.OpenSQLite(ctx, store.SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	chat, _ := model.NewChat(model.ChatInput{
		ID: "12345@lid", DisplayName: "Café chat", LastMessageAt: base, UnreadCount: 6, UpdatedAt: base,
	})
	if err := cache.EnsureChat(ctx, chat); err != nil {
		t.Fatal(err)
	}
	frontier := mustBootstrapMessage(t, chat.ID().String(), "current-frontier", base, true, "current 👋", "", false)
	writeHistoryMessages(t, cache, frontier)

	requestStarted := make(chan struct{})
	requester := &fakeOlderHistoryRequester{started: requestStarted}
	application := &connectedApplicationService{
		store: cache, history: requester, cacheUpdates: make(chan struct{}, 1), onDemandDone: make(chan struct{}, 1),
	}
	loaded := make(chan []model.Message, 1)
	loadErr := make(chan error, 1)
	go func() {
		messages, err := application.LoadOlderMessages(ctx, chat.ID(), frontier.MessageID(), frontier.SentAt(), frontier.FromMe(), 50)
		loaded <- messages
		loadErr <- err
	}()
	<-requestStarted
	if requester.count() != 1 {
		t.Fatalf("requests=%d", requester.count())
	}

	image, _ := model.NewMedia(model.MediaImage, "", "image/jpeg")
	olderImage, err := model.NewMessage(model.MessageInput{
		ChatID: chat.ID().String(), MessageID: "older-image", SentAt: base.Add(-2 * time.Hour), Text: "Holiday 日本語", Media: image, SenderID: "friend@lid",
	})
	if err != nil {
		t.Fatal(err)
	}
	quote, _ := model.NewMediaQuote("older-image", "Holiday 日本語", false, image)
	olderReply, err := model.NewMessage(model.MessageInput{
		ChatID: chat.ID().String(), MessageID: "older-reply", SentAt: base.Add(-time.Hour), FromMe: true, Text: "reply Café 👋", Quote: quote,
	})
	if err != nil {
		t.Fatal(err)
	}
	upstreamChat, _ := model.NewChat(model.ChatInput{
		ID: chat.ID().String(), DisplayName: chat.DisplayName(), LastMessageAt: base, UnreadCount: 99, UpdatedAt: base.Add(time.Minute),
	})
	record, _ := model.NewBootstrapRecord(model.BootstrapOnDemand, upstreamChat, []model.Message{olderImage, olderReply, frontier})
	complete, _ := model.NewBootstrapComplete(model.BootstrapOnDemand)
	records := make(chan model.BootstrapRecord, 2)
	records <- record
	records <- complete
	close(records)
	if err := application.runBootstrap(ctx, records); err != nil {
		t.Fatal(err)
	}
	if err := <-loadErr; err != nil {
		t.Fatal(err)
	}
	page := <-loaded
	if len(page) != 2 || page[0].MessageID().String() != "older-reply" || page[1].MessageID().String() != "older-image" {
		t.Fatalf("older page=%+v", page)
	}
	if page[1].Media().Kind() != model.MediaImage || page[1].Text() != "Holiday 日本語" || page[0].Quote().MessageID().String() != "older-image" || page[0].Quote().Media().Kind() != model.MediaImage {
		t.Fatalf("history metadata lost: %+v %+v", page[0], page[1])
	}
	persisted, err := cache.Chat(ctx, chat.ID())
	if err != nil || persisted.UnreadCount() != 6 {
		t.Fatalf("unread=%d err=%v", persisted.UnreadCount(), err)
	}
	all, _, err := cache.Page(ctx, chat.ID(), model.NoCursor(), 50)
	if err != nil || len(all) != 3 {
		t.Fatalf("deduplicated page=%d err=%v", len(all), err)
	}

	// The same explicit frontier is now satisfied locally and never causes a
	// duplicate peer request.
	cached, err := application.LoadOlderMessages(ctx, chat.ID(), frontier.MessageID(), frontier.SentAt(), frontier.FromMe(), 50)
	if err != nil || len(cached) != 2 || requester.count() != 1 {
		t.Fatalf("cached=%d err=%v requests=%d", len(cached), err, requester.count())
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := store.OpenSQLite(ctx, store.SQLiteOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restarted := &connectedApplicationService{store: reopened}
	cached, err = restarted.LoadOlderMessages(ctx, chat.ID(), frontier.MessageID(), frontier.SentAt(), frontier.FromMe(), 50)
	if err != nil || len(cached) != 2 || cached[1].Media().Kind() != model.MediaImage || cached[0].Quote().Media().Kind() != model.MediaImage {
		t.Fatalf("restart page=%+v err=%v", cached, err)
	}
}

func writeHistoryMessages(t *testing.T, cache *store.SQLiteStore, messages ...model.Message) {
	t.Helper()
	batch, err := model.NewWriteBatch(model.WriteHistory, messages)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Write(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
}
