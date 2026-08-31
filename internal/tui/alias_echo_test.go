package tui

import (
	"fmt"
	"reflect"
	"testing"
)

// PN/LID equivalence is resolved before LiveEvents (WA integration tests).
// The TUI sees only its original stable ID and needs no WhatsApp alias rules.
func TestNormalizedAliasEchoPreservesSelectedChatAndAllTransientState(t *testing.T) {
	for _, id := range []string{"12345@s.whatsapp.net", "987654@lid"} {
		t.Run(id, func(t *testing.T) {
			initial := InitialState{}
			for i := 0; i < ChatWorkingSetCapacity; i++ {
				chatID := fmt.Sprintf("unrelated-%02d", i)
				if i == 0 {
					chatID = id
				}
				initial.Chats = append(initial.Chats, InitialChat{ID: chatID, Title: chatID, ActivityTime: liveTestBase, Messages: []InitialMessage{liveInitialMessage("X", 0, true, "é 日本語 🐧")}})
			}
			for i := 1; i < MaxInitialMessagesPerChat; i++ {
				initial.Chats[0].Messages = append(initial.Chats[0].Messages, liveInitialMessage(fmt.Sprintf("history-%d", i), i, false, "older history"))
			}
			state, err := chatStateFromInitial(initial)
			if err != nil {
				t.Fatal(err)
			}
			model := viewModel{chats: state, options: DefaultOptions(), mode: modeCompose}
			model.composer.insertText("preserved draft 🐧")
			model.composer.cursor = 4
			model.replyTarget = replyTarget{valid: true, id: "X"}
			model.chatView.scrollOffset = 1
			model.emojiPicker.prepareOpen()
			model.settingsOpen = true
			beforeModel, beforeChats := model, *state
			echo := liveTestMessage(id, "X", 0, 0, true, "é 日本語 🐧", 0)
			if applyLiveMessage(&model, echo) {
				t.Fatal("duplicate caused redraw")
			}
			if !reflect.DeepEqual(model, beforeModel) || *state != beforeChats {
				t.Fatal("echo changed selection/draft/cursor/viewport/reply/popups/unread/order")
			}
			selected, _ := state.selectedChat()
			if selected.id != id || state.count() != ChatWorkingSetCapacity {
				t.Fatal("alias replaced selection or created another chat")
			}
		})
	}
}
