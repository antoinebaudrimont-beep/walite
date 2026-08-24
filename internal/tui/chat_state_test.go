package tui

import (
	"reflect"
	"testing"
)

func TestViewOnlyOperationsDoNotModifyChatState(t *testing.T) {
	model := defaultDemoView()
	want := *model.chats
	screen := initializedSimulationScreen(t, 120, 40)

	model.mode = modeCompose
	if !model.composer.insertText("state stays outside the renderer") {
		t.Fatal("draft setup failed")
	}
	model.emojiPicker.prepareOpen()
	for _, size := range [][2]int{{120, 40}, {60, 20}, {30, 6}, {120, 40}} {
		screen.SetSize(size[0], size[1])
		clampView(&model, size[0], size[1])
		draw(screen, &model)
		if !reflect.DeepEqual(*model.chats, want) {
			t.Fatalf("%dx%d view operation modified chat state", size[0], size[1])
		}
	}
	if model.terminalWidth != 120 || model.terminalHeight != 40 {
		t.Fatalf("terminal size=%dx%d", model.terminalWidth, model.terminalHeight)
	}
}

func TestChatStateMessageIDsSurviveActivityReorder(t *testing.T) {
	state := newDemoChatState()
	chatIndex := 2
	targetIndex := state.chats[chatIndex].messageCount - 1
	targetID := state.chats[chatIndex].messages[targetIndex].id
	targetText := state.chats[chatIndex].messages[targetIndex].text
	unread := state.chats[chatIndex].unreadCount

	if !state.recordUnreadActivity(chatIndex, 4) {
		t.Fatal("unread activity rejected")
	}
	if state.chats[0].title != "Family Demo" || state.chats[0].unreadCount != unread+4 {
		t.Fatalf("top chat=%q unread=%d", state.chats[0].title, state.chats[0].unreadCount)
	}
	message, found := state.findMessageByID(0, targetID)
	if !found || message.text != targetText {
		t.Fatalf("message id %d after reorder=%+v found=%t", targetID, message, found)
	}
}

func TestReplyTargetRemainsValidAfterChatStateReorder(t *testing.T) {
	model := defaultDemoView()
	model.chats.selected = 3
	chat := &model.chats.chats[3]
	targetID := chat.messages[chat.messageCount-1].id
	model.replyTarget = replyTarget{valid: true, id: targetID}

	if !model.chats.promoteChatActivity(3) {
		t.Fatal("activity promotion failed")
	}
	if model.chats.selected != 0 || model.replyTarget.id != targetID {
		t.Fatalf("selected=%d reply target=%+v", model.chats.selected, model.replyTarget)
	}
	if _, found := model.chats.findMessageByID(0, model.replyTarget.id); !found {
		t.Fatalf("reply target %d missing after reorder", model.replyTarget.id)
	}
}
