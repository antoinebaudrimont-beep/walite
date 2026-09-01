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

func TestLiveMessageDuplicateBodylessAndMessageCapacity(t *testing.T) {
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

func TestFirstLiveMessageCreatesSelectedChatFromEmptyState(t *testing.T) {
	tests := []struct {
		name         string
		text         string
		bodyRetained bool
		fromMe       bool
	}{
		{name: "retained Unicode", text: "Café 東京 ❤️ 👍🏽 👨‍👩‍👧‍👦", bodyRetained: true, fromMe: true},
		{name: "bodyless", text: "must disappear", bodyRetained: false, fromMe: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := chatStateFromInitial(InitialState{})
			if err != nil {
				t.Fatal(err)
			}
			model := viewModel{chats: state, options: DefaultOptions(), terminalWidth: 100, terminalHeight: 20}
			event := liveTestMessage("first-chat", "first-message", 70, 80, test.fromMe, test.text, 9)
			event.BodyRetained = test.bodyRetained
			if !applyLiveMessage(&model, event) {
				t.Fatal("first live message was not applied")
			}
			if model.chats.chatCount != 1 || len(model.chats.chats) != ChatWorkingSetCapacity || model.chats.selectedIndex() != 0 {
				t.Fatalf("working set count=%d capacity=%d selected=%d", model.chats.chatCount, len(model.chats.chats), model.chats.selectedIndex())
			}
			chat, selected := model.chats.selectedChat()
			if !selected || chat.id != event.ChatID || chat.title != event.ChatID || chat.unreadCount != event.UnreadCount ||
				!chat.activityTime.Equal(event.ActivityTime) || chat.messageCount != 1 {
				t.Fatalf("selected=%t chat=%+v", selected, chat)
			}
			message := chat.messages[0]
			wantText := event.Text
			if !event.BodyRetained {
				wantText = ""
			}
			if message.id != messageID(event.MessageID) || !message.sentAt.Equal(event.SentAt) ||
				message.text != wantText || message.bodyRetained != event.BodyRetained || message.fromMe != event.FromMe {
				t.Fatalf("message=%+v", message)
			}

			screen := initializedSimulationScreen(t, 100, 20)
			draw(screen, &model)
			screen.Show()
			rendered := screenText(screen)
			if !strings.Contains(rendered, "> first-chat (9)") {
				t.Fatalf("first chat not visible:\n%s", rendered)
			}
			if event.BodyRetained {
				if !strings.Contains(rendered, "Café") || !strings.Contains(rendered, "東") || !strings.Contains(rendered, "❤") {
					t.Fatalf("Unicode message not visible:\n%s", rendered)
				}
			} else if strings.Contains(rendered, event.Text) {
				t.Fatalf("bodyless text leaked:\n%s", rendered)
			}
		})
	}
}

func TestLiveMessageInsertsFifthChatWithExactPresentationData(t *testing.T) {
	model := defaultDemoView()
	selected, _ := model.chats.selectedChat()
	selectedID := selected.id
	event := liveTestMessage("new-contact", "new-unicode", 50, 60, true, "Café 東京 ❤️ 👍🏽 👨‍👩‍👧‍👦", 7)
	if !applyLiveMessage(&model, event) {
		t.Fatal("fifth chat event was not applied")
	}
	if model.chats.chatCount != 5 {
		t.Fatalf("chat count=%d want=5", model.chats.chatCount)
	}
	chatIndex, found := model.chats.chatIndexByID("new-contact")
	if !found {
		t.Fatal("new chat missing")
	}
	chat := &model.chats.chats[chatIndex]
	if chat.id != "new-contact" || chat.title != "new-contact" || chat.unreadCount != 7 ||
		!chat.activityTime.Equal(event.ActivityTime) || chat.messageCount != 1 {
		t.Fatalf("new chat=%+v", chat)
	}
	message := chat.messages[0]
	if message.id != "new-unicode" || !message.sentAt.Equal(event.SentAt) || !message.fromMe ||
		!message.bodyRetained || message.text != event.Text {
		t.Fatalf("new message=%+v", message)
	}
	selected, _ = model.chats.selectedChat()
	if selected.id != selectedID || model.chats.chats[0].id != "new-contact" {
		t.Fatalf("selected=%q top=%q", selected.id, model.chats.chats[0].id)
	}
	before := *model.chats
	if applyLiveMessage(&model, event) || *model.chats != before {
		t.Fatal("duplicate new-chat event changed presentation state")
	}

	bodyless := liveTestMessage("bodyless-contact", "bodyless-new", 61, 61, true, "must disappear", 8)
	bodyless.BodyRetained = false
	if !applyLiveMessage(&model, bodyless) {
		t.Fatal("bodyless new-chat event was not applied")
	}
	bodylessIndex, _ := model.chats.chatIndexByID(bodyless.ChatID)
	bodylessMessage := model.chats.chats[bodylessIndex].messages[0]
	if bodylessMessage.text != "" || bodylessMessage.bodyRetained || !bodylessMessage.fromMe {
		t.Fatalf("bodyless new message=%+v", bodylessMessage)
	}
}

