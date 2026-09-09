package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestAsyncSendProtectsDraftCursorReplyAndViewport(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	model.asyncSend = true
	model.composer.insertText("draft é 日本語 🐧")
	model.composer.cursor = 5
	model.replyTarget = replyTarget{valid: true, id: model.chats.chats[0].messages[0].id}
	model.chatView.scrollOffset = 2
	calls := 0
	model.send = func(SendRequest) error { calls++; return nil }
	beforeDraft, beforeReply, beforeChat := model.composer, model.replyTarget, *model.chats
	if !submitOutgoingMessage(&model) || !model.sendPending {
		t.Fatal("not pending")
	}
	for _, key := range []tcell.Key{tcell.KeyEnter, tcell.KeyEscape, tcell.KeyBackspace, tcell.KeyLeft, tcell.KeyRight, tcell.KeyCtrlE, tcell.KeyCtrlR, tcell.KeyRune} {
		handleKey(&model, tcell.NewEventKey(key, 'x', tcell.ModNone), 80, 24)
	}
	if calls != 1 || model.composer != beforeDraft || model.replyTarget != beforeReply || model.chatView.scrollOffset != 2 || !chatStatesEqual(*model.chats, beforeChat) {
		t.Fatal("pending send modified protected state or retried")
	}
	if !applySendResult(&model, SendResult{Failed: true}) || model.sendPending || model.sendUncertain || model.composer != beforeDraft || model.replyTarget != beforeReply || model.chatView.scrollOffset != 2 || !chatStatesEqual(*model.chats, beforeChat) {
		t.Fatal("failed send lost state or appended message")
	}
}

func TestAsyncSendSuccessWaitsForLiveMessageAndEchoIsIdempotent(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	model.asyncSend = true
	model.composer.insertText("outgoing 🐧")
	model.chatView.scrollOffset = 2
	model.send = func(SendRequest) error { return nil }
	before := *model.chats
	if !submitOutgoingMessage(&model) || !applySendResult(&model, SendResult{}) {
		t.Fatal("send failed")
	}
	if !chatStatesEqual(*model.chats, before) || model.composer.length != 0 || model.chatView.scrollOffset != 0 {
		t.Fatal("success appended fake message or failed to clear draft")
	}
	event := liveTestMessage("demo", "remote-id", 60, 60, true, "outgoing 🐧", 0)
	if !applyLiveMessage(&model, event) {
		t.Fatal("committed message rejected")
	}
	if applyLiveMessage(&model, event) {
		t.Fatal("identical echo changed presentation")
	}
	chat, _ := model.chats.selectedChat()
	count := 0
	for _, message := range chat.messages[:chat.messageCount] {
		if message.id == "remote-id" {
			count++
			if !message.fromMe || message.text != event.Text {
				t.Fatal("message data changed")
			}
		}
	}
	if count != 1 {
		t.Fatalf("copies=%d", count)
	}
}

func TestAsyncUncertainSendCannotRepeatAndKeepsTextVisible(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	model.asyncSend = true
	model.composer.insertText("retained draft")
	calls := 0
	model.send = func(SendRequest) error { calls++; return nil }
	submitOutgoingMessage(&model)
	applySendResult(&model, SendResult{Failed: true, Uncertain: true})
	screen := initializedSimulationScreen(t, 80, 24)
	draw(screen, &model)
	screen.Show()
	text := screenText(screen)
	if !strings.Contains(text, "retained draft") || !strings.Contains(text, "Delivery unknown") {
		t.Fatal(text)
	}
	for i := 0; i < 3; i++ {
		handleKey(&model, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), 80, 24)
	}
	if calls != 1 || model.composer.text() != "retained draft" {
		t.Fatal("unknown delivery was retried or lost")
	}
	handleKey(&model, tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), 80, 24)
	if model.composer.length != 0 || model.sendUncertain || calls != 1 {
		t.Fatal("explicit discard retried transport")
	}
}

func TestAsyncPreflightRejectionKeepsStateAndShowsStatus(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	model.asyncSend = true
	model.composer.insertText("keep draft")
	model.composer.cursor = 3
	model.replyTarget = replyTarget{valid: true, id: "target"}
	model.chatView.scrollOffset = 2
	beforeDraft, beforeReply, beforeChats := model.composer, model.replyTarget, *model.chats
	model.send = func(SendRequest) error { return errTestSendRejected }
	if !submitOutgoingMessage(&model) || model.sendPending || model.composer != beforeDraft || model.replyTarget != beforeReply || !chatStatesEqual(*model.chats, beforeChats) || model.chatView.scrollOffset != 2 || model.sendStatus == "" {
		t.Fatal("preflight changed state or hid rejection")
	}
}
