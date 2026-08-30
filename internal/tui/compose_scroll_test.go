package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestComposeScrollPreservesDraftCursorReplyAndChat(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	if !model.composer.insertText("hello how are you") {
		t.Fatal("draft setup failed")
	}
	model.composer.cursor = len("hello ")
	model.replyTarget = replyTarget{valid: true, id: model.chats.chats[0].messages[0].id}

	wantDraft := model.composer.text()
	wantCursor := model.composer.cursor
	wantTarget := model.replyTarget
	wantChat := model.chats.selectedIndex()
	width, height := 100, 12

	if changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), width, height); !changed || exit {
		t.Fatalf("Up changed=%t exit=%t", changed, exit)
	}
	if model.chatView.scrollOffset != 1 {
		t.Fatalf("Up offset=%d", model.chatView.scrollOffset)
	}
	assertComposeReadingState(t, &model, wantDraft, wantCursor, wantTarget, wantChat)

	if changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), width, height); !changed || exit {
		t.Fatalf("Down changed=%t exit=%t", changed, exit)
	}
	if model.chatView.scrollOffset != 0 {
		t.Fatalf("Down offset=%d", model.chatView.scrollOffset)
	}
	assertComposeReadingState(t, &model, wantDraft, wantCursor, wantTarget, wantChat)
}

func TestComposePageScrollAndEnd(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	if !model.composer.insertText("page through history") {
		t.Fatal("draft setup failed")
	}
	model.composer.cursor = len("page ")
	wantDraft := model.composer.text()
	wantCursor := model.composer.cursor
	wantChat := model.chats.selectedIndex()
	width, height := 100, 12

	if changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyPgUp, 0, tcell.ModNone), width, height); !changed || exit {
		t.Fatalf("PageUp changed=%t exit=%t", changed, exit)
	}
	pageOffset := model.chatView.scrollOffset
	if pageOffset <= 1 {
		t.Fatalf("PageUp offset=%d", pageOffset)
	}
	assertComposeReadingState(t, &model, wantDraft, wantCursor, replyTarget{}, wantChat)

	if changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyPgDn, 0, tcell.ModNone), width, height); !changed || exit {
		t.Fatalf("PageDown changed=%t exit=%t", changed, exit)
	}
	if model.chatView.scrollOffset >= pageOffset {
		t.Fatalf("PageDown offset=%d previous=%d", model.chatView.scrollOffset, pageOffset)
	}
	assertComposeReadingState(t, &model, wantDraft, wantCursor, replyTarget{}, wantChat)

	if model.chatView.scrollOffset == 0 {
		if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), width, height); !changed {
			t.Fatal("Up did not restore older reading position")
		}
	}
	if changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyEnd, 0, tcell.ModNone), width, height); !changed || exit {
		t.Fatalf("End changed=%t exit=%t", changed, exit)
	}
	if model.chatView.scrollOffset != 0 {
		t.Fatalf("End offset=%d", model.chatView.scrollOffset)
	}
	assertComposeReadingState(t, &model, wantDraft, wantCursor, replyTarget{}, wantChat)
}

func TestComposeResizeClampsScrollWithoutChangingComposerState(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	if !model.composer.insertText("resize while reading") {
		t.Fatal("draft setup failed")
	}
	model.composer.cursor = len("resize ")
	model.replyTarget = replyTarget{valid: true, id: model.chats.chats[0].messages[0].id}
	wantDraft := model.composer.text()
	wantCursor := model.composer.cursor
	wantTarget := model.replyTarget
	wantChat := model.chats.selectedIndex()

	if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyPgUp, 0, tcell.ModNone), 60, 10); !changed {
		t.Fatal("PageUp did not establish older reading position")
	}
	for _, size := range [][2]int{{50, 8}, {100, 12}, {60, 10}} {
		previous := model.chatView.scrollOffset
		clampView(&model, size[0], size[1])
		maximum := maximumScrollOffset(&model, size[0], size[1])
		wantOffset := previous
		if wantOffset > maximum {
			wantOffset = maximum
		}
		if model.chatView.scrollOffset != wantOffset {
			t.Fatalf("%dx%d offset=%d want=%d maximum=%d", size[0], size[1], model.chatView.scrollOffset, wantOffset, maximum)
		}
		assertComposeReadingState(t, &model, wantDraft, wantCursor, wantTarget, wantChat)
	}
}

func TestComposeHistoryIndicatorClearsAtNewest(t *testing.T) {
	screen := initializedSimulationScreen(t, 100, 12)
	model := defaultDemoView()
	model.mode = modeCompose
	if !model.composer.insertText("visible draft") {
		t.Fatal("draft setup failed")
	}
	if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), 100, 12); !changed {
		t.Fatal("Up did not scroll")
	}
	draw(screen, &model)
	screen.Show()
	if text := screenText(screen); !strings.Contains(text, "newer message") || !strings.Contains(text, "visible draft") {
		t.Fatalf("compose history frame incomplete:\n%s", text)
	}

	if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyEnd, 0, tcell.ModNone), 100, 12); !changed {
		t.Fatal("End did not return to newest")
	}
	draw(screen, &model)
	screen.Show()
	if text := screenText(screen); strings.Contains(text, "newer message") || !strings.Contains(text, "visible draft") {
		t.Fatalf("newest compose frame incorrect:\n%s", text)
	}
}

func TestComposeTypingAfterScrollAndSendReturnsToNewest(t *testing.T) {
	model := defaultDemoView()
	installCommittedTestSender(&model)
	model.mode = modeCompose
	selected := model.chats.selectedIndex()
	width, height := 100, 12
	for _, character := range "hello how are you" {
		if changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyRune, character, tcell.ModNone), width, height); !changed || exit {
			t.Fatalf("insert %q changed=%t exit=%t", character, changed, exit)
		}
	}
	if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), width, height); !changed {
		t.Fatal("Up did not scroll")
	}
	if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), width, height); !changed {
		t.Fatal("Down did not scroll")
	}
	if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), width, height); !changed {
		t.Fatal("second Up did not scroll")
	}
	for _, character := range "!" {
		if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyRune, character, tcell.ModNone), width, height); !changed {
			t.Fatalf("continued insert %q did not change", character)
		}
	}
	if changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), width, height); !changed || exit {
		t.Fatalf("send changed=%t exit=%t", changed, exit)
	}
	chat, ok := model.chats.selectedChat()
	if !ok || chat.messages[chat.messageCount-1].text != "hello how are you!" {
		t.Fatalf("sent chat available=%t last=%q", ok, chat.messages[chat.messageCount-1].text)
	}
	if model.chatView.scrollOffset != 0 || model.mode != modeCompose || model.chats.selectedIndex() != selected {
		t.Fatalf("offset=%d mode=%d selected=%d want=%d", model.chatView.scrollOffset, model.mode, model.chats.selectedIndex(), selected)
	}
}

func assertComposeReadingState(t *testing.T, model *viewModel, draft string, cursor int, target replyTarget, chat int) {
	t.Helper()
	if model.mode != modeCompose || model.composer.text() != draft || model.composer.cursor != cursor ||
		model.replyTarget != target || model.replySelect.valid || model.chats.selectedIndex() != chat {
		t.Fatalf("mode=%d draft=%q cursor=%d target=%+v selection=%+v chat=%d",
			model.mode, model.composer.text(), model.composer.cursor, model.replyTarget, model.replySelect, model.chats.selectedIndex())
	}
}
