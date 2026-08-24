package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestHandleKeyJKChangesChatsAndArrowsScrollMessages(t *testing.T) {
	model := defaultDemoView()
	width, height := 100, 12
	assertKeyChangeAtSize(t, &model, tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), width, height, true)
	if model.chats.selected != 0 || model.replySelect.valid || model.chatView.scrollOffset != 1 {
		t.Fatalf("Up selected chat=%d selection=%+v offset=%d", model.chats.selected, model.replySelect, model.chatView.scrollOffset)
	}
	assertKeyChangeAtSize(t, &model, tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), width, height, true)
	if model.chats.selected != 0 || model.chatView.scrollOffset != 0 {
		t.Fatalf("Down selected chat=%d offset=%d", model.chats.selected, model.chatView.scrollOffset)
	}
	assertKeyChangeAtSize(t, &model, tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), width, height, true)
	if model.chats.selected != 1 || model.replySelect.valid {
		t.Fatalf("j selected=%d", model.chats.selected)
	}
	assertKeyChangeAtSize(t, &model, tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone), width, height, true)
	if model.chats.selected != 0 {
		t.Fatalf("k selected=%d", model.chats.selected)
	}
}

func TestHomeAndEndJumpBetweenOldestAndNewest(t *testing.T) {
	model := defaultDemoView()
	width, height := 100, 12
	if changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyHome, 0, tcell.ModNone), width, height); !changed || exit {
		t.Fatalf("Home changed=%t exit=%t", changed, exit)
	}
	maximum := maximumScrollOffset(&model, width, height)
	if maximum <= 0 || model.chatView.scrollOffset != maximum {
		t.Fatalf("Home offset=%d maximum=%d", model.chatView.scrollOffset, maximum)
	}
	if changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyEnd, 0, tcell.ModNone), width, height); !changed || exit {
		t.Fatalf("End changed=%t exit=%t", changed, exit)
	}
	if model.chatView.scrollOffset != 0 {
		t.Fatalf("End offset=%d", model.chatView.scrollOffset)
	}
	if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyEnd, 0, tcell.ModNone), width, height); changed {
		t.Fatal("End redrew at newest boundary")
	}
}

func TestHandleKeySelectionBoundsDoNotRedraw(t *testing.T) {
	model := defaultDemoView()
	assertKeyChange(t, &model, tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone), false)
	model.chats.selected = model.chats.chatCount - 1
	assertKeyChange(t, &model, tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), false)
	if model.chats.selected != model.chats.chatCount-1 {
		t.Fatalf("selected=%d", model.chats.selected)
	}
}

func TestDrawUsesAdjacentRowsAndWrapsWithoutBlankLine(t *testing.T) {
	screen := initializedSimulationScreen(t, 100, 30)
	model := defaultDemoView()
	draw(screen, &model)
	screen.Show()
	text := screenText(screen)
	first := rowContaining(text, "Synthetic message one")
	second := rowContaining(text, "Synthetic reply")
	if first < 0 || second != first+1 {
		t.Fatalf("message rows first=%d second=%d:\n%s", first, second, text)
	}
	wrapped := rowContaining(text, "Synthetic wrapping preview")
	continuation := rowContaining(text, "demonstrating a longer conversation line")
	if wrapped < 0 || continuation != wrapped+1 {
		t.Fatalf("wrapped rows first=%d continuation=%d:\n%s", wrapped, continuation, text)
	}
}

func TestHandleKeyScrollsAndClamps(t *testing.T) {
	model := defaultDemoView()
	width, height := 100, 12
	newestStart, newestEnd := visibleMessageRange(&model, width, height)
	if newestEnd != model.chats.chats[0].messageCount || newestStart == 0 {
		t.Fatalf("newest range=%d:%d", newestStart, newestEnd)
	}

	pageUp := tcell.NewEventKey(tcell.KeyPgUp, 0, tcell.ModNone)
	for step := 0; step < maxMessages; step++ {
		changed, exit := handleKey(&model, pageUp, width, height)
		if exit {
			t.Fatal("PageUp requested exit")
		}
		if !changed {
			break
		}
	}
	maximum := maximumScrollOffset(&model, width, height)
	if model.chatView.scrollOffset != maximum || maximum <= 0 {
		t.Fatalf("oldest offset=%d maximum=%d", model.chatView.scrollOffset, maximum)
	}
	if changed, _ := handleKey(&model, pageUp, width, height); changed {
		t.Fatal("PageUp redrew at oldest bound")
	}

	pageDown := tcell.NewEventKey(tcell.KeyPgDn, 0, tcell.ModNone)
	for step := 0; step < maxMessages; step++ {
		changed, _ := handleKey(&model, pageDown, width, height)
		if !changed {
			break
		}
	}
	if model.chatView.scrollOffset != 0 {
		t.Fatalf("newest offset=%d", model.chatView.scrollOffset)
	}
	if changed, _ := handleKey(&model, pageDown, width, height); changed {
		t.Fatal("PageDown redrew at newest bound")
	}
}

