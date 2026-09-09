package tui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func TestOlderHistoryKeyUsesSelectedStableFrontierOnceAndBoundsCount(t *testing.T) {
	model := defaultDemoView()
	const width, height = 100, 12
	model.terminalWidth, model.terminalHeight = width, height
	model.chatView.scrollOffset = maximumScrollOffset(&model, width, height)
	frontier := model.chats.chats[0].messages[0]
	requests := make([]OlderHistoryRequest, 0, 1)
	model.loadOlder = func(request OlderHistoryRequest) bool {
		requests = append(requests, request)
		return true
	}
	key := tcell.NewEventKey(tcell.KeyRune, 'O', tcell.ModNone)
	if changed, exit := handleKey(&model, key, width, height); !changed || exit {
		t.Fatalf("O changed=%t exit=%t", changed, exit)
	}
	if len(requests) != 1 {
		t.Fatalf("requests=%d", len(requests))
	}
	request := requests[0]
	if request.ChatID != "demo" || request.Revision != model.selectionEpoch || request.OldestMessageID != string(frontier.id) ||
		!request.OldestSentAt.Equal(frontier.sentAt) || request.OldestFromMe != frontier.fromMe || request.Count != 50 {
		t.Fatalf("request=%+v frontier=%+v", request, frontier)
	}
	if changed, exit := handleKey(&model, key, width, height); !changed || exit || len(requests) != 1 || model.sendStatus != "Loading older messages…" {
		t.Fatalf("repeat changed=%t exit=%t requests=%d status=%q", changed, exit, len(requests), model.sendStatus)
	}
}

func TestOlderHistoryRequiresBoundaryAndReportsDisconnected(t *testing.T) {
	model := defaultDemoView()
	called := 0
	model.loadOlder = func(OlderHistoryRequest) bool { called++; return true }
	key := tcell.NewEventKey(tcell.KeyRune, 'O', tcell.ModNone)
	if changed, _ := handleKey(&model, key, 100, 12); !changed || called != 0 || !strings.Contains(model.sendStatus, "oldest loaded") {
		t.Fatalf("changed=%t called=%d status=%q", changed, called, model.sendStatus)
	}
	model.chatView.scrollOffset = maximumScrollOffset(&model, 100, 12)
	model.loadOlder = nil
	if changed, _ := handleKey(&model, key, 100, 12); !changed || model.sendStatus != "Older history unavailable while disconnected" {
		t.Fatalf("changed=%t status=%q", changed, model.sendStatus)
	}
}

