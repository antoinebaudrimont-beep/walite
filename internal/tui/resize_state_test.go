package tui

import "testing"

func TestWideNarrowShortWidePreservesInteractiveState(t *testing.T) {
	model := defaultDemoView()
	model.chats.selected = 2
	model.replySelect = replySelectionState{valid: true, index: 5}
	model.replyTarget = replyTarget{valid: true, id: model.chats.chats[2].messages[5].id}
	model.mode = modeCompose
	if !model.composer.insertText("resize ❤️ draft") {
		t.Fatal("draft setup failed")
	}
	model.emojiPicker.open = true
	model.emojiPicker.category = 7
	model.emojiPicker.categoryCursor = 3

	wantChat := model.chats.chats[2].title
	wantMessageID := model.chats.chats[2].messages[5].id
	wantTarget := model.replyTarget
	for _, size := range [][2]int{{120, 40}, {60, 20}, {30, 6}, {120, 40}} {
		clampView(&model, size[0], size[1])
		if model.chats.chats[model.chats.selected].title != wantChat {
			t.Fatalf("%dx%d selected chat=%q", size[0], size[1], model.chats.chats[model.chats.selected].title)
		}
		if !model.replySelect.valid || model.chats.chats[model.chats.selected].messages[model.replySelect.index].id != wantMessageID {
			t.Fatalf("%dx%d selected message=%+v", size[0], size[1], model.replySelect)
		}
		if model.composer.text() != "resize ❤️ draft" || !model.emojiPicker.open || model.emojiPicker.category != 7 || model.replyTarget != wantTarget {
			t.Fatalf("%dx%d draft=%q picker=%+v target=%+v", size[0], size[1], model.composer.text(), model.emojiPicker, model.replyTarget)
		}
	}
}
