package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestReactionUpdateAddsChangesRemovesAndIsIdempotent(t *testing.T) {
	model := defaultDemoView()
	chat, _ := model.chats.selectedChat()
	target := string(chat.messages[0].id)
	update := ReactionUpdate{ChatID: chat.id, TargetMessageID: target, Groups: []ReactionGroup{{Emoji: "👍", Count: 2}}}
	if !applyReactionUpdate(&model, update) || applyReactionUpdate(&model, update) {
		t.Fatal("reaction add was not changed then idempotent")
	}
	if got := messageReactionText(chat.messages[0]); got != "👍 2" {
		t.Fatalf("render text=%q", got)
	}
	changed := ReactionUpdate{ChatID: chat.id, TargetMessageID: target, Groups: []ReactionGroup{{Emoji: "❤️", Count: 1, Own: true}}}
	if !applyReactionUpdate(&model, changed) || messageReactionText(chat.messages[0]) != "❤️ 1" {
		t.Fatal("reaction replacement failed")
	}
	if !applyReactionUpdate(&model, ReactionUpdate{ChatID: chat.id, TargetMessageID: target}) || messageReactionText(chat.messages[0]) != "" {
		t.Fatal("reaction removal failed")
	}
}

func TestReactionUsesFocusedMessageAndExistingEmojiPicker(t *testing.T) {
	model := defaultDemoView()
	chat, _ := model.chats.selectedChat()
	target := chat.messages[chat.messageCount-1]
	var sent SendRequest
	model.send = func(request SendRequest) error { sent = request; return nil }
	if changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyCtrlR, 0, tcell.ModNone), 100, 24); !changed || exit || !model.replySelect.valid {
		t.Fatal("message focus failed")
	}
	if changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyRune, 'R', tcell.ModNone), 100, 24); !changed || exit || !model.emojiPicker.open || !model.reactionTarget.valid {
		t.Fatal("reaction picker did not open")
	}
	wantEmoji := model.emojiPicker.selectedEmoji()
	if changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), 100, 24); !changed || exit {
		t.Fatal("reaction was not submitted")
	}
	if sent.ChatID != chat.id || sent.ReactionTargetID != string(target.id) || sent.ReactionTargetFromMe != target.fromMe || sent.ReactionEmoji != wantEmoji || sent.Text != "" || sent.FilePath != "" {
		t.Fatalf("request=%+v", sent)
	}
}

func TestReactionRemovalAndGroupParticipantIdentity(t *testing.T) {
	initial := InitialState{Chats: []InitialChat{{ID: "family@g.us", Title: "Family", IsGroup: true, Messages: []InitialMessage{{
		ID: "group-target", Text: "hello", BodyRetained: true, SenderID: "reactor@lid", IsGroup: true,
	}}}}}
	state, err := chatStateFromInitial(initial)
	if err != nil {
		t.Fatal(err)
	}
	model := viewModel{chats: state, options: DefaultOptions()}
	var sent SendRequest
	model.send = func(request SendRequest) error { sent = request; return nil }
	model.replySelect = replySelectionState{valid: true, index: 0}
	if !beginReaction(&model) || !handleEmojiKey(&model, tcell.NewEventKey(tcell.KeyDelete, 0, tcell.ModNone), 100, 24) {
		t.Fatal("removal was not sent")
	}
	if sent.ReactionTargetID != "group-target" || sent.ReactionTargetSenderID != "reactor@lid" || !sent.ReactionIsGroup || sent.ReactionEmoji != "" {
		t.Fatalf("request=%+v", sent)
	}
}

func TestReactionRenderingIsOneBoundedViewportRow(t *testing.T) {
	model := defaultDemoView()
	chat, _ := model.chats.selectedChat()
	chat.messageCount = 1
	chat.messages[0] = messageView{id: "target", text: "hello", bodyRetained: true, reactionCount: maxReactionGroups}
	for index := 0; index < maxReactionGroups; index++ {
		chat.messages[0].reactions[index] = reactionGroupView{emoji: emojiCategories[index%len(emojiCategories)].values[index%16], count: 64}
	}
	if lines := wrappedMessageLines(chat.messages[0], 20, false); lines != 2 {
		t.Fatalf("viewport lines=%d", lines)
	}
	screen := initializedSimulationScreen(t, 80, 12)
	draw(screen, &model)
	screen.Show()
	text := replyScreenText(screen)
	if !strings.Contains(text, "hello") || !strings.Contains(text, "64") {
		t.Fatalf("reaction not visible:\n%s", text)
	}
}

func TestReplyOnlyReactionViewportMeasurementMatchesRendering(t *testing.T) {
	message := messageView{id: "target", hasReply: true, replyToID: "missing", reactionCount: 1}
	message.reactions[0] = reactionGroupView{emoji: "👍", count: 1}
	if lines := wrappedMessageLines(message, 20, false); lines != 2 {
		t.Fatalf("viewport lines=%d", lines)
	}
}

func TestPendingReactionAppliesWhenBaseMessageArrives(t *testing.T) {
	model := defaultDemoView()
	update := ReactionUpdate{ChatID: "demo", TargetMessageID: "arrives-later", Groups: []ReactionGroup{{Emoji: "🔥", Count: 1}}}
	if applyReactionUpdate(&model, update) || model.pendingReactionCount != 1 {
		t.Fatal("unknown target was not retained in bounded pending state")
	}
	chat, _ := model.chats.selectedChat()
	if !applyLiveMessage(&model, LiveMessage{ChatID: "demo", MessageID: "arrives-later", SentAt: chat.activityTime.Add(1), Text: "later", BodyRetained: true, ActivityTime: chat.activityTime.Add(1)}) {
		t.Fatal("base message rejected")
	}
	message, ok := model.chats.findMessageByID(model.chats.selectedIndex(), "arrives-later")
	if !ok || messageReactionText(message) != "🔥 1" || model.pendingReactionCount != 0 {
		t.Fatalf("message=%+v pending=%d", message, model.pendingReactionCount)
	}
}

func TestReactionUpdateRejectsOversizedGraphemeBeforePendingStorage(t *testing.T) {
	model := defaultDemoView()
	oversized := strings.Repeat("\u0301", maxReactionEmojiBytes+1)
	update := ReactionUpdate{ChatID: "unknown", TargetMessageID: "unknown", Groups: []ReactionGroup{{Emoji: "a" + oversized, Count: 1}}}
	if applyReactionUpdate(&model, update) || model.pendingReactionCount != 0 {
		t.Fatalf("oversized update retained; pending=%d", model.pendingReactionCount)
	}
}
