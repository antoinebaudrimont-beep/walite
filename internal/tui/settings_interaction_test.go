package tui

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func settingsKey(t *testing.T, model *viewModel, key tcell.Key, r rune) {
	t.Helper()
	changed, exit := handleKey(model, tcell.NewEventKey(key, r, tcell.ModNone), 80, 24)
	if !changed || exit {
		t.Fatalf("key=%v rune=%q changed=%v exit=%v", key, r, changed, exit)
	}
}

func TestSettingsKeyboardFocusToggleAndCancelPreserveInteraction(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	model.composer.insertText("draft 👋 café")
	model.composer.moveLeft()
	model.chatView.scrollOffset = 1
	model.replyTarget = replyTarget{valid: true, id: "target"}
	model.emojiPicker.prepareOpen()
	before := model
	beforeChats := *model.chats
	settingsKey(t, &model, tcell.KeyCtrlP, 0)
	settingsKey(t, &model, tcell.KeyEnter, 0)
	settingsKey(t, &model, tcell.KeyDown, 0)
	settingsKey(t, &model, tcell.KeyRune, ' ')
	settingsKey(t, &model, tcell.KeyRune, 'j')
	settingsKey(t, &model, tcell.KeyRune, 'k')
	settingsKey(t, &model, tcell.KeyUp, 0)
	if model.settings.selected != 0 || model.settings.draft.ShowTimestamps || !model.settings.draft.ConfirmQuit {
		t.Fatalf("settings=%+v", model.settings)
	}
	if model.options != before.options {
		t.Fatal("unsaved edits changed active preferences")
	}
	screen := initializedSimulationScreen(t, 80, 24)
	draw(screen, &model)
	screen.Show()
	if !strings.Contains(screenText(screen), "> Timestamps: Off") {
		t.Fatal("selected setting is not visible")
	}
	cells, _, _ := screen.GetContents()
	focused := false
	for _, cell := range cells {
		_, _, attrs := cell.Style.Decompose()
		if len(cell.Runes) > 0 && cell.Runes[0] == '>' && attrs&tcell.AttrReverse != 0 && attrs&tcell.AttrBold != 0 {
			focused = true
		}
	}
	if !focused {
		t.Fatal("selected row lacks highlight and bold focus")
	}
	settingsKey(t, &model, tcell.KeyCtrlP, 0)
	model.settings = before.settings
	model.terminalWidth, model.terminalHeight = before.terminalWidth, before.terminalHeight
	if !reflect.DeepEqual(model, before) {
		t.Fatal("settings cancellation disturbed chat, viewport, composer, reply, emoji, or loader state")
	}
	if !reflect.DeepEqual(*model.chats, beforeChats) {
		t.Fatal("popup mutated unread, revision, or chat selection")
	}
	settingsKey(t, &model, tcell.KeyCtrlP, 0)
	if model.settings.draft != before.options {
		t.Fatal("cancelled edits survived reopening")
	}
	settingsKey(t, &model, tcell.KeyEscape, 0)
}

func TestSettingsSaveAppliesOnlyOnSuccessAndFailureIsControlled(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failure], func(t *testing.T) {
			model := defaultDemoView()
			before := model.options
			model.mode = modeCompose
			model.composer.insertText("preserved 🙂 draft")
			model.composer.moveLeft()
			model.chatView.scrollOffset = 2
			model.replyTarget = replyTarget{valid: true, id: "quote"}
			model.emojiPicker.prepareOpen()
			beforeView, beforeComposer, beforeReply, beforeEmoji, beforeChats := model.chatView, model.composer, model.replyTarget, model.emojiPicker, *model.chats
			openSettings(&model)
			settingsKey(t, &model, tcell.KeyEnter, 0)
			model.settings.selected = settingsSaveRow
			settingsKey(t, &model, tcell.KeyEnter, 0)
			calls := 0
			requestSettingsSave(&model, func(value Options) bool {
				calls++
				if value.ShowTimestamps {
					t.Fatal("save lost edited value")
				}
				return true
			})
			if model.options != before || !model.settings.pending || calls != 1 {
				t.Fatal("save completion not awaited")
			}
			handleKey(&model, tcell.NewEventKey(tcell.KeyEnter, 0, 0), 80, 24)
			requestSettingsSave(&model, func(Options) bool { calls++; return true })
			if calls != 1 {
				t.Fatal("pending save duplicated")
			}
			var err error
			if failure {
				err = errors.New("synthetic disk failure")
			}
			finishSettingsSave(&model, err)
			if failure {
				if model.options != before || !model.settingsOpen || !strings.Contains(model.settings.status, "Save failed") {
					t.Fatal("failed save changed preferences or hid error")
				}
			} else if model.options.ShowTimestamps || model.settingsOpen {
				t.Fatal("successful save not applied")
			}
			if model.mode != modeCompose || model.chatView != beforeView || model.composer != beforeComposer || model.replyTarget != beforeReply || model.emojiPicker != beforeEmoji || !reflect.DeepEqual(*model.chats, beforeChats) {
				t.Fatal("save changed interaction or cached chat state")
			}
		})
	}
}

func TestSettingsUnavailableSaveKeepsDraft(t *testing.T) {
	model := defaultDemoView()
	openSettings(&model)
	model.settings.draft.ShowTimestamps = false
	model.settings.request = true
	requestSettingsSave(&model, nil)
	if !model.options.ShowTimestamps || model.settings.pending || !strings.Contains(model.settings.status, "unavailable") {
		t.Fatal("unavailable save was not controlled")
	}
}

