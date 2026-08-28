package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

var liveTestBase = time.Date(2100, 7, 8, 9, 0, 0, 0, time.UTC)

func TestLiveMessagePromotesByActivityAndPreservesSelectedChatID(t *testing.T) {
	model := liveTestView(t)
	model.chats.selected = 1
	event := liveTestMessage("c", "c-live", 35, 40, false, "committed C", 7)
	if !applyLiveMessage(&model, event) {
		t.Fatal("live event was not applied")
	}
	if got := []string{model.chats.chats[0].id, model.chats.chats[1].id, model.chats.chats[2].id}; fmt.Sprint(got) != "[c a b]" {
		t.Fatalf("chat order=%v", got)
	}
	selected, _ := model.chats.selectedChat()
	if selected.id != "b" || model.chats.selectedIndex() != 2 {
		t.Fatalf("selected=%q index=%d", selected.id, model.chats.selectedIndex())
	}
	chatC := &model.chats.chats[0]
	if chatC.unreadCount != 7 || !chatC.activityTime.Equal(liveTestBase.Add(40*time.Minute)) || chatC.messages[0].text != "committed C" {
		t.Fatalf("chat C=%+v message=%+v", chatC, chatC.messages[0])
	}
	before := *model.chats
	if applyLiveMessage(&model, event) || *model.chats != before {
		t.Fatal("duplicate background event promoted or mutated twice")
	}
}

func TestLiveMessageDelayedAndEqualTimestampOrdering(t *testing.T) {
	initial := InitialState{Chats: []InitialChat{{
		ID: "ordered", Title: "Ordered", ActivityTime: liveTestBase.Add(30 * time.Minute),
		Messages: []InitialMessage{
			liveInitialMessage("m10", 10, false, "10"),
			liveInitialMessage("m20", 20, false, "20"),
			liveInitialMessage("m30", 30, false, "30"),
		},
	}}}
	state, err := chatStateFromInitial(initial)
	if err != nil {
		t.Fatal(err)
	}
	model := viewModel{chats: state, options: DefaultOptions(), terminalWidth: 100, terminalHeight: 20}
	if !applyLiveMessage(&model, liveTestMessage("ordered", "m15", 15, 30, false, "15", 4)) {
		t.Fatal("delayed event rejected")
	}
	if !applyLiveMessage(&model, liveTestMessage("ordered", "z-equal", 25, 30, true, "Café 東京 ❤️ 👍🏽 👨‍👩‍👧‍👦", 4)) {
		t.Fatal("first equal-time event rejected")
	}
	if !applyLiveMessage(&model, liveTestMessage("ordered", "a-equal", 25, 30, false, "ASCII", 5)) {
		t.Fatal("second equal-time event rejected")
	}
	chat, _ := model.chats.selectedChat()
	want := "[m10 m15 m20 a-equal z-equal m30]"
	if got := messageIDs(chat); fmt.Sprint(got) != want {
		t.Fatalf("message order=%v want=%s", got, want)
	}
	if !chat.activityTime.Equal(liveTestBase.Add(30*time.Minute)) || chat.unreadCount != 5 {
		t.Fatalf("activity=%v unread=%d", chat.activityTime, chat.unreadCount)
	}
	unicode, found := model.chats.findMessageByID(0, "z-equal")
	if !found || !unicode.fromMe || unicode.text != "Café 東京 ❤️ 👍🏽 👨‍👩‍👧‍👦" {
		t.Fatalf("unicode=%+v found=%t", unicode, found)
	}
}

