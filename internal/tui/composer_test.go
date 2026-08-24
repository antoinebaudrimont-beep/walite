package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

func TestEnterComposeMode(t *testing.T) {
	model := defaultDemoView()
	selected := model.chats.selected
	changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), 100, 30)
	if !changed || exit || model.mode != modeCompose || model.chats.selected != selected || model.composer.length != 0 {
		t.Fatalf("changed=%t exit=%t mode=%d selected=%d draft=%q", changed, exit, model.mode, model.chats.selected, model.composer.text())
	}
}

func TestComposerASCIIAndUTF8Editing(t *testing.T) {
	var composer composerState
	insertComposerText(t, &composer, "hello")
	if got := composer.text(); got != "hello" || composer.cursor != len(got) {
		t.Fatalf("text=%q cursor=%d", got, composer.cursor)
	}
	if !composer.backspace() || composer.text() != "hell" {
		t.Fatalf("after backspace=%q", composer.text())
	}

	composer.clear()
	insertComposerText(t, &composer, "héλ🙂")
	if got := composer.text(); got != "héλ🙂" || composer.cursor != len(got) || !utf8.ValidString(got) {
		t.Fatalf("text=%q cursor=%d", got, composer.cursor)
	}
	if !composer.moveLeft() || !composer.delete() || composer.text() != "héλ" {
		t.Fatalf("after UTF-8 delete=%q cursor=%d", composer.text(), composer.cursor)
	}
	if !composer.moveLeft() || !composer.moveRight() || composer.cursor != len("héλ") {
		t.Fatalf("UTF-8 movement cursor=%d", composer.cursor)
	}
}

func TestComposerMovementAndDeletionBounds(t *testing.T) {
	var composer composerState
	if composer.moveLeft() || composer.moveRight() || composer.backspace() || composer.delete() {
		t.Fatal("empty composer changed")
	}
	insertComposerText(t, &composer, "a🙂b")
	for composer.moveLeft() {
	}
	if composer.cursor != 0 || composer.moveLeft() {
		t.Fatalf("left bound cursor=%d", composer.cursor)
	}
	if !composer.delete() || composer.text() != "🙂b" || composer.cursor != 0 {
		t.Fatalf("delete at start text=%q cursor=%d", composer.text(), composer.cursor)
	}
	for composer.moveRight() {
	}
	if composer.cursor != composer.length || composer.moveRight() || composer.delete() {
		t.Fatalf("right bound cursor=%d length=%d", composer.cursor, composer.length)
	}
}

func TestComposerCapacityAndMalformedState(t *testing.T) {
	var composer composerState
	for index := 0; index < maxDraftBytes; index++ {
		if !composer.insert('x') {
			t.Fatalf("insert rejected at %d", index)
		}
	}
	if composer.insert('y') || composer.length != maxDraftBytes {
		t.Fatalf("overflow accepted length=%d", composer.length)
	}

	composer.clear()
	composer.data[0] = 0xff
	composer.length = 1
	composer.cursor = 1
	composer.normalize()
	if composer.length != 0 || composer.cursor != 0 {
		t.Fatalf("invalid UTF-8 survived length=%d cursor=%d", composer.length, composer.cursor)
	}
	copy(composer.data[:], []byte("é"))
	composer.length = len("é")
	composer.cursor = 1
	composer.normalize()
	if composer.cursor != 0 {
		t.Fatalf("cursor split rune at %d", composer.cursor)
	}
}

func TestComposerInsertTextIsAtomic(t *testing.T) {
	var composer composerState
	if composer.insertText("") || composer.insertText("\xff") {
		t.Fatal("empty or invalid UTF-8 insertion changed composer")
	}
	insertComposerText(t, &composer, strings.Repeat("x", maxDraftBytes-len("❤️")+1))
	before := composer.text()
	beforeCursor := composer.cursor
	if composer.insertText("❤️") || composer.text() != before || composer.cursor != beforeCursor {
		t.Fatal("oversized emoji insertion was not atomic")
	}
	composer.clear()
	if !composer.insertText("👍🏽") || composer.text() != "👍🏽" || composer.cursor != len("👍🏽") {
		t.Fatalf("atomic emoji text=%q cursor=%d", composer.text(), composer.cursor)
	}
}

func TestComposerEditsWholeGraphemeClusters(t *testing.T) {
	var composer composerState
	if !composer.insertText("❤️👍🏽✌️") {
		t.Fatal("emoji sequence insertion failed")
	}
	for _, want := range []string{"❤️👍🏽", "❤️", ""} {
		if !composer.backspace() || composer.text() != want {
			t.Fatalf("backspace text=%q want %q", composer.text(), want)
		}
	}
	if composer.backspace() {
		t.Fatal("backspace changed empty composer")
	}

	if !composer.insertText("❤️👍🏽✌️") {
		t.Fatal("emoji reinsertion failed")
	}
	composer.cursor = 0
	for _, want := range []string{"👍🏽✌️", "✌️", ""} {
		if !composer.delete() || composer.text() != want || composer.cursor != 0 {
			t.Fatalf("delete text=%q cursor=%d want %q", composer.text(), composer.cursor, want)
		}
	}
}