func TestNewLiveChatsUseDeterministicActivityAndIDOrder(t *testing.T) {
	model := liveTestView(t)
	for _, event := range []LiveMessage{
		liveTestMessage("new-z", "z-message", 40, 50, false, "z", 1),
		liveTestMessage("new-a", "a-message", 41, 50, false, "a", 1),
		liveTestMessage("new-newest", "newest-message", 60, 60, false, "newest", 1),
	} {
		if !applyLiveMessage(&model, event) {
			t.Fatalf("event for %q was not applied", event.ChatID)
		}
	}
	got := []string{
		model.chats.chats[0].id,
		model.chats.chats[1].id,
		model.chats.chats[2].id,
	}
	if fmt.Sprint(got) != "[new-newest new-a new-z]" {
		t.Fatalf("new chat order=%v", got)
	}
}

func TestNewLiveChatPreservesTransientSelectedState(t *testing.T) {
	model := liveTestView(t)
	model.terminalHeight = 12
	model.mode = modeCompose
	if !model.composer.insertText("draft survives new chat") {
		t.Fatal("draft setup failed")
	}
	model.composer.cursor = len("draft")
	selected, _ := model.chats.selectedChat()
	targetIndex := selected.messageCount - 3
	targetID := selected.messages[targetIndex].id
	model.replyTarget = replyTarget{valid: true, id: targetID}
	model.replySelect = replySelectionState{valid: true, index: targetIndex, fromCompose: true}
	model.chatView.scrollOffset = 2
	model.emojiPicker.prepareOpen()
	model.settingsOpen = true
	wantComposer := model.composer
	wantReplyTarget := model.replyTarget
	wantReplySelect := model.replySelect
	wantChatView := model.chatView
	wantEmoji := model.emojiPicker
	wantSettings := model.settingsOpen
	wantSelected := selected.id

	if !applyLiveMessage(&model, liveTestMessage("background-new", "background-message", 50, 60, false, "background", 3)) {
		t.Fatal("background new chat was not inserted")
	}
	selected, _ = model.chats.selectedChat()
	if selected.id != wantSelected || model.mode != modeCompose || model.composer != wantComposer ||
		model.replyTarget != wantReplyTarget || model.replySelect != wantReplySelect ||
		model.chatView != wantChatView || model.emojiPicker != wantEmoji || model.settingsOpen != wantSettings {
		t.Fatalf("transient state changed: selected=%q mode=%d cursor=%d reply=%+v selection=%+v view=%+v popup=%t settings=%t",
			selected.id, model.mode, model.composer.cursor, model.replyTarget, model.replySelect,
			model.chatView, model.emojiPicker.open, model.settingsOpen)
	}
}

func TestNewLiveChatAtSummaryCapacityIsIgnoredDeterministically(t *testing.T) {
	initial := InitialState{Chats: make([]InitialChat, ChatWorkingSetCapacity)}
	for index := range initial.Chats {
		initial.Chats[index] = InitialChat{
			ID:           fmt.Sprintf("full-%02d", index),
			Title:        fmt.Sprintf("Full %02d", index),
			ActivityTime: liveTestBase.Add(time.Duration(ChatWorkingSetCapacity-index) * time.Minute),
		}
	}
	state, err := chatStateFromInitial(initial)
	if err != nil {
		t.Fatal(err)
	}
	state.selected = ChatWorkingSetCapacity - 1
	model := viewModel{chats: state, options: DefaultOptions(), terminalWidth: 100, terminalHeight: 20}
	selectedID := state.chats[state.selected].id

	before := *model.chats
	newest := liveTestMessage("capacity-new", "capacity-message", 100, 100, false, "bounded", 4)
	if applyLiveMessage(&model, newest) || *model.chats != before {
		t.Fatal("10,001st chat changed the bounded summary set")
	}
	if model.chats.chatCount != ChatWorkingSetCapacity {
		t.Fatalf("chat count=%d bound=%d", model.chats.chatCount, ChatWorkingSetCapacity)
	}
	if _, found := model.chats.chatIndexByID("capacity-new"); found {
		t.Fatal("over-capacity chat was admitted")
	}
	selected, _ := model.chats.selectedChat()
	if selected.id != selectedID {
		t.Fatalf("selected chat=%q want=%q", selected.id, selectedID)
	}

	stale := liveTestMessage("capacity-stale", "stale-message", -2, -1, false, "outside working set", 1)
	if applyLiveMessage(&model, stale) || *model.chats != before {
		t.Fatal("stale new chat displaced the bounded working set")
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