func TestControlScrollKeysAndChatChangeReset(t *testing.T) {
	model := defaultDemoView()
	width, height := 60, 10
	assertKeyChangeAtSize(t, &model, tcell.NewEventKey(tcell.KeyCtrlU, 0, tcell.ModNone), width, height, true)
	if model.chatView.scrollOffset <= 0 {
		t.Fatalf("Ctrl-U offset=%d", model.chatView.scrollOffset)
	}
	assertKeyChangeAtSize(t, &model, tcell.NewEventKey(tcell.KeyCtrlD, 0, tcell.ModNone), width, height, true)
	assertKeyChangeAtSize(t, &model, tcell.NewEventKey(tcell.KeyPgUp, 0, tcell.ModNone), width, height, true)
	assertKeyChangeAtSize(t, &model, tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), width, height, true)
	if model.chats.selected != 1 || model.chatView.scrollOffset != 0 {
		t.Fatalf("selected=%d offset=%d", model.chats.selected, model.chatView.scrollOffset)
	}
}

func TestClampViewPreservesSelectionAndBoundsScroll(t *testing.T) {
	model := defaultDemoView()
	model.chats.selected = 2
	model.chatView.scrollOffset = maxMessages
	clampView(&model, 60, 10)
	if model.chats.selected != 2 {
		t.Fatalf("selected=%d", model.chats.selected)
	}
	maximum := maximumScrollOffset(&model, 60, 10)
	if model.chatView.scrollOffset != maximum {
		t.Fatalf("offset=%d maximum=%d", model.chatView.scrollOffset, maximum)
	}
	clampView(&model, 100, 30)
	if model.chats.selected != 2 || model.chatView.scrollOffset < 0 || model.chatView.scrollOffset > maximumScrollOffset(&model, 100, 30) {
		t.Fatalf("selected=%d offset=%d", model.chats.selected, model.chatView.scrollOffset)
	}
}

func TestIncomingMessagePreservesOlderReadingPosition(t *testing.T) {
	model := defaultDemoView()
	width, height := 60, 10
	clampView(&model, width, height)
	model.chatView.scrollOffset = 4
	beforeStart, beforeEnd := visibleMessageRange(&model, width, height)
	chat, _ := model.chats.selectedChat()
	wantFirst := chat.messages[beforeStart].id
	wantLast := chat.messages[beforeEnd-1].id

	if !recordIncomingMessage(&model, model.chats.selectedIndex(), "synthetic incoming while reading") {
		t.Fatal("incoming message rejected")
	}
	afterStart, afterEnd := visibleMessageRange(&model, width, height)
	chat, _ = model.chats.selectedChat()
	if chat.messages[afterStart].id != wantFirst || chat.messages[afterEnd-1].id != wantLast {
		t.Fatalf("viewport changed from IDs %d:%d to %d:%d", wantFirst, wantLast,
			chat.messages[afterStart].id, chat.messages[afterEnd-1].id)
	}
}

func assertKeyChange(t *testing.T, model *viewModel, event *tcell.EventKey, want bool) {
	t.Helper()
	assertKeyChangeAtSize(t, model, event, 100, 30, want)
}

func assertKeyChangeAtSize(t *testing.T, model *viewModel, event *tcell.EventKey, width, height int, want bool) {
	t.Helper()
	changed, exit := handleKey(model, event, width, height)
	if exit || changed != want {
		t.Fatalf("key=%v changed=%t exit=%t want changed=%t", event.Key(), changed, exit, want)
	}
}

func rowContaining(text, value string) int {
	for row, line := range strings.Split(text, "\n") {
		if strings.Contains(line, value) {
			return row
		}
	}
	return -1
}
