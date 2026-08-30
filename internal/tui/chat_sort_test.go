package tui

import "testing"

func TestSyntheticChatsStartInNewestActivityOrder(t *testing.T) {
	model := defaultDemoView()
	want := [maxChats]string{"Demo Chat", "Project Room", "Family Demo", "Test Contact"}
	for index := 0; index < model.chats.chatCount; index++ {
		if model.chats.chats[index].title != want[index] {
			t.Fatalf("chat %d=%q want=%q", index, model.chats.chats[index].title, want[index])
		}
		if index > 0 && !model.chats.chats[index-1].activityTime.After(model.chats.chats[index].activityTime) {
			t.Fatalf("activity order at %d: %s <= %s", index, model.chats.chats[index-1].activityTime, model.chats.chats[index].activityTime)
		}
	}
}

func TestLocalSendMovesSelectedChatToTopWithOwnedState(t *testing.T) {
	model := defaultDemoView()
	installCommittedTestSender(&model)
	model.chats.selected = 3
	selectedTitle := model.chats.chats[3].title
	selectedUnread := model.chats.chats[3].unreadCount
	otherUnread := unreadByTitle(model)
	model.mode = modeCompose
	if !model.composer.insertText("newest local activity") || !submitOutgoingMessage(&model) {
		t.Fatal("local send failed")
	}
	if model.chats.selected != 0 || model.chats.chats[0].title != selectedTitle {
		t.Fatalf("selected=%d top=%q want=%q", model.chats.selected, model.chats.chats[0].title, selectedTitle)
	}
	top := &model.chats.chats[0]
	if top.unreadCount != selectedUnread || top.messages[top.messageCount-1].text != "newest local activity" {
		t.Fatalf("top unread=%d message=%q", top.unreadCount, top.messages[top.messageCount-1].text)
	}
	for title, unread := range otherUnread {
		if got, ok := chatByTitle(&model, title); !ok || got.unreadCount != unread {
			t.Fatalf("chat %q unread=%d found=%t want=%d", title, got.unreadCount, ok, unread)
		}
	}
}

func TestDeferredReplySendPreservesState(t *testing.T) {
	model := defaultDemoView()
	model.chats.selected = 2
	chat := &model.chats.chats[model.chats.selected]
	targetID := chat.messages[chat.messageCount-1].id
	model.replySelect = replySelectionState{valid: true, index: chat.messageCount - 1}
	if !chooseReplyTarget(&model) {
		t.Fatal("reply target selection failed")
	}
	wantDraft := "activity reply"
	wantCursor := 3
	var request SendRequest
	model.send = func(got SendRequest) error {
		request = got
		return errTestSendRejected
	}
	if !model.composer.insertText(wantDraft) {
		t.Fatal("draft setup failed")
	}
	model.composer.cursor = wantCursor
	if submitOutgoingMessage(&model) {
		t.Fatal("unsupported reply send accepted")
	}
	if request.ChatID != chat.id || request.Text != wantDraft || request.ReplyToID != string(targetID) {
		t.Fatalf("request=%+v", request)
	}
	if model.chats.selected != 2 || model.chats.chats[2].title != "Family Demo" ||
		model.composer.text() != wantDraft || model.composer.cursor != wantCursor || !model.replyTarget.valid {
		t.Fatalf("selected=%d draft=%q cursor=%d target=%+v", model.chats.selected, model.composer.text(), model.composer.cursor, model.replyTarget)
	}
}

func TestSyntheticIncomingMovesChatToTopAndPreservesSelectedChat(t *testing.T) {
	model := defaultDemoView()
	selectedTitle := model.chats.chats[model.chats.selected].title
	beforeUnread := model.chats.chats[2].unreadCount
	beforeCount := model.chats.chats[2].messageCount
	if !recordIncomingMessage(&model, 2, "synthetic incoming activity") {
		t.Fatal("incoming activity rejected")
	}
	if model.chats.chats[0].title != "Family Demo" {
		t.Fatalf("top=%q", model.chats.chats[0].title)
	}
	if model.chats.chats[model.chats.selected].title != selectedTitle {
		t.Fatalf("selected chat changed to %q", model.chats.chats[model.chats.selected].title)
	}
	family := &model.chats.chats[0]
	if family.unreadCount != beforeUnread+1 || family.messageCount != beforeCount+1 ||
		family.messages[family.messageCount-1].text != "synthetic incoming activity" {
		t.Fatalf("family unread=%d count=%d latest=%q", family.unreadCount, family.messageCount,
			family.messages[family.messageCount-1].text)
	}
}

func TestUnreadActivityMovesChatAndKeepsAllUnreadCountsAttached(t *testing.T) {
	model := defaultDemoView()
	before := unreadByTitle(model)
	before["Test Contact"] += 5
	if !model.chats.recordUnreadActivity(3, 5) {
		t.Fatal("unread activity rejected")
	}
	if model.chats.chats[0].title != "Test Contact" {
		t.Fatalf("top=%q", model.chats.chats[0].title)
	}
	for title, unread := range before {
		chat, ok := chatByTitle(&model, title)
		if !ok {
			t.Fatalf("chat %q missing after reorder", title)
		}
		if chat.unreadCount != unread {
			t.Fatalf("chat %q unread=%d want=%d", title, chat.unreadCount, unread)
		}
	}
}

func unreadByTitle(model viewModel) map[string]uint32 {
	result := make(map[string]uint32, model.chats.chatCount)
	for index := 0; index < model.chats.chatCount; index++ {
		result[model.chats.chats[index].title] = model.chats.chats[index].unreadCount
	}
	return result
}

func chatByTitle(model *viewModel, title string) (*chatView, bool) {
	for index := 0; index < model.chats.chatCount; index++ {
		if model.chats.chats[index].title == title {
			return &model.chats.chats[index], true
		}
	}
	return nil, false
}
