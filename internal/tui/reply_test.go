package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestDefaultMessagesHaveDeterministicStableIDs(t *testing.T) {
	model := defaultDemoView()
	want := messageID(1)
	for chatIndex := 0; chatIndex < model.chats.chatCount; chatIndex++ {
		chat := &model.chats.chats[chatIndex]
		for messageIndex := 0; messageIndex < chat.messageCount; messageIndex++ {
			if chat.messages[messageIndex].id != want {
				t.Fatalf("chat=%d message=%d id=%d want=%d", chatIndex, messageIndex, chat.messages[messageIndex].id, want)
			}
			want++
		}
	}
	if model.chats.nextMessageID != want {
		t.Fatalf("next ID=%d want=%d", model.chats.nextMessageID, want)
	}
}

func TestArrowFocusesNewestVisibleMessageInsideConversation(t *testing.T) {
	model := defaultDemoView()
	_, end := visibleMessageRange(&model, 100, 20)
	changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), 100, 20)
	if !changed || exit || model.mode != modeNavigate || !model.replySelect.valid || model.replySelect.index != end-1 {
		t.Fatalf("changed=%t exit=%t mode=%d selection=%+v visibleEnd=%d", changed, exit, model.mode, model.replySelect, end)
	}
}

func TestReplySelectionDoesNothingForEmptyChat(t *testing.T) {
	model := defaultDemoView()
	model.chats.chats[0].messageCount = 0
	if changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), 100, 20); changed || exit || model.mode != modeNavigate {
		t.Fatalf("changed=%t exit=%t mode=%d", changed, exit, model.mode)
	}
}

func TestReplySelectionMovesClampsAndStaysVisible(t *testing.T) {
	model := defaultDemoView()
	width, height := 60, 10
	if !focusNewestVisibleMessage(&model, width, height) {
		t.Fatal("reply selection did not open")
	}
	for model.replySelect.index > 0 {
		if !moveMessageFocus(&model, -1, width, height) {
			t.Fatalf("older movement stopped at %d", model.replySelect.index)
		}
		start, end := visibleMessageRange(&model, width, height)
		if model.replySelect.index < start || model.replySelect.index >= end {
			t.Fatalf("selection=%d outside visible %d:%d", model.replySelect.index, start, end)
		}
	}
	if moveMessageFocus(&model, -1, width, height) {
		t.Fatal("older boundary requested redraw")
	}
	if !moveMessageFocus(&model, 1, width, height) || model.replySelect.index != 1 {
		t.Fatalf("newer selection=%d", model.replySelect.index)
	}
}

func TestReplySelectionCancelAndChoose(t *testing.T) {
	model := defaultDemoView()
	if !focusNewestVisibleMessage(&model, 100, 20) {
		t.Fatal("reply selection did not open")
	}
	if changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), 100, 20); !changed || exit || model.mode != modeNavigate || model.replySelect.valid || model.replyTarget.valid {
		t.Fatalf("cancel changed=%t exit=%t mode=%d selection=%+v target=%+v", changed, exit, model.mode, model.replySelect, model.replyTarget)
	}
	if !focusNewestVisibleMessage(&model, 100, 20) {
		t.Fatal("reply selection did not reopen")
	}
	wantID := model.chats.chats[0].messages[model.replySelect.index].id
	insertComposerText(t, &model.composer, "stale")
	if changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), 100, 20); !changed || exit {
		t.Fatalf("choose changed=%t exit=%t", changed, exit)
	}
	if model.mode != modeCompose || !model.replyTarget.valid || model.replyTarget.id != wantID || model.replySelect.valid || model.composer.length != 0 {
		t.Fatalf("mode=%d target=%+v selection=%+v draft=%q", model.mode, model.replyTarget, model.replySelect, model.composer.text())
	}
}

func TestReplyTargetSurvivesEmojiInsertion(t *testing.T) {
	model := defaultDemoView()
	if !focusNewestVisibleMessage(&model, 100, 20) || !chooseReplyTarget(&model) {
		t.Fatal("reply target setup failed")
	}
	target := model.replyTarget
	if !model.composer.insertText("Thanks") {
		t.Fatal("draft insertion failed")
	}
	if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyCtrlE, 0, tcell.ModNone), 100, 20); !changed {
		t.Fatal("picker did not open")
	}
	model.emojiPicker.categoryCursor = 7
	if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), 100, 20); !changed {
		t.Fatal("emoji did not insert")
	}
	if model.composer.text() != "Thanks🙂" || model.replyTarget != target {
		t.Fatalf("draft=%q target=%+v want=%+v", model.composer.text(), model.replyTarget, target)
	}
}