func TestComposerMovementAndViewportUseGraphemeCellWidth(t *testing.T) {
	var composer composerState
	if !composer.insertText("abc🙂def") {
		t.Fatal("insert failed")
	}
	composer.cursor = len("abc🙂")
	composer.normalize()
	start, end, cursorCells := visibleDraftSpan(&composer, 5)
	visible := string(composer.data[start:end])
	if cursorCells < 0 || cursorCells >= 5 {
		t.Fatalf("cursor cells=%d", cursorCells)
	}
	if width := uniseg.StringWidth(visible); width > 5 {
		t.Fatalf("visible width=%d span=%q", width, visible)
	}
	if start > composer.cursor || end < composer.cursor || !utf8.ValidString(visible) {
		t.Fatalf("span=%d:%d cursor=%d visible=%q", start, end, composer.cursor, visible)
	}
	if !composer.moveLeft() || composer.cursor != len("abc") {
		t.Fatalf("left crossed part of emoji cursor=%d", composer.cursor)
	}
	if !composer.moveRight() || composer.cursor != len("abc🙂") {
		t.Fatalf("right crossed part of emoji cursor=%d", composer.cursor)
	}
}

func TestComposeEscapeCancelsAndControlCExits(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	insertComposerText(t, &model.composer, "draft")
	changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), 100, 30)
	if !changed || exit || model.mode != modeNavigate || model.composer.length != 0 {
		t.Fatalf("changed=%t exit=%t mode=%d draft=%q", changed, exit, model.mode, model.composer.text())
	}
	model.mode = modeCompose
	changed, exit = handleKey(&model, tcell.NewEventKey(tcell.KeyCtrlC, 0, tcell.ModNone), 100, 30)
	if changed || !exit {
		t.Fatalf("Ctrl-C changed=%t exit=%t", changed, exit)
	}
}

func TestComposeRunesDoNotNavigate(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	selected := model.chats.selected
	for _, character := range "jk" {
		changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyRune, character, tcell.ModNone), 100, 30)
		if !changed || exit {
			t.Fatalf("rune %q changed=%t exit=%t", character, changed, exit)
		}
	}
	if model.composer.text() != "jk" || model.chats.selected != selected {
		t.Fatalf("draft=%q selected=%d", model.composer.text(), model.chats.selected)
	}
}

func TestSubmitLocalMessageAndEmptySend(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	chat := &model.chats.chats[model.chats.selected]
	before := chat.messageCount
	if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), 100, 30); changed || chat.messageCount != before {
		t.Fatalf("empty send changed=%t count=%d", changed, chat.messageCount)
	}
	insertComposerText(t, &model.composer, "hello demo")
	model.scrollOffset = 2
	if changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), 100, 30); !changed || exit {
		t.Fatalf("send changed=%t exit=%t", changed, exit)
	}
	last := chat.messages[chat.messageCount-1]
	if chat.messageCount != before+1 || last.text != "hello demo" || last.time != "now" || model.composer.length != 0 || model.scrollOffset != 0 || model.mode != modeCompose {
		t.Fatalf("count=%d last=%+v draft=%q offset=%d mode=%d", chat.messageCount, last, model.composer.text(), model.scrollOffset, model.mode)
	}
}

func TestSubmitFullChatDropsOldest(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	chat := &model.chats.chats[0]
	chat.messageCount = maxMessages
	for index := 0; index < maxMessages; index++ {
		chat.messages[index] = messageView{time: "old", text: "old-" + twoDigits(index)}
	}
	insertComposerText(t, &model.composer, "new local")
	if !submitLocalMessage(&model) {
		t.Fatal("full-chat send rejected")
	}
	if chat.messageCount != maxMessages || chat.messages[0].text != "old-01" || chat.messages[maxMessages-1].text != "new local" {
		t.Fatalf("count=%d first=%q last=%q", chat.messageCount, chat.messages[0].text, chat.messages[maxMessages-1].text)
	}
}

func TestSyntheticSendIsIsolatedToSelectedChat(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	otherCount := model.chats.chats[1].messageCount
	insertComposerText(t, &model.composer, "chat zero only")
	if !submitLocalMessage(&model) {
		t.Fatal("send rejected")
	}
	if model.chats.chats[1].messageCount != otherCount || strings.Contains(model.chats.chats[1].messages[otherCount-1].text, "chat zero only") {
		t.Fatal("message leaked into another chat")
	}
}

func insertComposerText(t *testing.T, composer *composerState, value string) {
	t.Helper()
	for _, character := range value {
		if !composer.insert(character) {
			t.Fatalf("insert rejected for %q", character)
		}
	}
}
