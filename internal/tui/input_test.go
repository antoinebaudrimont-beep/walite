package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestHandleKeyJKChangesChatsAndArrowsFocusMessages(t *testing.T) {
	model := defaultDemoView()
	assertKeyChange(t, &model, tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), true)
	if model.chats.selected != 0 || !model.replySelect.valid {
		t.Fatalf("Up selected chat=%d message=%+v", model.chats.selected, model.replySelect)
	}
	assertKeyChange(t, &model, tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), false)
	if model.chats.selected != 0 {
		t.Fatalf("Down changed chat=%d", model.chats.selected)
	}
	assertKeyChange(t, &model, tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), true)
	if model.chats.selected != 1 || model.replySelect.valid {
		t.Fatalf("j selected=%d", model.chats.selected)
	}
	assertKeyChange(t, &model, tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone), true)
	if model.chats.selected != 0 {
		t.Fatalf("k selected=%d", model.chats.selected)
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
	if model.scrollOffset != maximum || maximum <= 0 {
		t.Fatalf("oldest offset=%d maximum=%d", model.scrollOffset, maximum)
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
	if model.scrollOffset != 0 {
		t.Fatalf("newest offset=%d", model.scrollOffset)
	}
	if changed, _ := handleKey(&model, pageDown, width, height); changed {
		t.Fatal("PageDown redrew at newest bound")
	}
}

func TestControlScrollKeysAndChatChangeReset(t *testing.T) {
	model := defaultDemoView()
	width, height := 60, 10
	assertKeyChangeAtSize(t, &model, tcell.NewEventKey(tcell.KeyCtrlU, 0, tcell.ModNone), width, height, true)
	if model.scrollOffset <= 0 {
		t.Fatalf("Ctrl-U offset=%d", model.scrollOffset)
	}
	assertKeyChangeAtSize(t, &model, tcell.NewEventKey(tcell.KeyCtrlD, 0, tcell.ModNone), width, height, true)
	assertKeyChangeAtSize(t, &model, tcell.NewEventKey(tcell.KeyPgUp, 0, tcell.ModNone), width, height, true)
	assertKeyChangeAtSize(t, &model, tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), width, height, true)
	if model.chats.selected != 1 || model.scrollOffset != 0 {
		t.Fatalf("selected=%d offset=%d", model.chats.selected, model.scrollOffset)
	}
}

func TestClampViewPreservesSelectionAndBoundsScroll(t *testing.T) {
	model := defaultDemoView()
	model.chats.selected = 2
	model.scrollOffset = maxMessages
	clampView(&model, 60, 10)
	if model.chats.selected != 2 {
		t.Fatalf("selected=%d", model.chats.selected)
	}
	maximum := maximumScrollOffset(&model, 60, 10)
	if model.scrollOffset != maximum {
		t.Fatalf("offset=%d maximum=%d", model.scrollOffset, maximum)
	}
	clampView(&model, 100, 30)
	if model.chats.selected != 2 || model.scrollOffset < 0 || model.scrollOffset > maximumScrollOffset(&model, 100, 30) {
		t.Fatalf("selected=%d offset=%d", model.chats.selected, model.scrollOffset)
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
