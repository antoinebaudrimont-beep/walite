package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/gdamore/tcell/v2"
)

type snapshotStub struct {
	chats              []model.Chat
	messages           map[string][]model.Message
	chatLimit          int
	messageLimits      []int
	ignoreMessageLimit bool
}

func (stub *snapshotStub) Run(ctx context.Context) error      { <-ctx.Done(); return ctx.Err() }
func (stub *snapshotStub) Updates() <-chan model.Update       { return make(chan model.Update) }
func (stub *snapshotStub) LiveEvents() <-chan model.LiveEvent { return nil }

func (stub *snapshotStub) InitialChats(_ context.Context, limit int) ([]model.Chat, error) {
	stub.chatLimit = limit
	if limit > len(stub.chats) {
		limit = len(stub.chats)
	}
	return append([]model.Chat(nil), stub.chats[:limit]...), nil
}

func (stub *snapshotStub) InitialMessages(_ context.Context, chatID model.ChatID, limit int) ([]model.Message, error) {
	stub.messageLimits = append(stub.messageLimits, limit)
	messages := stub.messages[chatID.String()]
	if !stub.ignoreMessageLimit && limit < len(messages) {
		messages = messages[:limit]
	}
	return append([]model.Message(nil), messages...), nil
}

func TestInitialSnapshotAdapterOrdersAndPreservesFidelity(t *testing.T) {
	equal := time.Date(2100, 5, 6, 7, 8, 0, 0, time.UTC)
	newer := equal.Add(time.Minute)
	chatB := mustSnapshotChat(t, "b-contact", "Café 東京", false, 9, equal)
	chatA := mustSnapshotChat(t, "a-group", "Family ❤️", true, 2, equal)
	chatNew := mustSnapshotChat(t, "new-chat", "Newest 👍🏽", false, 0, newer)
	bodyless := mustSnapshotMessage(t, "a-group", "bodyless", equal.Add(-time.Minute), false, "must disappear").WithoutBody()
	older := mustSnapshotMessage(t, "a-group", "m-a", equal.Add(-time.Minute), false, "ASCII")
	equalID := mustSnapshotMessage(t, "a-group", "m-b", equal.Add(-time.Minute), true, "👨‍👩‍👧‍👦")
	stub := &snapshotStub{
		chats: []model.Chat{chatB, chatNew, chatA},
		messages: map[string][]model.Message{
			"a-group":   {equalID, older, bodyless},
			"b-contact": {mustSnapshotMessage(t, "b-contact", "cjk", equal, false, "東京 🙂")},
		},
	}
	initial, err := buildInitialTUIState(context.Background(), stub)
	if err != nil {
		t.Fatal(err)
	}
	if stub.chatLimit != tui.MaxInitialChats || len(stub.messageLimits) != 3 {
		t.Fatalf("bounds chats=%d messages=%v", stub.chatLimit, stub.messageLimits)
	}
	for _, limit := range stub.messageLimits {
		if limit != tui.MaxInitialMessagesPerChat {
			t.Fatalf("message limit=%d", limit)
		}
	}
	if got := []string{initial.Chats[0].ID, initial.Chats[1].ID, initial.Chats[2].ID}; fmt.Sprint(got) != "[new-chat a-group b-contact]" {
		t.Fatalf("chat order=%v", got)
	}
	group := initial.Chats[1]
	if !group.IsGroup || group.UnreadCount != 2 || group.Title != "Family ❤️" || !group.ActivityTime.Equal(equal) {
		t.Fatalf("chat fidelity=%+v", group)
	}
	if got := []string{group.Messages[0].ID, group.Messages[1].ID, group.Messages[2].ID}; fmt.Sprint(got) != "[bodyless m-a m-b]" {
		t.Fatalf("oldest-first equal-time order=%v", got)
	}
	if group.Messages[0].BodyRetained || group.Messages[0].Text != "" {
		t.Fatalf("bodyless message=%+v", group.Messages[0])
	}
	if !group.Messages[2].FromMe || group.Messages[2].Text != "👨‍👩‍👧‍👦" {
		t.Fatalf("direction/unicode lost=%+v", group.Messages[2])
	}
}

