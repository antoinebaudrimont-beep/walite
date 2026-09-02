package tui

import (
	"context"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func readReceiptView(t *testing.T) viewModel {
	t.Helper()
	base := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	state, err := chatStateFromInitial(InitialState{Chats: []InitialChat{
		{ID: "read@lid", Title: "Read", ActivityTime: base},
		{ID: "unread@lid", Title: "Unread", UnreadCount: 3, ActivityTime: base.Add(time.Minute), Messages: []InitialMessage{
			{ID: "incoming-é-🙂", SentAt: base, Text: "body 日本語", BodyRetained: true},
			{ID: "from-me", SentAt: base.Add(time.Second), FromMe: true, Text: "mine", BodyRetained: true},
			{ID: "bodyless", SentAt: base.Add(2 * time.Second), BodyRetained: false},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return viewModel{chats: state}
}

func TestExplicitUnreadSelectionBuildsOneIncomingReadFrontier(t *testing.T) {
	model := readReceiptView(t)
	if !moveChatSelection(&model, 1) {
		t.Fatal("selection failed")
	}
	if model.chats.chats[1].unreadCount != 0 || model.localReadRequest.ChatID != "unread@lid" {
		t.Fatalf("unread=%d local=%+v", model.chats.chats[1].unreadCount, model.localReadRequest)
	}
	var captured ReadReceiptRequest
	requestPendingReadReceipt(&model, func(request ReadReceiptRequest) bool { captured = request; return true })
	if captured.ChatID != "unread@lid" || captured.IsGroup || len(captured.Messages) != 2 ||
		captured.Messages[0].MessageID != "incoming-é-🙂" || captured.Messages[1].MessageID != "bodyless" {
		t.Fatalf("request=%+v", captured)
	}
	if captured.Messages[0].SentAt.IsZero() || captured.Messages[1].SentAt.IsZero() {
		t.Fatal("stable timestamps lost")
	}
	if !moveChatSelection(&model, -1) || !moveChatSelection(&model, 1) || model.readRequest.ChatID != "" {
		t.Fatal("already-read reselection created a redundant receipt")
	}
}

func TestReadAdmissionFailureDoesNotRestoreLocalUnread(t *testing.T) {
	model := readReceiptView(t)
	index, ok := model.chats.chatIndexByID("unread@lid")
	if !ok || !moveChatSelection(&model, index-model.chats.selectedIndex()) {
		t.Fatal("could not reselect unread chat")
	}
	calls := 0
	requestPendingReadReceipt(&model, func(ReadReceiptRequest) bool { calls++; return false })
	if calls != 1 || model.chats.chats[1].unreadCount != 0 || model.readRequest.ChatID != "" {
		t.Fatalf("calls=%d unread=%d pending=%+v", calls, model.chats.chats[1].unreadCount, model.readRequest)
	}
}

func TestStartupSummaryAndLiveArrivalNeverCreateRemoteRead(t *testing.T) {
	model := readReceiptView(t)
	if model.readRequest.ChatID != "" || model.readIntent.chatID != "" {
		t.Fatal("initial snapshot created receipt")
	}
	base := time.Date(2026, 9, 1, 11, 0, 0, 0, time.UTC)
	if !applyLiveMessage(&model, LiveMessage{ChatID: "unread@lid", MessageID: "background", SentAt: base, Text: "incoming", BodyRetained: true, UnreadCount: 4, ActivityTime: base}) {
		t.Fatal("live event not applied")
	}
	if model.readRequest.ChatID != "" || model.readIntent.chatID != "" {
		t.Fatal("background event created receipt")
	}
	update := InitialState{Chats: []InitialChat{{ID: "unread@lid", Title: "Unread", UnreadCount: 4, ActivityTime: base}, {ID: "read@lid", Title: "Read", ActivityTime: base.Add(-time.Minute)}}}
	applyChatSummaries(&model, update)
	if model.readRequest.ChatID != "" || model.readIntent.chatID != "" {
		t.Fatal("summary/history refresh created receipt")
	}
}

func TestCachedUnreadStartupSendsNoRemoteRead(t *testing.T) {
	screen := initializedSimulationScreen(t, 100, 30)
	called := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- runInitializedWithDependencies(context.Background(), screen, Input{
			Options: DefaultOptions(), InitialState: InitialState{Chats: []InitialChat{{ID: "cached@lid", Title: "Cached", UnreadCount: 2, ActivityTime: time.Unix(1, 0).UTC()}}},
			SendReadReceipt: func(ReadReceiptRequest) bool { called <- struct{}{}; return true },
		}, "")
	}()
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
		t.Fatal("startup sent remote read")
	default:
	}
}

func TestUnreadSelectionWaitsForBoundedChatLoad(t *testing.T) {
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	state, err := chatStateFromInitial(InitialState{Chats: []InitialChat{
		{ID: "first@lid", Title: "First", ActivityTime: base},
		{ID: "loaded@lid", Title: "Loaded", UnreadCount: 40, ActivityTime: base.Add(time.Minute)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	model := viewModel{chats: state}
	moveChatSelection(&model, 1)
	if model.readRequest.ChatID != "" || model.readIntent.chatID != "loaded@lid" {
		t.Fatal("receipt did not wait for cache page")
	}
	messages := make([]InitialMessage, MaxInitialMessagesPerChat)
	for index := range messages {
		messages[index] = InitialMessage{ID: "id-" + string(rune('A'+index)), SentAt: base.Add(time.Duration(index) * time.Second), Text: "unicode 🐧", BodyRetained: true}
	}
	chat, _ := model.chats.selectedChat()
	if !applyChatLoad(&model, ChatLoadResult{ChatID: chat.id, Revision: chat.revision, Messages: messages}) {
		t.Fatal("cache load failed")
	}
	if model.readRequest.ChatID != "loaded@lid" || len(model.readRequest.Messages) != MaxInitialMessagesPerChat || model.readIntent.chatID != "" {
		t.Fatalf("request=%+v intent=%+v", model.readRequest, model.readIntent)
	}
}

func TestGroupReadFrontierPreservesParticipants(t *testing.T) {
	base := time.Date(2026, 9, 1, 12, 30, 0, 0, time.UTC)
	state, err := chatStateFromInitial(InitialState{Chats: []InitialChat{
		{ID: "first@lid", Title: "First", ActivityTime: base},
		{ID: "group@g.us", Title: "Group", IsGroup: true, UnreadCount: 2, ActivityTime: base.Add(time.Minute), Messages: []InitialMessage{
			{ID: "one", SentAt: base, Text: "one", BodyRetained: true, SenderID: "111@s.whatsapp.net", IsGroup: true},
			{ID: "two", SentAt: base.Add(time.Second), Text: "two", BodyRetained: true, SenderID: "222@lid", IsGroup: true},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	model := viewModel{chats: state}
	moveChatSelection(&model, 1)
	if !model.readRequest.IsGroup || len(model.readRequest.Messages) != 2 ||
		model.readRequest.Messages[0].SenderID != "111@s.whatsapp.net" || model.readRequest.Messages[1].SenderID != "222@lid" {
		t.Fatalf("group request=%+v", model.readRequest)
	}
}

func TestNewUnreadAfterClearCreatesOnlyNextSelectionReceipt(t *testing.T) {
	model := readReceiptView(t)
	moveChatSelection(&model, 1)
	requestPendingReadReceipt(&model, func(ReadReceiptRequest) bool { return true })
	moveChatSelection(&model, -1)
	base := time.Date(2026, 9, 1, 13, 0, 0, 0, time.UTC)
	if !applyLiveMessage(&model, LiveMessage{ChatID: "unread@lid", MessageID: "next", SentAt: base, Text: "new", BodyRetained: true, UnreadCount: 1, ActivityTime: base}) || model.readRequest.ChatID != "" {
		t.Fatal("background next message incorrectly sent receipt")
	}
	index, ok := model.chats.chatIndexByID("unread@lid")
	if !ok || !moveChatSelection(&model, index-model.chats.selectedIndex()) {
		t.Fatal("could not reselect unread chat")
	}
	if model.readRequest.ChatID != "unread@lid" || len(model.readRequest.Messages) != 1 || model.readRequest.Messages[0].MessageID != "next" {
		t.Fatalf("next request=%+v", model.readRequest)
	}
	duplicate := LiveMessage{ChatID: "unread@lid", MessageID: "next", SentAt: base, Text: "new", BodyRetained: true, UnreadCount: 0, ActivityTime: base}
	applyLiveMessage(&model, duplicate)
	if len(model.readRequest.Messages) != 1 {
		t.Fatal("duplicate live event duplicated receipt")
	}
}
