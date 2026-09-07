package tui

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func TestGroupParticipantNamesSurviveRepeatedChatReload(t *testing.T) {
	base := time.Date(2100, 2, 3, 10, 0, 0, 0, time.UTC)
	state, err := chatStateFromInitial(InitialState{Chats: []InitialChat{
		{ID: "group-a", Title: "Group A", IsGroup: true, ActivityTime: base},
		{ID: "chat-b", Title: "Chat B", ActivityTime: base.Add(-time.Minute)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	model := viewModel{chats: state, options: DefaultOptions(), terminalWidth: 100, terminalHeight: 24}

	messages := []InitialMessage{
		{ID: "message-1", SentAt: base, Text: "one", BodyRetained: true, SenderID: "participant-1@lid", IsGroup: true},
		{ID: "message-2", SentAt: base.Add(time.Minute), Text: "two", BodyRetained: true, SenderID: "participant-2@lid", IsGroup: true},
		{ID: "message-3", SentAt: base.Add(2 * time.Minute), Text: "unknown", BodyRetained: true, SenderID: "unknown@lid", IsGroup: true},
	}
	request, _ := selectedChatLoadRequest(&model)
	if !applyChatLoad(&model, ChatLoadResult{ChatID: request.ChatID, Revision: request.Revision, Messages: messages}) {
		t.Fatal("initial group page rejected")
	}
	for _, value := range []DisplayMetadata{
		{ID: "participant-1@lid", Name: "Léa Baudrimont", Quality: 4},
		{ID: "participant-2@lid", Name: "Marine 👋", Quality: 4},
	} {
		if !applyDisplayMetadata(&model, value) {
			t.Fatalf("initial metadata did not enrich %q", value.ID)
		}
	}
	assertGroupSenderNames(t, &model, "Léa Baudrimont", "Marine 👋", "unknown@lid")

	for cycle := 0; cycle < 3; cycle++ {
		if !moveChatSelection(&model, 1) || !moveChatSelection(&model, -1) {
			t.Fatalf("cycle %d selection failed", cycle)
		}
		request, _ = selectedChatLoadRequest(&model)
		// A retained resolver response is commonly delivered before the much
		// faster second page replaces the already-enriched in-memory page.
		for _, value := range []DisplayMetadata{
			{ID: "participant-1@lid", Name: "Léa Baudrimont", Quality: 4},
			{ID: "participant-2@lid", Name: "Marine 👋", Quality: 4},
		} {
			if applyDisplayMetadata(&model, value) {
				t.Fatalf("cycle %d retained metadata unexpectedly changed old page", cycle)
			}
		}
		if !applyChatLoad(&model, ChatLoadResult{ChatID: request.ChatID, Revision: request.Revision, Messages: messages}) {
			t.Fatalf("cycle %d reloaded page rejected", cycle)
		}
		assertGroupSenderNames(t, &model, "Léa Baudrimont", "Marine 👋", "unknown@lid")
	}
}

func TestGroupParticipantNameRehydratesAfterPresentationCacheEviction(t *testing.T) {
	base := time.Date(2100, 2, 4, 10, 0, 0, 0, time.UTC)
	state, err := chatStateFromInitial(InitialState{Chats: []InitialChat{
		{ID: "group-a", Title: "Group A", IsGroup: true, ActivityTime: base},
		{ID: "chat-b", Title: "Chat B", ActivityTime: base.Add(-time.Minute)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	model := viewModel{chats: state, options: DefaultOptions(), terminalWidth: 100, terminalHeight: 24}
	message := InitialMessage{ID: "message", SentAt: base, Text: "body", BodyRetained: true, SenderID: "participant@lid", IsGroup: true}
	request, _ := selectedChatLoadRequest(&model)
	applyChatLoad(&model, ChatLoadResult{ChatID: request.ChatID, Revision: request.Revision, Messages: []InitialMessage{message}})
	applyDisplayMetadata(&model, DisplayMetadata{ID: "participant@lid", Name: "Dominique", Quality: 4})
	for index := 0; index < displayMetadataCapacity; index++ {
		applyDisplayMetadata(&model, DisplayMetadata{ID: fmt.Sprintf("eviction-%03d", index), Name: "Synthetic", Quality: 2})
	}
	if model.display.lookup("participant@lid").Name != "" {
		t.Fatal("fixture did not evict participant presentation metadata")
	}
	if !moveChatSelection(&model, 1) || !moveChatSelection(&model, -1) {
		t.Fatal("selection failed")
	}
	request, _ = selectedChatLoadRequest(&model)
	// This models the retained 256-contact registry re-emitting its result.
	applyDisplayMetadata(&model, DisplayMetadata{ID: "participant@lid", Name: "Dominique", Quality: 4})
	if !applyChatLoad(&model, ChatLoadResult{ChatID: request.ChatID, Revision: request.Revision, Messages: []InitialMessage{message}}) {
		t.Fatal("reloaded page rejected")
	}
	assertGroupSenderNames(t, &model, "Dominique")
}

func TestStaleGroupPageCannotApplyNamesToSelectedChat(t *testing.T) {
	base := time.Date(2100, 2, 5, 10, 0, 0, 0, time.UTC)
	state, err := chatStateFromInitial(InitialState{Chats: []InitialChat{
		{ID: "group-a", Title: "Group A", IsGroup: true, ActivityTime: base},
		{ID: "group-b", Title: "Group B", IsGroup: true, ActivityTime: base.Add(-time.Minute)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	model := viewModel{chats: state, options: DefaultOptions(), terminalWidth: 100, terminalHeight: 24}
	stale, _ := selectedChatLoadRequest(&model)
	applyDisplayMetadata(&model, DisplayMetadata{ID: "participant-a@lid", Name: "Participant A", Quality: 4})
	if !moveChatSelection(&model, 1) {
		t.Fatal("selection failed")
	}
	if applyChatLoad(&model, ChatLoadResult{ChatID: stale.ChatID, Revision: stale.Revision, Messages: []InitialMessage{{
		ID: "stale", SentAt: base, Text: "wrong chat", BodyRetained: true, SenderID: "participant-a@lid", IsGroup: true,
	}}}) {
		t.Fatal("stale group page was accepted")
	}
	selected, _ := model.chats.selectedChat()
	if selected.id != "group-b" || selected.messageCount != 0 {
		t.Fatal("stale group page corrupted selected chat")
	}
}

func assertGroupSenderNames(t *testing.T, model *viewModel, want ...string) {
	t.Helper()
	chat, ok := model.chats.selectedChat()
	if !ok || chat.messageCount != len(want) {
		t.Fatalf("selected messages=%d want=%d", chat.messageCount, len(want))
	}
	for index, name := range want {
		if got := senderLabel(chat.messages[index]); got != name {
			t.Fatalf("message %d sender=%q want=%q", index, got, name)
		}
	}
	screen := initializedSimulationScreen(t, 100, 24)
	draw(screen, model)
	screen.Show()
	text := replyScreenText(screen)
	for _, name := range want {
		if !strings.Contains(text, name) {
			t.Fatalf("rendered sender %q missing:\n%s", name, text)
		}
	}
}

func TestRunDisplayUpdatesRedrawOnlyChangedFrames(t *testing.T) {
	screen := newObservedScreen(100, 30)
	updates := make(chan DisplayMetadata)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runWithDependencies(ctx, screen, Input{Options: DefaultOptions(), InitialState: testInitialState(), DisplayUpdates: updates}, "")
	}()
	<-screen.shown
	updates <- DisplayMetadata{ID: "demo", Name: "Saved contact", Quality: 4}
	<-screen.shown
	for _, value := range []DisplayMetadata{{ID: "demo", Name: "Saved contact", Quality: 4}, {ID: "demo", Name: "Lower push", Quality: 2}, {ID: "demo"}, {ID: "not-visible", Name: "No chat", Quality: 4}} {
		updates <- value
	}
	close(updates)
	screen.InjectKey(tcell.KeyRune, 'j', tcell.ModNone)
	<-screen.shown
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := screen.showCount.Load(); got != 3 {
		t.Fatalf("Show calls=%d: want initial, rename, navigation only", got)
	}
}

func TestGroupFromMeOmitsParticipantLabel(t *testing.T) {
	view := liveTestView(t)
	message := messageView{isGroup: true, fromMe: true, senderID: "self", senderName: "Own saved name", text: "outgoing body", time: "10:00"}
	screen := initializedSimulationScreen(t, 40, 10)
	drawMessage(screen, &view, 0, message, 0, 0, 40, 10, false)
	screen.Show()
	if text := replyScreenText(screen); strings.Contains(text, "Own saved name") || !strings.Contains(text, "outgoing body") {
		t.Fatal("from-me message gained redundant sender label")
	}
	if rows := wrappedMessageLines(message, 40, true); rows != 1 {
		t.Fatalf("from-me rows=%d", rows)
	}
}

func TestDisplayNamesAndGroupSendersRender(t *testing.T) {
	for _, group := range []bool{false, true} {
		state, _ := chatStateFromInitial(InitialState{})
		view := viewModel{chats: state, options: DefaultOptions(), terminalWidth: 100, terminalHeight: 24}
		event := liveTestMessage("opaque-chat", "message", 10, 10, false, "Group body é 👋", 2)
		event.IsGroup = group
		if group {
			event.SenderID = "opaque-person"
		}
		if !applyLiveMessage(&view, event) {
			t.Fatal("message rejected")
		}
		screen := initializedSimulationScreen(t, 100, 24)
		draw(screen, &view)
		screen.Show()
		fallback := replyScreenText(screen)
		if !strings.Contains(fallback, "opaque-chat") || (group && !strings.Contains(fallback, "opaque-person")) {
			t.Fatal("opaque fallback missing")
		}
		name := "Café contact 👋"
		quality := uint8(4)
		if group {
			name = "Family 家族 👋"
			quality = 5
		}
		if !applyDisplayMetadata(&view, DisplayMetadata{ID: event.ChatID, Name: name, Quality: quality, IsGroup: group}) {
			t.Fatal("chat rename did not change frame")
		}
		if group && !applyDisplayMetadata(&view, DisplayMetadata{ID: event.SenderID, Name: "Elena é 👋", Quality: 4}) {
			t.Fatal("sender rename did not change frame")
		}
		draw(screen, &view)
		screen.Show()
		text := replyScreenText(screen)
		if !strings.Contains(text, name) || !strings.Contains(text, event.Text) || strings.Contains(text, "opaque-chat") {
			t.Fatalf("name/body missing:\n%s", text)
		}
		if group {
			if !strings.Contains(text, "Elena é 👋") || strings.Contains(text, "opaque-person") {
				t.Fatal("participant name missing")
			}
			foundBold := false
			for y := 0; y < 24; y++ {
				for x := 0; x < 100; x++ {
					r, _, style, _ := screen.GetContent(x, y)
					_, _, attr := style.Decompose()
					if r == 'E' && attr&tcell.AttrBold != 0 {
						foundBold = true
					}
				}
			}
			if !foundBold {
				t.Fatal("sender label not distinct")
			}
		} else if strings.Count(text, name) != 2 {
			t.Fatal("direct chat name repeated in message body")
		}
		// Names learned before the message produce the same presentation.
		pre, _ := chatStateFromInitial(InitialState{})
		other := viewModel{chats: pre, options: DefaultOptions(), terminalWidth: 100, terminalHeight: 24}
		applyDisplayMetadata(&other, DisplayMetadata{ID: event.ChatID, Name: name, Quality: quality, IsGroup: group})
		if group {
			applyDisplayMetadata(&other, DisplayMetadata{ID: event.SenderID, Name: "Elena é 👋", Quality: 4})
		}
		applyLiveMessage(&other, event)
		draw(screen, &other)
		screen.Show()
		if replyScreenText(screen) != text {
			t.Fatal("metadata/message arrival order changed presentation")
		}
	}
}

func TestDisplayUpdatePreservesInteractionAndIdentity(t *testing.T) {
	view := liveTestView(t)
	view.mode = modeCompose
	view.composer.insertText("draft 👋")
	view.composer.cursor = 2
	view.replyTarget = replyTarget{valid: true, id: "original"}
	view.replySelect = replySelectionState{valid: true, index: 0, fromCompose: true}
	view.chatView.scrollOffset = 2
	view.emojiPicker.prepareOpen()
	view.settingsOpen = true
	before, chatBefore := view, *view.chats
	if !applyDisplayMetadata(&view, DisplayMetadata{ID: "a", Name: "Jean 👋", Quality: 4}) {
		t.Fatal("no visible update")
	}
	if view.chats.chats[0].id != "a" || view.chats.selectedIndex() != chatBefore.selected || view.chats.chats[0].unreadCount != chatBefore.chats[0].unreadCount || view.chats.chats[0].activityTime != chatBefore.chats[0].activityTime || view.chats.chats[0].messages != chatBefore.chats[0].messages {
		t.Fatal("metadata changed chat/message state")
	}
	before.chats = view.chats
	before.display = view.display
	if !reflect.DeepEqual(before, view) {
		t.Fatal("metadata reset draft/cursor/reply/viewport/popups")
	}
	for _, value := range []DisplayMetadata{{ID: "a", Name: "Jean 👋", Quality: 4}, {ID: "a", Name: "Jean's phone", Quality: 2}, {ID: "a", Quality: 0}} {
		if applyDisplayMetadata(&view, value) || view.chats.chats[0].title != "Jean 👋" {
			t.Fatal("duplicate/lower/empty update redrew or downgraded")
		}
	}
	if !applyDisplayMetadata(&view, DisplayMetadata{ID: "b", Name: "Jean 👋", Quality: 4}) || view.chats.chatCount != chatBefore.chatCount || view.chats.chats[1].id != "b" {
		t.Fatal("same display name merged chats")
	}
	if applyDisplayMetadata(&view, DisplayMetadata{ID: "unknown", Name: "Not a chat", Quality: 4}) || view.chats.chatCount != chatBefore.chatCount {
		t.Fatal("metadata inserted a chat")
	}
}

func TestLongGroupNamesAndSenderLabelsAreCellSafe(t *testing.T) {
	state, _ := chatStateFromInitial(InitialState{})
	view := viewModel{chats: state, options: DefaultOptions()}
	event := liveTestMessage("group", "message", 1, 1, false, "body", 4)
	event.SenderID = "sender"
	event.IsGroup = true
	applyLiveMessage(&view, event)
	name := strings.Repeat("Café é 🙂 家族 ", 10)
	applyDisplayMetadata(&view, DisplayMetadata{ID: "group", Name: name, Quality: 5, IsGroup: true})
	applyDisplayMetadata(&view, DisplayMetadata{ID: "sender", Name: name, Quality: 4})
	for _, size := range [][2]int{{100, 24}, {70, 18}, {35, 12}, {12, 3}, {100, 24}} {
		screen := initializedSimulationScreen(t, size[0], size[1])
		draw(screen, &view)
		screen.Show()
		if view.chats.chats[0].messages[0].text != "body" || view.chats.chats[0].id != "group" {
			t.Fatal("resize changed data")
		}
		if size[0] >= 70 && !strings.Contains(replyScreenText(screen), "…") {
			t.Fatal("long names not truncated")
		}
	}
	chat, _ := view.chats.selectedChat()
	message := chat.messages[0]
	screen := initializedSimulationScreen(t, 30, 8)
	rows := drawMessage(screen, &view, 0, message, 0, 0, 24, 8, false)
	if rows != wrappedMessageLines(message, 24, true) {
		t.Fatal("sender row missing from viewport measurement")
	}
}
