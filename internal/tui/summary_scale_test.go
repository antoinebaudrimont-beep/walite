package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/gdamore/tcell/v2"
)

func TestChatSummaryCollectionScalesWithoutPerChatMessageSlots(t *testing.T) {
	for _, count := range []int{17, 100, ChatWorkingSetCapacity} {
		t.Run(fmt.Sprintf("chats-%d", count), func(t *testing.T) {
			initial := InitialState{Chats: make([]InitialChat, count)}
			for index := range initial.Chats {
				initial.Chats[index] = InitialChat{
					ID: fmt.Sprintf("chat-%05d", index), Title: fmt.Sprintf("Chat %05d", index),
					ActivityTime: time.Unix(int64(count-index), 0).UTC(),
				}
			}
			state, err := chatStateFromInitial(initial)
			if err != nil || state.count() != count {
				t.Fatalf("count=%d err=%v", state.count(), err)
			}
			for index := 0; index < state.count(); index++ {
				if state.chats[index].messages != nil || state.chats[index].messageCount != 0 {
					t.Fatalf("metadata-only chat %d allocated message slots", index)
				}
			}
		})
	}
}

func BenchmarkCachedSummaryFirstFrame10000(b *testing.B) {
	initial := InitialState{Chats: make([]InitialChat, ChatWorkingSetCapacity)}
	for index := range initial.Chats {
		initial.Chats[index] = InitialChat{ID: fmt.Sprintf("chat-%05d", index), Title: fmt.Sprintf("Chat %05d", index), ActivityTime: time.Unix(int64(ChatWorkingSetCapacity-index), 0).UTC()}
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.ReportMetric(float64(unsafe.Sizeof(chatState{})), "bounded-state-B")
	for range b.N {
		state, err := chatStateFromInitial(initial)
		if err != nil {
			b.Fatal(err)
		}
		screen := tcell.NewSimulationScreen("UTF-8")
		if err := screen.Init(); err != nil {
			b.Fatal(err)
		}
		screen.SetSize(100, 30)
		model := viewModel{chats: state, options: DefaultOptions()}
		draw(screen, &model)
		screen.Show()
		screen.Fini()
	}
}

func TestHundredChatNavigationAndViewportFollowSelection(t *testing.T) {
	initial := InitialState{Chats: make([]InitialChat, 100)}
	for index := range initial.Chats {
		initial.Chats[index] = InitialChat{ID: fmt.Sprintf("chat-%03d", index), Title: fmt.Sprintf("Chat %03d", index), ActivityTime: time.Unix(int64(100-index), 0).UTC()}
	}
	state, err := chatStateFromInitial(initial)
	if err != nil {
		t.Fatal(err)
	}
	model := viewModel{chats: state, options: DefaultOptions()}
	for range 99 {
		if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), 100, 30); !changed {
			t.Fatal("navigation stopped before final summary")
		}
	}
	if model.chats.selectedIndex() != 99 {
		t.Fatalf("selected=%d", model.chats.selectedIndex())
	}
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(100, 30)
	draw(screen, &model)
	screen.Show()
	if rendered := simulationText(screen); !strings.Contains(rendered, "> Chat 099") {
		t.Fatalf("selected chat not visible in followed viewport:\n%s", rendered)
	}
}

