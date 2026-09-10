package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestNavigationPasteSDoesNotSave(t *testing.T) {
	model, calls := pasteCommandModel(t)
	routePasteRunes(t, &model, "S")
	if calls.save != 0 || calls.preview != 0 || model.mode != modeNavigate {
		t.Fatalf("calls=%+v mode=%d", calls, model.mode)
	}
}

func TestNavigationPastePDoesNotPreview(t *testing.T) {
	model, calls := pasteCommandModel(t)
	routePasteRunes(t, &model, "P")
	if calls.preview != 0 || calls.save != 0 || model.mode != modeNavigate {
		t.Fatalf("calls=%+v mode=%d", calls, model.mode)
	}
}

func TestNavigationPasteFDoesNotEnterFileMode(t *testing.T) {
	model, _ := pasteCommandModel(t)
	routePasteRunes(t, &model, "F")
	if model.mode != modeNavigate || model.composer.length != 0 {
		t.Fatalf("mode=%d input=%q", model.mode, model.composer.text())
	}
}

func TestNavigationMixedPasteExecutesZeroCommands(t *testing.T) {
	model, calls := pasteCommandModel(t)
	paste := pasteRoutingState{}
	paste.transition(&model, tcell.NewEventPaste(true))
	for _, event := range []*tcell.EventKey{
		tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone),
		tcell.NewEventKey(tcell.KeyRune, 'S', tcell.ModShift),
		tcell.NewEventKey(tcell.KeyRune, 'P', tcell.ModShift),
		tcell.NewEventKey(tcell.KeyRune, 'F', tcell.ModShift),
		tcell.NewEventKey(tcell.KeyRune, 'O', tcell.ModShift),
		tcell.NewEventKey(tcell.KeyCtrlR, 0, tcell.ModCtrl),
		tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone),
		tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone),
		tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone),
	} {
		if handled, changed := paste.routeKey(&model, event); !handled || changed {
			t.Fatalf("pasted key=%v handled=%t changed=%t", event.Key(), handled, changed)
		}
	}
	paste.transition(&model, tcell.NewEventPaste(false))
	if *calls != (pasteCommandCalls{}) || model.mode != modeNavigate || model.composer.length != 0 || model.replySelect.valid {
		t.Fatalf("calls=%+v mode=%d input=%q reply=%+v", calls, model.mode, model.composer.text(), model.replySelect)
	}
}

func TestComposePasteInsertsFullPrintableText(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	want := "Café S/P/F 日本語 👋"
	routePasteRunes(t, &model, want)
	if model.mode != modeCompose || model.composer.text() != want {
		t.Fatalf("mode=%d draft=%q", model.mode, model.composer.text())
	}
}

func TestFileModePasteInsertsFullPath(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeFile
	want := "/tmp/Café S P F $(literal).pdf"
	routePasteRunes(t, &model, want)
	if model.mode != modeFile || model.composer.text() != want {
		t.Fatalf("mode=%d path=%q", model.mode, model.composer.text())
	}
}

func TestOrdinaryPressedSPFRemainCommands(t *testing.T) {
	model, calls := pasteCommandModel(t)
	for _, test := range []struct {
		key    rune
		action MediaAction
	}{
		{'S', MediaSave},
		{'P', MediaPreview},
	} {
		changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyRune, test.key, tcell.ModShift), 100, 30)
		if !changed || exit {
			t.Fatalf("key=%q changed=%t exit=%t", test.key, changed, exit)
		}
		closeMedia(&model)
	}
	changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyRune, 'F', tcell.ModShift), 100, 30)
	if !changed || exit || model.mode != modeFile || calls.save != 1 || calls.preview != 1 {
		t.Fatalf("changed=%t exit=%t mode=%d calls=%+v", changed, exit, model.mode, calls)
	}
}

func TestPasteIgnoresCommandKeysEvenInTextEntry(t *testing.T) {
	for _, mode := range []inputMode{modeCompose, modeFile} {
		model := defaultDemoView()
		model.mode = mode
		paste := pasteRoutingState{}
		paste.transition(&model, tcell.NewEventPaste(true))
		for _, key := range []tcell.Key{tcell.KeyEnter, tcell.KeyEscape, tcell.KeyCtrlR, tcell.KeyUp, tcell.KeyDown} {
			if handled, changed := paste.routeKey(&model, tcell.NewEventKey(key, 0, tcell.ModNone)); !handled || changed {
				t.Fatalf("mode=%d key=%v handled=%t changed=%t", mode, key, handled, changed)
			}
		}
		if model.mode != mode || model.composer.length != 0 {
			t.Fatalf("mode=%d became=%d input=%q", mode, model.mode, model.composer.text())
		}
	}
}

type pasteCommandCalls struct{ save, preview int }

func pasteCommandModel(t *testing.T) (viewModel, *pasteCommandCalls) {
	t.Helper()
	model := defaultDemoView()
	chat, ok := model.chats.selectedChat()
	if !ok || chat.messageCount == 0 {
		t.Fatal("fixture has no selected message")
	}
	chat.messages[chat.messageCount-1].mediaKind = mediaImage
	calls := &pasteCommandCalls{}
	model.media = func(request MediaRequest) bool {
		switch request.Action {
		case MediaSave:
			calls.save++
		case MediaPreview:
			calls.preview++
		}
		return true
	}
	return model, calls
}

func routePasteRunes(t *testing.T, model *viewModel, value string) {
	t.Helper()
	paste := pasteRoutingState{}
	paste.transition(model, tcell.NewEventPaste(true))
	for _, character := range value {
		if handled, changed := paste.routeKey(model, tcell.NewEventKey(tcell.KeyRune, character, tcell.ModNone)); !handled || changed != acceptsPastedText(model) {
			t.Fatalf("character=%q handled=%t changed=%t", character, handled, changed)
		}
	}
	paste.transition(model, tcell.NewEventPaste(false))
}
