package tui

import "testing"

func TestSyntheticChatsStartInNewestActivityOrder(t *testing.T) {
	model := defaultDemoView()
	want := [maxChats]string{"Demo Chat", "Project Room", "Family Demo", "Test Contact"}
	for index := 0; index < model.chatCount; index++ {
		if model.chats[index].title != want[index] {
			t.Fatalf("chat %d=%q want=%q", index, model.chats[index].title, want[index])
		}
		if index > 0 && model.chats[index-1].activity <= model.chats[index].activity {
			t.Fatalf("activity order at %d: %d <= %d", index, model.chats[index-1].activity, model.chats[index].activity)
		}
	}
}

func TestLocalSendMovesSelectedChatToTopWithOwnedState(t *testing.T) {
	model := defaultDemoView()
	model.selectedChat = 3
	selectedTitle := model.chats[3].title
	selectedUnread := model.chats[3].unreadCount
	otherUnread := unreadByTitle(model)
	model.mode = modeCompose
	if !model.composer.insertText("newest local activity") || !submitLocalMessage(&model) {
		t.Fatal("local send failed")
	}
	if model.selectedChat != 0 || model.chats[0].title != selectedTitle {
		t.Fatalf("selected=%d top=%q want=%q", model.selectedChat, model.chats[0].title, selectedTitle)
	}
	top := &model.chats[0]
	if top.unreadCount != selectedUnread || top.messages[top.messageCount-1].text != "newest local activity" {
		t.Fatalf("top unread=%d message=%q", top.unreadCount, top.messages[top.messageCount-1].text)
	}
	for title, unread := range otherUnread {
		if got, ok := chatByTitle(&model, title); !ok || got.unreadCount != unread {
			t.Fatalf("chat %q unread=%d found=%t want=%d", title, got.unreadCount, ok, unread)
		}
	}
}

func TestReplySendMovesChatToTopAndKeepsReplyIdentity(t *testing.T) {
	model := defaultDemoView()
	model.selectedChat = 2
	chat := &model.chats[model.selectedChat]
	targetID := chat.messages[chat.messageCount-1].id
	model.replySelect = replySelectionState{valid: true, index: chat.messageCount - 1}
	if !chooseReplyTarget(&model) {
		t.Fatal("reply target selection failed")
	}
	if !model.composer.insertText("activity reply") || !submitLocalMessage(&model) {
		t.Fatal("reply send failed")
	}
	if model.selectedChat != 0 || model.chats[0].title != "Family Demo" {
		t.Fatalf("selected=%d top=%q", model.selectedChat, model.chats[0].title)
	}
	reply := model.chats[0].messages[model.chats[0].messageCount-1]
	if !reply.hasReply || reply.replyToID != targetID {
		t.Fatalf("reply=%+v target=%d", reply, targetID)
	}
	if original, ok := findMessageByID(&model.chats[0], targetID); !ok || original.id != targetID {
		t.Fatalf("reply original lost after reorder: %+v found=%t", original, ok)
	}
}

func TestSyntheticIncomingMovesChatToTopAndPreservesSelectedChat(t *testing.T) {
	model := defaultDemoView()
	selectedTitle := model.chats[model.selectedChat].title
	beforeUnread := model.chats[2].unreadCount
	beforeCount := model.chats[2].messageCount
	if !recordSyntheticIncoming(&model, 2, "synthetic incoming activity") {
		t.Fatal("incoming activity rejected")
	}
	if model.chats[0].title != "Family Demo" {
		t.Fatalf("top=%q", model.chats[0].title)
	}
	if model.chats[model.selectedChat].title != selectedTitle {
		t.Fatalf("selected chat changed to %q", model.chats[model.selectedChat].title)
	}
	family := &model.chats[0]
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
	if !recordUnreadActivity(&model, 3, 5) {
		t.Fatal("unread activity rejected")
	}
	if model.chats[0].title != "Test Contact" {
		t.Fatalf("top=%q", model.chats[0].title)
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

func unreadByTitle(model viewModel) map[string]uint16 {
	result := make(map[string]uint16, model.chatCount)
	for index := 0; index < model.chatCount; index++ {
		result[model.chats[index].title] = model.chats[index].unreadCount
	}
	return result
}

func chatByTitle(model *viewModel, title string) (*chatView, bool) {
	for index := 0; index < model.chatCount; index++ {
		if model.chats[index].title == title {
			return &model.chats[index], true
		}
	}
	return nil, false
}