func TestSelectedChatLoadRejectsStaleResultAndMergesCommittedMessage(t *testing.T) {
	base := time.Date(2100, 1, 1, 10, 0, 0, 0, time.UTC)
	state, err := chatStateFromInitial(InitialState{Chats: []InitialChat{
		{ID: "a", Title: "A", ActivityTime: base},
		{ID: "b", Title: "B", ActivityTime: base.Add(-time.Minute)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	model := viewModel{chats: state, options: DefaultOptions(), terminalWidth: 100, terminalHeight: 30}
	requestA, _ := selectedChatLoadRequest(&model)
	if !moveChatSelection(&model, 1) {
		t.Fatal("selection did not move")
	}
	if applyChatLoad(&model, ChatLoadResult{ChatID: requestA.ChatID, Revision: requestA.Revision, Messages: []InitialMessage{{ID: "stale", SentAt: base, Text: "wrong", BodyRetained: true}}}) {
		t.Fatal("stale A result replaced selected B")
	}
	live := LiveMessage{ChatID: "b", MessageID: "live", SentAt: base.Add(time.Minute), Text: "committed 👋", BodyRetained: true, ActivityTime: base.Add(time.Minute)}
	if !applyLiveMessage(&model, live) {
		t.Fatal("live message rejected")
	}
	requestB, _ := selectedChatLoadRequest(&model)
	away, back := 1, -1
	if model.chats.selectedIndex() != 0 {
		away, back = -1, 1
	}
	if !moveChatSelection(&model, away) || !moveChatSelection(&model, back) {
		t.Fatal("reselection setup failed")
	}
	if applyChatLoad(&model, ChatLoadResult{ChatID: "b", Revision: requestB.Revision}) {
		t.Fatal("old B result survived a newer B selection generation")
	}
	requestB, _ = selectedChatLoadRequest(&model)
	result := ChatLoadResult{ChatID: "b", Revision: requestB.Revision, Messages: []InitialMessage{{ID: "cached", SentAt: base, Text: "cached Café", BodyRetained: true}}}
	if !applyChatLoad(&model, result) {
		t.Fatal("current cache page rejected")
	}
	chat, _ := model.chats.selectedChat()
	if chat.messageCount != 2 || chat.messages[0].id != "cached" || chat.messages[1].id != "live" || chat.messages[1].text != "committed 👋" {
		t.Fatalf("merged messages count=%d first=%+v last=%+v", chat.messageCount, chat.messages[0], chat.messages[1])
	}
}

func TestChatSummaryRefreshPreservesSelectedInteractionState(t *testing.T) {
	base := time.Date(2100, 1, 2, 10, 0, 0, 0, time.UTC)
	state, err := chatStateFromInitial(InitialState{Chats: []InitialChat{{
		ID: "a", Title: "A", ActivityTime: base,
		Messages: []InitialMessage{{ID: "target", SentAt: base, Text: "quoted", BodyRetained: true}},
	}, {ID: "b", Title: "B", ActivityTime: base.Add(-time.Minute)}}})
	if err != nil {
		t.Fatal(err)
	}
	model := viewModel{chats: state, options: DefaultOptions(), mode: modeCompose, terminalWidth: 100, terminalHeight: 30}
	if !model.composer.insertText("draft Café 👋") {
		t.Fatal("draft setup failed")
	}
	model.composer.moveLeft()
	model.replyTarget = replyTarget{valid: true, id: "target"}
	model.replySelect = replySelectionState{valid: true, index: 0, fromCompose: true}
	model.chatView.scrollOffset = 0
	model.emojiPicker.open = true
	model.settingsOpen = true
	wantComposer, wantTarget, wantSelection := model.composer, model.replyTarget, model.replySelect
	wantPicker, wantSettings, wantView := model.emojiPicker, model.settingsOpen, model.chatView

	update := InitialState{Chats: []InitialChat{
		{ID: "b", Title: "B renamed", ActivityTime: base.Add(time.Minute)},
		{ID: "a", Title: "A renamed", ActivityTime: base, UnreadCount: 9},
	}}
	if !applyChatSummaries(&model, update) {
		t.Fatal("summary refresh rejected")
	}
	selected, ok := model.chats.selectedChat()
	if !ok || selected.id != "a" || selected.title != "A renamed" || selected.unreadCount != 9 || model.localReadRequest.ChatID != "" ||
		model.mode != modeCompose || model.composer != wantComposer || model.replyTarget != wantTarget ||
		model.replySelect != wantSelection || model.emojiPicker != wantPicker || model.settingsOpen != wantSettings ||
		model.chatView != wantView || selected.messages == nil || selected.messages[0].id != "target" {
		t.Fatalf("summary refresh changed interaction: selected=%+v mode=%d composer=%+v target=%+v selection=%+v picker=%+v settings=%t view=%+v",
			selected, model.mode, model.composer, model.replyTarget, model.replySelect, model.emojiPicker, model.settingsOpen, model.chatView)
	}
}

func simulationText(screen tcell.SimulationScreen) string {
	cells, width, height := screen.GetContents()
	var builder strings.Builder
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			builder.WriteString(string(cells[y*width+x].Runes))
		}
		builder.WriteByte('\n')
	}
	return builder.String()
}
