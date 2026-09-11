package tui

import "testing"

func TestReplySendCapturesRetainedTargetAndClearsOnlyOnSuccess(t *testing.T) {
	for _, fromMe := range []bool{false, true} {
		model := defaultDemoView()
		model.mode = modeCompose
		model.asyncSend = true
		target := &model.chats.chats[0].messages[0]
		target.text = "original é 日本語 🐧"
		target.bodyRetained = true
		target.fromMe = fromMe
		model.replyTarget = replyTarget{valid: true, id: target.id}
		model.composer.insertText("reply 👋")
		model.composer.cursor = 3
		model.chatView.scrollOffset = 2
		beforeDraft, beforeTarget, beforeChats := model.composer, model.replyTarget, *model.chats
		var got SendRequest
		model.send = func(request SendRequest) error { got = request; return nil }
		if !submitOutgoingMessage(&model) || !model.sendPending {
			t.Fatal("reply not admitted")
		}
		if got.ReplyToID != string(target.id) || got.ReplyToText != target.text || got.ReplyToFromMe != fromMe {
			t.Fatalf("request=%+v", got)
		}
		if !applySendResult(&model, SendResult{Failed: true, Uncertain: true}) || model.composer != beforeDraft || model.replyTarget != beforeTarget || model.chatView.scrollOffset != 2 || !chatStatesEqual(*model.chats, beforeChats) {
			t.Fatal("remote failure changed protected draft/target/viewport/data")
		}
		// A separate successful completion exercises the existing success path.
		model.sendUncertain = false
		model.sendPending = true
		if !applySendResult(&model, SendResult{}) || model.replyTarget.valid || model.composer.length != 0 || model.chatView.scrollOffset != 0 || !chatStatesEqual(*model.chats, beforeChats) {
			t.Fatal("success did not clear quote or appended optimistic message")
		}
	}
}