func TestLiveMessageDuplicateBodylessUnknownAndCapacity(t *testing.T) {
	model := liveTestView(t)
	event := liveTestMessage("a", "bodyless", 31, 31, false, "must disappear", 9)
	event.BodyRetained = false
	if !applyLiveMessage(&model, event) {
		t.Fatal("bodyless event rejected")
	}
	chat, _ := model.chats.selectedChat()
	bodyless, found := model.chats.findMessageByID(model.chats.selectedIndex(), "bodyless")
	if !found || bodyless.bodyRetained || bodyless.text != "" || chat.unreadCount != 9 {
		t.Fatalf("bodyless=%+v found=%t unread=%d", bodyless, found, chat.unreadCount)
	}
	before := *model.chats
	if applyLiveMessage(&model, event) || *model.chats != before {
		t.Fatal("identical duplicate changed presentation state")
	}
	unknown := liveTestMessage("unknown", "unknown-message", 50, 50, false, "ignored", 1)
	selectedID := chat.id
	if applyLiveMessage(&model, unknown) || model.chats.chatCount != 3 || model.chats.chats[model.chats.selected].id != selectedID {
		t.Fatal("unknown chat event changed bounded working set")
	}

	full := InitialChat{ID: "full", Title: "Full", ActivityTime: liveTestBase.Add(40 * time.Minute), Messages: make([]InitialMessage, maxMessages)}
	for index := range full.Messages {
		full.Messages[index] = liveInitialMessage(fmt.Sprintf("m%02d", index), index, false, fmt.Sprintf("message %02d", index))
	}
	state, _ := chatStateFromInitial(InitialState{Chats: []InitialChat{full}})
	bounded := viewModel{chats: state, options: DefaultOptions(), terminalWidth: 100, terminalHeight: 20}
	if !applyLiveMessage(&bounded, liveTestMessage("full", "newest", 50, 50, false, "newest", 1)) {
		t.Fatal("bounded newest event rejected")
	}
	boundedChat, _ := bounded.chats.selectedChat()
	if boundedChat.messageCount != maxMessages || boundedChat.messages[0].id != "m01" || boundedChat.messages[maxMessages-1].id != "newest" {
		t.Fatalf("bounded messages first=%q last=%q count=%d", boundedChat.messages[0].id, boundedChat.messages[maxMessages-1].id, boundedChat.messageCount)
	}
}

func TestLiveMessagePreservesComposeReplyPopupAndScrollState(t *testing.T) {
	model := liveTestView(t)
	model.mode = modeCompose
	if !model.composer.insertText("draft survives") {
		t.Fatal("draft setup failed")
	}
	model.composer.cursor = len("draft")
	chat, _ := model.chats.selectedChat()
	targetIndex := chat.messageCount - 2
	targetID := chat.messages[targetIndex].id
	model.replyTarget = replyTarget{valid: true, id: targetID}
	model.replySelect = replySelectionState{valid: true, index: targetIndex, fromCompose: true}
	model.emojiPicker.prepareOpen()
	model.settingsOpen = true
	model.chatView.scrollOffset = 1
	wantDraft, wantCursor, wantTarget := model.composer.text(), model.composer.cursor, model.replyTarget

	selectedEvent := liveTestMessage("a", "selected-live", 31, 31, false, "selected", 6)
	if !applyLiveMessage(&model, selectedEvent) {
		t.Fatal("selected live event rejected")
	}
	selected, _ := model.chats.selectedChat()
	if selected.id != "a" || model.mode != modeCompose || model.composer.text() != wantDraft || model.composer.cursor != wantCursor ||
		model.replyTarget != wantTarget || !model.replySelect.valid || selected.messages[model.replySelect.index].id != targetID ||
		!model.emojiPicker.open || !model.settingsOpen || model.chatView.scrollOffset != 2 {
		t.Fatalf("selected live state mode=%d draft=%q cursor=%d target=%+v selection=%+v popup=%t settings=%t offset=%d",
			model.mode, model.composer.text(), model.composer.cursor, model.replyTarget, model.replySelect,
			model.emojiPicker.open, model.settingsOpen, model.chatView.scrollOffset)
	}

	backgroundEvent := liveTestMessage("c", "background-live", 32, 50, false, "background", 8)
	if !applyLiveMessage(&model, backgroundEvent) {
		t.Fatal("background live event rejected")
	}
	selected, _ = model.chats.selectedChat()
	if selected.id != "a" || model.composer.text() != wantDraft || model.composer.cursor != wantCursor || model.replyTarget != wantTarget || model.chatView.scrollOffset != 2 {
		t.Fatalf("background event changed selected state: selected=%q draft=%q offset=%d", selected.id, model.composer.text(), model.chatView.scrollOffset)
	}
}