func TestSettingsResizesAndClosesWithoutStaleCells(t *testing.T) {
	model := defaultDemoView()
	screen := initializedSimulationScreen(t, 80, 24)
	openSettings(&model)
	model.settings.selected = 2
	for _, size := range [][2]int{{80, 24}, {40, 12}, {24, 8}, {18, 6}, {8, 3}, {1, 1}, {80, 24}} {
		screen.SetSize(size[0], size[1])
		clampView(&model, size[0], size[1])
		draw(screen, &model)
		screen.Show()
		if model.settings.selected != 2 || !model.settingsOpen {
			t.Fatal("resize changed selection")
		}
		if !strings.Contains(screenText(screen), ">") {
			t.Fatalf("%v lost focus", size)
		}
		if size[0] >= 18 && !strings.Contains(screenText(screen), ": On") {
			t.Fatalf("%v clipped the selected value", size)
		}
		w, h := min(size[0], settingsPopupWidth), min(size[1], settingsPopupHeight)
		if w >= 6 && h >= 5 {
			left, top := (size[0]-w)/2, (size[1]-h)/2
			for y := top + 1; y < top+h-1; y++ {
				for _, x := range []int{left, left + w - 1} {
					r, _, _, _ := screen.GetContent(x, y)
					if r != '│' {
						t.Fatalf("%v text overwrote border at %d,%d", size, x, y)
					}
				}
			}
		}
		fresh := initializedSimulationScreen(t, size[0], size[1])
		draw(fresh, &model)
		fresh.Show()
		a, _, _ := screen.GetContents()
		b, _, _ := fresh.GetContents()
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("%v resize left stale cells", size)
		}
	}
	settingsKey(t, &model, tcell.KeyEscape, 0)
	draw(screen, &model)
	screen.Show()
	fresh := initializedSimulationScreen(t, 80, 24)
	draw(fresh, &model)
	fresh.Show()
	a, _, _ := screen.GetContents()
	b, _, _ := fresh.GetContents()
	if !reflect.DeepEqual(a, b) {
		t.Fatal("closing settings left stale cells")
	}
}

func TestConfirmQuitPreferenceAndCancel(t *testing.T) {
	model := defaultDemoView()
	model.options.ConfirmQuit = true
	settingsKey(t, &model, tcell.KeyEscape, 0)
	if !model.quitConfirm {
		t.Fatal("quit confirmation missing")
	}
	screen := initializedSimulationScreen(t, 80, 24)
	draw(screen, &model)
	screen.Show()
	if !strings.Contains(screenText(screen), "Quit walite?") {
		t.Fatal("confirmation not rendered")
	}
	settingsKey(t, &model, tcell.KeyEscape, 0)
	if model.quitConfirm {
		t.Fatal("confirmation not cancelled")
	}
	settingsKey(t, &model, tcell.KeyEscape, 0)
	if _, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyEnter, 0, 0), 80, 24); !exit {
		t.Fatal("confirmation not accepted")
	}
}

func TestReadReceiptsOffClearLocallyAndEnablingIsNotRetroactive(t *testing.T) {
	model := readReceiptView(t)
	model.options.SendReadReceipts = false
	calls := 0
	admit := func(ReadReceiptRequest) bool { calls++; return true }
	moveChatSelection(&model, 1)
	requestPendingReadReceipt(&model, admit)
	if model.chats.chats[1].unreadCount != 0 || model.localReadRequest.ChatID != "unread@lid" || calls != 0 {
		t.Fatal("Off did not preserve local clear while suppressing remote receipt")
	}
	openSettings(&model)
	model.settings.selected = 2
	settingsKey(t, &model, tcell.KeyEnter, 0)
	model.settings.pending = true
	finishSettingsSave(&model, nil)
	requestPendingReadReceipt(&model, admit)
	moveChatSelection(&model, -1)
	moveChatSelection(&model, 1)
	requestPendingReadReceipt(&model, admit)
	if calls != 0 {
		t.Fatal("enabling sent retroactive receipt")
	}
	moveChatSelection(&model, -1)
	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	applyLiveMessage(&model, LiveMessage{ChatID: "unread@lid", MessageID: "new-after-enable", SentAt: at, ActivityTime: at, UnreadCount: 1, Text: "🐧", BodyRetained: true})
	index, _ := model.chats.chatIndexByID("unread@lid")
	moveChatSelection(&model, index-model.chats.selectedIndex())
	requestPendingReadReceipt(&model, func(request ReadReceiptRequest) bool {
		calls++
		if len(request.Messages) != 1 || request.Messages[0].MessageID != "new-after-enable" {
			t.Fatalf("frontier=%+v", request)
		}
		return true
	})
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestDisablingReceiptsDiscardsDeferredCacheFrontier(t *testing.T) {
	model := readReceiptView(t)
	model.readIntent = readReceiptIntent{chatID: "unread@lid", unreadCount: 2}
	openSettings(&model)
	model.settings.draft.SendReadReceipts = false
	model.settings.pending = true
	finishSettingsSave(&model, nil)
	if model.readIntent.chatID != "" || model.readRequest.ChatID != "" {
		t.Fatal("deferred receipt survived disable")
	}
	model.options.SendReadReceipts = true
	prepareReadReceipt(&model, &model.chats.chats[1])
	if model.readRequest.ChatID != "" {
		t.Fatal("late cache page created retroactive receipt")
	}
}
