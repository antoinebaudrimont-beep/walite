package tui

import (
	"testing"
	"time"
)

func TestLocalReadAdmissionPreservesInteractionState(t *testing.T) {
	state, err := chatStateFromInitial(InitialState{Chats: []InitialChat{{
		ID: "chat", Title: "Chat", Messages: []InitialMessage{{ID: "target", Text: "quoted", BodyRetained: true}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	activity := time.Date(2100, 9, 6, 10, 0, 0, 0, time.UTC)
	model := viewModel{chats: state, mode: modeCompose, options: DefaultOptions(), localReadRequest: LocalReadRequest{ChatID: "chat", ActivityTime: activity}}
	if !model.composer.insertText("draft Café 👋") {
		t.Fatal("draft setup failed")
	}
	model.composer.moveLeft()
	model.replyTarget = replyTarget{valid: true, id: "target"}
	model.replySelect = replySelectionState{valid: true, index: 0, fromCompose: true}
	model.chatView.scrollOffset = 2
	model.emojiPicker.open = true
	model.settingsOpen = true
	wantComposer, wantTarget, wantSelection := model.composer, model.replyTarget, model.replySelect
	wantView, wantPicker, wantSettings := model.chatView, model.emojiPicker, model.settingsOpen
	calls := 0
	requestPendingLocalRead(&model, func(request LocalReadRequest) bool {
		calls++
		return request.ChatID == "chat" && request.ActivityTime.Equal(activity)
	})
	if calls != 1 || model.localReadRequest != (LocalReadRequest{}) || model.mode != modeCompose || model.composer != wantComposer ||
		model.replyTarget != wantTarget || model.replySelect != wantSelection || model.chatView != wantView ||
		model.emojiPicker != wantPicker || model.settingsOpen != wantSettings {
		t.Fatalf("local-read admission changed interaction: calls=%d model=%+v", calls, model)
	}
}

func TestRejectedLocalReadAdmissionRemainsPending(t *testing.T) {
	model := viewModel{localReadRequest: LocalReadRequest{ChatID: "chat"}}
	requestPendingLocalRead(&model, func(LocalReadRequest) bool { return false })
	if model.localReadRequest.ChatID != "chat" {
		t.Fatal("rejected local-read admission was discarded")
	}
}