func TestOlderHistoryMergePreservesBoundedPresentationMetadataUnreadAndDates(t *testing.T) {
	currentDay := time.Date(2026, 9, 9, 12, 0, 0, 0, time.Local)
	state, err := chatStateFromInitial(InitialState{Chats: []InitialChat{{
		ID: "chat@lid", Title: "Chat", UnreadCount: 7, ActivityTime: currentDay,
		Messages: []InitialMessage{
			{ID: "current-oldest", SentAt: currentDay, Text: "current anchor", BodyRetained: true},
			{ID: "current-newest", SentAt: currentDay.Add(time.Minute), FromMe: true, Text: "current tail", BodyRetained: true},
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	model := viewModel{chats: state, options: DefaultOptions(), terminalWidth: 100, terminalHeight: 30}
	model.chatView.scrollOffset = maximumScrollOffset(&model, 100, 30)
	var request OlderHistoryRequest
	model.loadOlder = func(value OlderHistoryRequest) bool { request = value; return true }
	requestOlderHistory(&model, 100, 30)
	firstDay := currentDay.Add(-48 * time.Hour)
	secondDay := currentDay.Add(-24 * time.Hour)
	result := OlderHistoryResult{Request: request, Kind: OlderHistoryLoaded, Messages: []InitialMessage{
		{ID: "history-image", SentAt: firstDay, Text: "Holiday Café 👋", BodyRetained: true, MediaKind: mediaImage, SenderID: "person@lid", IsGroup: true},
		{ID: "history-reply", SentAt: secondDay, FromMe: true, Text: "older reply 日本語", BodyRetained: true, ReplyToID: "history-image", ReplyToText: "Holiday Café 👋", ReplyMediaKind: mediaImage},
	}}
	if !applyOlderHistoryResult(&model, result) {
		t.Fatal("older page was not applied")
	}
	chat, _ := model.chats.selectedChat()
	if chat.messageCount != 4 || chat.unreadCount != 7 || chat.messages[0].id != "history-image" || chat.messages[1].id != "history-reply" || chat.messageCount > SelectedChatHistoryCapacity {
		t.Fatalf("chat messages=%d unread=%d first=%q second=%q", chat.messageCount, chat.unreadCount, chat.messages[0].id, chat.messages[1].id)
	}
	if chat.messages[0].mediaKind != mediaImage || chat.messages[1].replyMediaKind != mediaImage || chat.messages[1].replyText != "Holiday Café 👋" {
		t.Fatalf("media/reply metadata lost: %+v %+v", chat.messages[0], chat.messages[1])
	}
	screen := initializedSimulationScreen(t, 100, 30)
	draw(screen, &model)
	screen.Show()
	text := screenText(screen)
	for _, want := range []string{"[Image] Holiday Café 👋", "↪ " + localMessageTime(firstDay) + " [Image] Holiday Café 👋", messageDateLabel(firstDay), messageDateLabel(secondDay), messageDateLabel(currentDay)} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q after merge:\n%s", want, text)
		}
	}
}

func TestOlderHistoryResultIsStaleAfterChatSwitch(t *testing.T) {
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	state, _ := chatStateFromInitial(InitialState{Chats: []InitialChat{
		{ID: "a@lid", Title: "A", ActivityTime: base, Messages: []InitialMessage{{ID: "a-frontier", SentAt: base, Text: "A", BodyRetained: true}}},
		{ID: "b@lid", Title: "B", ActivityTime: base.Add(-time.Hour), Messages: []InitialMessage{{ID: "b-frontier", SentAt: base, Text: "B", BodyRetained: true}}},
	}})
	model := viewModel{chats: state, options: DefaultOptions(), terminalWidth: 100, terminalHeight: 30}
	var request OlderHistoryRequest
	model.loadOlder = func(value OlderHistoryRequest) bool { request = value; return true }
	requestOlderHistory(&model, 100, 30)
	if !moveChatSelection(&model, 1) {
		t.Fatal("chat switch failed")
	}
	before := *model.chats
	if applyOlderHistoryResult(&model, OlderHistoryResult{Request: request, Kind: OlderHistoryLoaded, Messages: []InitialMessage{{ID: "older-a", SentAt: base.Add(-time.Hour), Text: "old", BodyRetained: true}}}) {
		t.Fatal("stale result redrew selected chat")
	}
	if !chatStatesEqual(*model.chats, before) || model.chats.selectedChatMustForTest().id != "b@lid" {
		t.Fatal("stale result changed chat state")
	}
}

func TestOlderHistoryNoMoreStatus(t *testing.T) {
	model := defaultDemoView()
	model.terminalWidth, model.terminalHeight = 100, 30
	model.chatView.scrollOffset = maximumScrollOffset(&model, 100, 30)
	var request OlderHistoryRequest
	model.loadOlder = func(value OlderHistoryRequest) bool { request = value; return true }
	requestOlderHistory(&model, 100, 30)
	if !applyOlderHistoryResult(&model, OlderHistoryResult{Request: request, Kind: OlderHistoryNoMore}) || model.sendStatus != "No older messages available" {
		t.Fatalf("status=%q", model.sendStatus)
	}
}

func TestOlderHistoryPagesAccumulateWithoutReplacingRecentMessages(t *testing.T) {
	base := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	state, err := chatStateFromInitial(InitialState{Chats: []InitialChat{{
		ID: "chat@lid", Title: "Chat", ActivityTime: base.Add(1031 * time.Minute),
		Messages: historyMessages(base, 1000, MaxInitialMessagesPerChat, "recent"),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	model := viewModel{chats: state, options: DefaultOptions(), terminalWidth: 100, terminalHeight: 12}
	var request OlderHistoryRequest
	model.loadOlder = func(value OlderHistoryRequest) bool { request = value; return true }

	for page := 0; page < 5; page++ {
		model.chatView.scrollOffset = maximumScrollOffset(&model, 100, 12)
		_, visibleEnd := visibleMessageRange(&model, 100, 12)
		anchor := model.chats.chats[0].messages[visibleEnd-1].id
		beforeOffset := model.chatView.scrollOffset
		if !requestOlderHistory(&model, 100, 12) {
			t.Fatalf("page %d request not handled", page)
		}
		start := 950 - page*50
		if !applyOlderHistoryResult(&model, OlderHistoryResult{
			Request: request, Kind: OlderHistoryLoaded, Messages: historyMessages(base, start, 50, "older"),
		}) {
			t.Fatalf("page %d result not applied", page)
		}
		if model.chatView.scrollOffset != beforeOffset {
			t.Fatalf("page %d viewport offset changed from %d to %d", page, beforeOffset, model.chatView.scrollOffset)
		}
		_, afterEnd := visibleMessageRange(&model, 100, 12)
		if got := model.chats.chats[0].messages[afterEnd-1].id; got != anchor {
			t.Fatalf("page %d visible anchor changed from %q to %q", page, anchor, got)
		}
	}
	chat, _ := model.chats.selectedChat()
	if chat.messageCount != SelectedChatHistoryCapacity || len(chat.messages) != SelectedChatHistoryCapacity {
		t.Fatalf("selected buffer count=%d capacity=%d", chat.messageCount, len(chat.messages))
	}
	for index := 0; index < MaxInitialMessagesPerChat; index++ {
		id := messageID("recent-" + decimal(1000+index))
		if _, found := model.chats.findMessageByID(0, id); !found {
			t.Fatalf("recent message %q was evicted by older history", id)
		}
	}
	if got := chat.messages[chat.messageCount-1].id; got != "recent-1031" {
		t.Fatalf("newest retained=%q", got)
	}
	model.chatView.scrollOffset = maximumScrollOffset(&model, 100, 12)
	request = OlderHistoryRequest{}
	requestOlderHistory(&model, 100, 12)
	if request.ChatID != "" || model.sendStatus != "Selected chat history limit reached" {
		t.Fatalf("overflow request=%+v status=%q", request, model.sendStatus)
	}
}

func TestOlderHistoryMergeKeepsConcurrentIncomingOutgoingAndDraft(t *testing.T) {
	base := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	state, _ := chatStateFromInitial(InitialState{Chats: []InitialChat{{
		ID: "chat@lid", Title: "Chat", ActivityTime: base.Add(11 * time.Minute),
		Messages: historyMessages(base, 0, 12, "recent"),
	}}})
	model := viewModel{chats: state, options: DefaultOptions(), terminalWidth: 100, terminalHeight: 12, mode: modeCompose}
	model.composer.insertText("draft Café 👋")
	model.replySelect = replySelectionState{valid: true, index: 5, fromCompose: true}
	focusedID := model.chats.chats[0].messages[model.replySelect.index].id
	model.chatView.scrollOffset = maximumScrollOffset(&model, 100, 12)
	draft, cursor := model.composer.text(), model.composer.cursor
	var request OlderHistoryRequest
	model.loadOlder = func(value OlderHistoryRequest) bool { request = value; return true }
	requestOlderHistory(&model, 100, 12)
	for _, event := range []LiveMessage{
		{ChatID: "chat@lid", MessageID: "incoming-D", SentAt: base.Add(12 * time.Minute), Text: "D 日本語", BodyRetained: true, UnreadCount: 1, ActivityTime: base.Add(12 * time.Minute)},
		{ChatID: "chat@lid", MessageID: "outgoing-E", SentAt: base.Add(13 * time.Minute), FromMe: true, Text: "E 👋", BodyRetained: true, UnreadCount: 1, ActivityTime: base.Add(13 * time.Minute)},
	} {
		if !applyLiveMessage(&model, event) {
			t.Fatalf("live event %q rejected", event.MessageID)
		}
	}
	if !applyOlderHistoryResult(&model, OlderHistoryResult{
		Request: request, Kind: OlderHistoryLoaded, Messages: historyMessages(base, -2, 2, "older"),
	}) {
		t.Fatal("concurrent older result rejected")
	}
	chat, _ := model.chats.selectedChat()
	if got := messageIDs(chat); len(got) != 16 || got[0] != "older--2" || got[1] != "older--1" || got[14] != "incoming-D" || got[15] != "outgoing-E" {
		t.Fatalf("merged IDs=%v", got)
	}
	if model.composer.text() != draft || model.composer.cursor != cursor || model.mode != modeCompose {
		t.Fatalf("draft changed: text=%q cursor=%d mode=%d", model.composer.text(), model.composer.cursor, model.mode)
	}
	if !model.replySelect.valid || model.replySelect.index < 0 || model.replySelect.index >= chat.messageCount || chat.messages[model.replySelect.index].id != focusedID || !model.replySelect.fromCompose {
		t.Fatalf("reply focus changed: %+v want=%q", model.replySelect, focusedID)
	}
	if model.chatView.scrollOffset == 0 {
		t.Fatal("concurrent live events forced viewport to newest")
	}
	jumpMessageViewport(&model, 100, 12, false)
	if model.chatView.scrollOffset != 0 || chat.messages[chat.messageCount-1].id != "outgoing-E" {
		t.Fatal("End did not expose newest retained message")
	}
}

func TestOlderHistoryDeduplicatesAndRejectsABACompletion(t *testing.T) {
	base := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	state, _ := chatStateFromInitial(InitialState{Chats: []InitialChat{
		{ID: "a@lid", Title: "A", ActivityTime: base, Messages: historyMessages(base, 0, 2, "a")},
		{ID: "b@lid", Title: "B", ActivityTime: base, Messages: historyMessages(base, 0, 1, "b")},
	}})
	model := viewModel{chats: state, options: DefaultOptions(), terminalWidth: 100, terminalHeight: 12}
	var stale OlderHistoryRequest
	model.loadOlder = func(value OlderHistoryRequest) bool { stale = value; return true }
	model.chatView.scrollOffset = maximumScrollOffset(&model, 100, 12)
	requestOlderHistory(&model, 100, 12)
	moveChatSelection(&model, 1)
	moveChatSelection(&model, -1)
	before := *model.chats
	if applyOlderHistoryResult(&model, OlderHistoryResult{Request: stale, Kind: OlderHistoryLoaded, Messages: historyMessages(base, -1, 1, "old")}) || !chatStatesEqual(*model.chats, before) {
		t.Fatal("A→B→A completion was not rejected")
	}

	model.chatView.scrollOffset = maximumScrollOffset(&model, 100, 12)
	requestOlderHistory(&model, 100, 12)
	duplicate := InitialMessage{ID: "a-1", SentAt: base.Add(-time.Minute), Text: "duplicate", BodyRetained: true}
	if !applyOlderHistoryResult(&model, OlderHistoryResult{Request: stale, Kind: OlderHistoryLoaded, Messages: []InitialMessage{duplicate}}) {
		t.Fatal("duplicate page was not handled")
	}
	if model.chats.chats[0].messageCount != 2 {
		t.Fatal("duplicate history message was inserted")
	}
}

func TestOnlySelectedChatOwnsExpandedHistoryBuffer(t *testing.T) {
	base := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	state, _ := chatStateFromInitial(InitialState{Chats: []InitialChat{
		{ID: "a", Title: "A", Messages: historyMessages(base, 0, 32, "a")},
		{ID: "b", Title: "B", Messages: historyMessages(base, 0, 32, "b")},
		{ID: "c", Title: "C"},
	}})
	if len(state.chats[0].messages) != SelectedChatHistoryCapacity || len(state.chats[1].messages) != maxMessages || state.chats[2].messages != nil {
		t.Fatalf("initial capacities=%d,%d,%d", len(state.chats[0].messages), len(state.chats[1].messages), len(state.chats[2].messages))
	}
	if !state.moveSelection(1) {
		t.Fatal("selection failed")
	}
	if len(state.chats[0].messages) != maxMessages || len(state.chats[1].messages) != SelectedChatHistoryCapacity || state.chats[2].messages != nil {
		t.Fatalf("moved capacities=%d,%d,%d", len(state.chats[0].messages), len(state.chats[1].messages), len(state.chats[2].messages))
	}
}

func historyMessages(base time.Time, start, count int, prefix string) []InitialMessage {
	messages := make([]InitialMessage, count)
	for index := range messages {
		sequence := start + index
		messages[index] = InitialMessage{
			ID: prefix + "-" + strconv.Itoa(sequence), SentAt: base.Add(time.Duration(sequence) * time.Minute),
			Text: prefix + " " + strconv.Itoa(sequence), BodyRetained: true,
		}
	}
	return messages
}

func TestEndReturnsToNewestRetainedMessage(t *testing.T) {
	model := defaultDemoView()
	model.chatView.scrollOffset = maximumScrollOffset(&model, 100, 12)
	end := tcell.NewEventKey(tcell.KeyEnd, 0, tcell.ModNone)
	if changed, exit := handleKey(&model, end, 100, 12); !changed || exit || model.chatView.scrollOffset != 0 {
		t.Fatalf("End changed=%t exit=%t view=%+v", changed, exit, model.chatView)
	}
	if changed, exit := handleKey(&model, end, 100, 12); changed || exit {
		t.Fatalf("repeat End changed=%t exit=%t", changed, exit)
	}
}

func (state *chatState) selectedChatMustForTest() *chatView {
	chat, _ := state.selectedChat()
	return chat
}