func TestLiveMessageViewportNewestOlderBackgroundAndResize(t *testing.T) {
	atNewest := liveTestView(t)
	if !applyLiveMessage(&atNewest, liveTestMessage("a", "newest-live", 31, 31, false, "newest", 1)) || atNewest.chatView.scrollOffset != 0 {
		t.Fatalf("newest offset=%d", atNewest.chatView.scrollOffset)
	}

	reading := liveTestView(t)
	reading.chatView.scrollOffset = 1
	if !applyLiveMessage(&reading, liveTestMessage("a", "while-reading", 31, 31, false, "reading", 1)) {
		t.Fatal("reading event rejected")
	}
	if reading.chatView.scrollOffset != 2 || newerMessageCount(&reading, 100, 20) < 2 {
		t.Fatalf("reading offset=%d newer=%d", reading.chatView.scrollOffset, newerMessageCount(&reading, 100, 20))
	}
	before := reading.chatView.scrollOffset
	if !applyLiveMessage(&reading, liveTestMessage("c", "background-scroll", 32, 50, false, "background", 2)) || reading.chatView.scrollOffset != before {
		t.Fatalf("background offset=%d want=%d", reading.chatView.scrollOffset, before)
	}

	screen := initializedSimulationScreen(t, 100, 20)
	resizeFirst := liveTestView(t)
	screen.SetSize(60, 12)
	clampView(&resizeFirst, 60, 12)
	if !applyLiveMessage(&resizeFirst, liveTestMessage("a", "resize-first", 31, 31, false, "resize first", 1)) {
		t.Fatal("resize-then-live rejected")
	}
	draw(screen, &resizeFirst)
	liveFirst := liveTestView(t)
	if !applyLiveMessage(&liveFirst, liveTestMessage("a", "live-first", 31, 31, false, "live first", 1)) {
		t.Fatal("live-then-resize rejected")
	}
	clampView(&liveFirst, 60, 12)
	draw(screen, &liveFirst)
}

func TestRunLiveEventsRedrawOnceAndClosedChannelRemainsUsable(t *testing.T) {
	screen := newObservedScreen(100, 30)
	live := make(chan LiveMessage)
	result := make(chan error, 1)
	go func() {
		result <- runWithDependencies(context.Background(), screen, Input{Options: DefaultOptions(), InitialState: testInitialState(), LiveEvents: live}, "")
	}()
	<-screen.shown
	event := LiveMessage{
		ChatID: "demo", MessageID: "run-live", SentAt: liveTestBase.Add(60 * time.Minute),
		Text: "run live body", BodyRetained: true, UnreadCount: 1, ActivityTime: liveTestBase.Add(60 * time.Minute),
	}
	live <- event
	<-screen.shown
	if text := screenText(screen); !strings.Contains(text, "run live body") {
		t.Fatalf("live frame missing body:\n%s", text)
	}
	live <- event
	close(live)
	screen.InjectKey(tcell.KeyRune, 'j', tcell.ModNone)
	<-screen.shown
	if text := screenText(screen); !strings.Contains(text, "Project Room") {
		t.Fatalf("closed live channel prevented terminal input or duplicate redrew:\n%s", text)
	}
	if got := screen.showCount.Load(); got != 3 {
		t.Fatalf("Show calls=%d want=3", got)
	}
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func liveTestView(t *testing.T) viewModel {
	t.Helper()
	aMessages := make([]InitialMessage, 20)
	for index := range aMessages {
		aMessages[index] = liveInitialMessage(fmt.Sprintf("a%02d", index+1), index+1, index%2 == 1, fmt.Sprintf("A message %02d", index+1))
	}
	initial := InitialState{Chats: []InitialChat{
		{ID: "a", Title: "A", ActivityTime: liveTestBase.Add(30 * time.Minute), Messages: aMessages},
		{ID: "b", Title: "B", ActivityTime: liveTestBase.Add(20 * time.Minute), Messages: []InitialMessage{liveInitialMessage("b20", 20, false, "B twenty")}},
		{ID: "c", Title: "C", ActivityTime: liveTestBase.Add(10 * time.Minute)},
	}}
	state, err := chatStateFromInitial(initial)
	if err != nil {
		t.Fatal(err)
	}
	return viewModel{chats: state, options: DefaultOptions(), terminalWidth: 100, terminalHeight: 20}
}

func liveInitialMessage(id string, minute int, fromMe bool, text string) InitialMessage {
	return InitialMessage{ID: id, SentAt: liveTestBase.Add(time.Duration(minute) * time.Minute), FromMe: fromMe, Text: text, BodyRetained: true}
}

func liveTestMessage(chatID, messageID string, sentMinute, activityMinute int, fromMe bool, text string, unread uint32) LiveMessage {
	return LiveMessage{
		ChatID: chatID, MessageID: messageID, SentAt: liveTestBase.Add(time.Duration(sentMinute) * time.Minute),
		FromMe: fromMe, Text: text, BodyRetained: true, UnreadCount: unread,
		ActivityTime: liveTestBase.Add(time.Duration(activityMinute) * time.Minute),
	}
}

func messageIDs(chat *chatView) []messageID {
	ids := make([]messageID, chat.messageCount)
	for index := range ids {
		ids[index] = chat.messages[index].id
	}
	return ids
}