func TestInitialSnapshotAdapterKeepsLargeHistoryBounded(t *testing.T) {
	activity := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	chat := mustSnapshotChat(t, "large", "Large", false, 0, activity)
	messages := make([]model.Message, 100)
	for index := range messages {
		messages[index] = mustSnapshotMessage(t, "large", fmt.Sprintf("m-%03d", 100-index), activity.Add(time.Duration(100-index)*time.Minute), false, "bounded")
	}
	stub := &snapshotStub{chats: []model.Chat{chat}, messages: map[string][]model.Message{"large": messages}}
	initial, err := buildInitialTUIState(context.Background(), stub)
	if err != nil {
		t.Fatal(err)
	}
	if len(initial.Chats) != 1 || len(initial.Chats[0].Messages) != tui.MaxInitialMessagesPerChat || stub.messageLimits[0] != tui.MaxInitialMessagesPerChat {
		t.Fatalf("snapshot chats=%d messages=%d requested=%d", len(initial.Chats), len(initial.Chats[0].Messages), stub.messageLimits[0])
	}
	stub.ignoreMessageLimit = true
	if _, err := buildInitialTUIState(context.Background(), stub); err == nil {
		t.Fatal("oversized provider result accepted")
	}
}

type readySnapshotService struct {
	updates            chan model.Update
	chat               model.Chat
	message            model.Message
	readyPublished     atomic.Bool
	snapshotAfterReady atomic.Bool
}

func (service *readySnapshotService) Run(ctx context.Context) error {
	ready, _ := model.NewUpdate(model.UpdateInput{Kind: model.UpdateReady})
	service.readyPublished.Store(true)
	service.updates <- ready
	<-ctx.Done()
	close(service.updates)
	return ctx.Err()
}

func (service *readySnapshotService) Updates() <-chan model.Update { return service.updates }
func (*readySnapshotService) LiveEvents() <-chan model.LiveEvent   { return nil }

func (service *readySnapshotService) InitialChats(context.Context, int) ([]model.Chat, error) {
	service.snapshotAfterReady.Store(service.readyPublished.Load())
	return []model.Chat{service.chat}, nil
}

func (service *readySnapshotService) InitialMessages(_ context.Context, chatID model.ChatID, _ int) ([]model.Message, error) {
	if chatID.String() != service.chat.ID().String() {
		return nil, errors.New("unexpected chat")
	}
	return []model.Message{service.message}, nil
}

func TestApplicationLoadsSnapshotOnlyAfterReady(t *testing.T) {
	activity := time.Date(2100, 6, 7, 8, 9, 0, 0, time.UTC)
	service := &readySnapshotService{
		updates: make(chan model.Update, 1),
		chat:    mustSnapshotChat(t, "adapter-only", "Adapter Only Chat", false, 0, activity),
		message: mustSnapshotMessage(t, "adapter-only", "adapter-message", activity, false, "Only supplied by adapter"),
	}
	screen := newStartupObservedScreen()
	done := make(chan error, 1)
	go func() {
		done <- runStartedApplication(context.Background(), screen, tui.DefaultOptions(), service, tui.Run)
	}()
	<-screen.shown
	if !service.snapshotAfterReady.Load() {
		t.Fatal("initial snapshot loaded before ready")
	}
	if rendered := startupScreenText(screen); !strings.Contains(rendered, "Adapter Only Chat") || !strings.Contains(rendered, "Only supplied by adapter") || strings.Contains(rendered, "Demo Chat") {
		t.Fatalf("TUI replaced adapter snapshot:\n%s", rendered)
	}
	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func mustSnapshotChat(t *testing.T, id, title string, group bool, unread uint32, activity time.Time) model.Chat {
	t.Helper()
	chat, err := model.NewChat(model.ChatInput{ID: id, DisplayName: title, IsGroup: group, UnreadCount: unread, LastMessageAt: activity})
	if err != nil {
		t.Fatal(err)
	}
	return chat
}

func mustSnapshotMessage(t *testing.T, chatID, id string, sentAt time.Time, fromMe bool, text string) model.Message {
	t.Helper()
	message, err := model.NewMessage(model.MessageInput{ChatID: chatID, MessageID: id, SentAt: sentAt, FromMe: fromMe, Text: text})
	if err != nil {
		t.Fatal(err)
	}
	return message
}