func TestSubmitSyntheticReplyUsesStableTargetAndClearsState(t *testing.T) {
	model := defaultDemoView()
	if !focusNewestVisibleMessage(&model, 100, 20) || !chooseReplyTarget(&model) {
		t.Fatal("reply target setup failed")
	}
	targetID := model.replyTarget.id
	chat := &model.chats.chats[0]
	before := chat.messageCount
	if !model.composer.insertText("Thanks, that makes sense") || !submitLocalMessage(&model) {
		t.Fatal("reply send failed")
	}
	message := chat.messages[chat.messageCount-1]
	if chat.messageCount != before+1 || !message.hasReply || message.replyToID != targetID || message.id == 0 {
		t.Fatalf("count=%d message=%+v target=%d", chat.messageCount, message, targetID)
	}
	if model.replyTarget.valid || model.composer.length != 0 || model.scrollOffset != 0 || model.mode != modeCompose {
		t.Fatalf("target=%+v draft=%q offset=%d mode=%d", model.replyTarget, model.composer.text(), model.scrollOffset, model.mode)
	}
}

func TestReplyToEvictedOriginalRetainsIDWithoutPointer(t *testing.T) {
	model := defaultDemoView()
	chat := &model.chats.chats[0]
	chat.messageCount = maxMessages
	for index := 0; index < maxMessages; index++ {
		chat.messages[index] = messageView{id: messageID(100 + index), time: "old", text: "old-" + twoDigits(index)}
	}
	model.chats.nextMessageID = 1000
	model.mode = modeCompose
	model.replyTarget = replyTarget{valid: true, id: chat.messages[0].id}
	if !model.composer.insertText("reply after eviction") || !submitLocalMessage(&model) {
		t.Fatal("reply send failed")
	}
	reply := chat.messages[maxMessages-1]
	if !reply.hasReply || reply.replyToID != 100 {
		t.Fatalf("reply=%+v", reply)
	}
	if _, found := model.chats.findMessageByID(0, reply.replyToID); found {
		t.Fatal("oldest original was not evicted")
	}
}

func TestEscapeFromReplyComposeClearsDraftAndTarget(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	model.replyTarget = replyTarget{valid: true, id: model.chats.chats[0].messages[0].id}
	insertComposerText(t, &model.composer, "cancel me")
	changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), 100, 20)
	if !changed || exit || model.mode != modeNavigate || model.replyTarget.valid || model.composer.length != 0 {
		t.Fatalf("changed=%t exit=%t mode=%d target=%+v draft=%q", changed, exit, model.mode, model.replyTarget, model.composer.text())
	}
}

func TestControlRFromComposePreservesDraftAndCursor(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	if !model.composer.insertText("draft in progress") {
		t.Fatal("draft setup failed")
	}
	model.composer.cursor = len("draft ")
	wantDraft := model.composer.text()
	wantCursor := model.composer.cursor

	changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyCtrlR, 0, tcell.ModNone), 100, 24)
	if !changed || exit || !model.replySelect.valid || !model.replySelect.fromCompose {
		t.Fatalf("changed=%t exit=%t selection=%+v", changed, exit, model.replySelect)
	}
	if model.mode != modeCompose || model.composer.text() != wantDraft || model.composer.cursor != wantCursor {
		t.Fatalf("mode=%d draft=%q cursor=%d", model.mode, model.composer.text(), model.composer.cursor)
	}
}

func TestCancelComposeReplySelectionPreservesDraftCursorAndTarget(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	model.replyTarget = replyTarget{valid: true, id: model.chats.chats[0].messages[0].id}
	if !model.composer.insertText("keep this draft") {
		t.Fatal("draft setup failed")
	}
	model.composer.cursor = len("keep ")
	wantDraft := model.composer.text()
	wantCursor := model.composer.cursor
	wantTarget := model.replyTarget
	if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyCtrlR, 0, tcell.ModNone), 100, 24); !changed {
		t.Fatal("Ctrl-R did not enter reply selection")
	}
	if changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), 100, 24); !changed || exit {
		t.Fatalf("cancel changed=%t exit=%t", changed, exit)
	}
	if model.replySelect.valid || model.mode != modeCompose || model.composer.text() != wantDraft ||
		model.composer.cursor != wantCursor || model.replyTarget != wantTarget {
		t.Fatalf("selection=%+v mode=%d draft=%q cursor=%d target=%+v",
			model.replySelect, model.mode, model.composer.text(), model.composer.cursor, model.replyTarget)
	}
}

func TestConfirmComposeReplySelectionSetsTargetWithoutChangingDraft(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	if !model.composer.insertText("reply text") {
		t.Fatal("draft setup failed")
	}
	model.composer.cursor = len("reply")
	wantDraft := model.composer.text()
	wantCursor := model.composer.cursor
	if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyCtrlR, 0, tcell.ModNone), 100, 24); !changed {
		t.Fatal("Ctrl-R did not enter reply selection")
	}
	if changed, _ := handleKey(&model, tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), 100, 24); !changed {
		t.Fatal("message selection did not move")
	}
	wantID := model.chats.chats[model.chats.selected].messages[model.replySelect.index].id
	if changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), 100, 24); !changed || exit {
		t.Fatalf("confirm changed=%t exit=%t", changed, exit)
	}
	if !model.replyTarget.valid || model.replyTarget.id != wantID || model.replySelect.valid ||
		model.composer.text() != wantDraft || model.composer.cursor != wantCursor {
		t.Fatalf("target=%+v selection=%+v draft=%q cursor=%d",
			model.replyTarget, model.replySelect, model.composer.text(), model.composer.cursor)
	}
}
