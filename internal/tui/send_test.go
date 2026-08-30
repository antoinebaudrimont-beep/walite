package tui

import (
	"errors"
	"testing"
)

var errTestSendRejected = errors.New("test send rejected")

func TestAcceptedSendUsesStableRequestAndWaitsForLiveEvent(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	chat, _ := model.chats.selectedChat()
	before := chat.messageCount
	model.chatView.scrollOffset = 2
	var calls int
	var got SendRequest
	model.send = func(request SendRequest) error {
		calls++
		got = request
		return nil
	}
	const text = "Café 東京 ❤️ 👍🏽 👨‍👩‍👧‍👦"
	if !model.composer.insertText(text) || !submitOutgoingMessage(&model) {
		t.Fatal("accepted send rejected")
	}
	chat, _ = model.chats.selectedChat()
	if calls != 1 || got != (SendRequest{ChatID: chat.id, Text: text}) {
		t.Fatalf("calls=%d request=%+v chat=%q", calls, got, chat.id)
	}
	if chat.messageCount != before {
		t.Fatalf("message count=%d before live event, want=%d", chat.messageCount, before)
	}
	if model.composer.length != 0 || model.replyTarget.valid || model.chatView.scrollOffset != 0 || model.mode != modeCompose {
		t.Fatalf("accepted state draft=%q target=%+v offset=%d mode=%d", model.composer.text(), model.replyTarget, model.chatView.scrollOffset, model.mode)
	}
	if submitOutgoingMessage(&model) || calls != 1 {
		t.Fatalf("empty key repeat submitted again: calls=%d", calls)
	}
}

func TestRejectedSendPreservesDraftCursorReplyAndChatState(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	model.replyTarget = replyTarget{valid: true, id: model.chats.chats[0].messages[0].id}
	model.chatView.scrollOffset = 3
	const text = "preserve this draft"
	if !model.composer.insertText(text) {
		t.Fatal("draft setup failed")
	}
	model.composer.cursor = 5
	beforeChat := *model.chats
	beforeTarget := model.replyTarget
	var got SendRequest
	model.send = func(request SendRequest) error {
		got = request
		return errTestSendRejected
	}
	if submitOutgoingMessage(&model) {
		t.Fatal("rejected send reported a redraw")
	}
	if got.ChatID != "demo" || got.Text != text || got.ReplyToID != string(beforeTarget.id) {
		t.Fatalf("request=%+v", got)
	}
	if model.composer.text() != text || model.composer.cursor != 5 || model.replyTarget != beforeTarget ||
		model.chatView.scrollOffset != 3 || model.mode != modeCompose || *model.chats != beforeChat {
		t.Fatalf("rejection mutated state: draft=%q cursor=%d target=%+v offset=%d mode=%d", model.composer.text(), model.composer.cursor, model.replyTarget, model.chatView.scrollOffset, model.mode)
	}
}

func TestEmptyAndNoChatSendDoNotInvokeApplication(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	calls := 0
	model.send = func(SendRequest) error { calls++; return nil }
	if submitOutgoingMessage(&model) || calls != 0 {
		t.Fatalf("empty send changed state or called application: calls=%d", calls)
	}
	model.chats = &chatState{}
	if !model.composer.insertText("no selected chat") {
		t.Fatal("draft setup failed")
	}
	if submitOutgoingMessage(&model) || calls != 0 {
		t.Fatalf("no-chat send changed state or called application: calls=%d", calls)
	}
}
