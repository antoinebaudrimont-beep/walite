package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func TestInitialStateOwnsBoundedApplicationSnapshot(t *testing.T) {
	sentAt := time.Date(2100, 3, 4, 5, 6, 0, 0, time.FixedZone("fixture", 2*60*60))
	initial := InitialState{Chats: []InitialChat{
		{
			ID: "new-contact", Title: "Café 東京 ❤️ 👍🏽 👨‍👩‍👧‍👦", IsGroup: false,
			UnreadCount: 7, ActivityTime: sentAt.Add(time.Minute),
			Messages: []InitialMessage{
				{ID: "bodyless", SentAt: sentAt, Text: "must not leak", BodyRetained: false},
				{ID: "outgoing", SentAt: sentAt.Add(time.Minute), FromMe: true, Text: "Hello 🙂", BodyRetained: true},
			},
		},
		{ID: "group", Title: "Project Group", IsGroup: true, ActivityTime: sentAt},
	}}
	state, err := chatStateFromInitial(initial)
	if err != nil {
		t.Fatal(err)
	}
	initial.Chats[0].Title = "mutated"
	initial.Chats[0].Messages[1].Text = "mutated"
	chat := &state.chats[0]
	if chat.id != "new-contact" || chat.title != "Café 東京 ❤️ 👍🏽 👨‍👩‍👧‍👦" || chat.isGroup || chat.unreadCount != 7 || !chat.activityTime.Equal(sentAt.Add(time.Minute)) {
		t.Fatalf("chat fidelity lost: %+v", chat)
	}
	if chat.messages[0].text != "" || chat.messages[0].bodyRetained || chat.messages[0].fromMe {
		t.Fatalf("bodyless message rendered synthetic content: %+v", chat.messages[0])
	}
	if got := chat.messages[1]; got.id != "outgoing" || got.text != "Hello 🙂" || !got.fromMe || !got.bodyRetained || got.time != localMessageTime(sentAt.Add(time.Minute)) {
		t.Fatalf("message fidelity lost: %+v", got)
	}
	if !state.chats[1].isGroup {
		t.Fatal("group identity was not retained")
	}
	screen := initializedSimulationScreen(t, 100, 30)
	model := viewModel{chats: state, options: DefaultOptions()}
	draw(screen, &model)
	screen.Show()
	if rendered := screenText(screen); !strings.Contains(rendered, "Café") || !strings.Contains(rendered, "Hello 🙂") || strings.Contains(rendered, "must not leak") {
		t.Fatalf("new contact snapshot rendering:\n%s", rendered)
	}
}

func TestInitialStateSupportsEmptyOneAndMultipleChats(t *testing.T) {
	for _, count := range []int{0, 1, ChatWorkingSetCapacity} {
		initial := initialStateWithChatCount(count)
		state, err := chatStateFromInitial(initial)
		if err != nil || state.count() != count {
			t.Fatalf("count=%d state=%+v err=%v", count, state, err)
		}
	}
	one, err := chatStateFromInitial(InitialState{Chats: testInitialState().Chats[:1]})
	if err != nil {
		t.Fatal(err)
	}
	model := viewModel{chats: one, options: DefaultOptions()}
	installCommittedTestSender(&model)
	if changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), 100, 30); !changed || exit || model.mode != modeCompose {
		t.Fatalf("one-chat compose changed=%t exit=%t mode=%d", changed, exit, model.mode)
	}
	if !model.composer.insertText("one chat local send") || !submitOutgoingMessage(&model) || one.chats[0].messages[one.chats[0].messageCount-1].text != "one chat local send" {
		t.Fatal("one-chat compose/send failed")
	}

	screen := newObservedScreen(100, 30)
	result := make(chan error, 1)
	go func() {
		result <- runWithDependencies(context.Background(), screen, Input{Options: DefaultOptions()}, "")
	}()
	<-screen.shown
	if text := screenText(screen); !strings.Contains(text, "No chats") {
		t.Fatalf("empty snapshot first frame:\n%s", text)
	}
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	select {
	case <-screen.shown:
		t.Fatal("empty chat Enter caused a redraw")
	default:
	}
	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestInitialStateRejectsOverflowAndDuplicateIDs(t *testing.T) {
	overflowChats := initialStateWithChatCount(ChatWorkingSetCapacity + 1)
	if _, err := chatStateFromInitial(overflowChats); err == nil {
		t.Fatal("chat overflow accepted")
	}
	overflowMessages := testInitialState()
	overflowMessages.Chats[0].Messages = make([]InitialMessage, MaxInitialMessagesPerChat+1)
	if _, err := chatStateFromInitial(overflowMessages); err == nil {
		t.Fatal("message overflow accepted")
	}
	duplicate := testInitialState()
	duplicate.Chats[1].ID = duplicate.Chats[0].ID
	if _, err := chatStateFromInitial(duplicate); err == nil {
		t.Fatal("duplicate chat ID accepted")
	}
	duplicate = testInitialState()
	duplicate.Chats[0].Messages[1].ID = duplicate.Chats[0].Messages[0].ID
	if _, err := chatStateFromInitial(duplicate); err == nil {
		t.Fatal("duplicate message ID accepted")
	}
}

func initialStateWithChatCount(count int) InitialState {
	initial := InitialState{Chats: make([]InitialChat, count)}
	for index := range initial.Chats {
		initial.Chats[index] = InitialChat{ID: fmt.Sprintf("bounded-chat-%02d", index)}
	}
	return initial
}

func TestInitialStableMessageIDCrossesDeferredReplyBoundary(t *testing.T) {
	state, err := chatStateFromInitial(testInitialState())
	if err != nil {
		t.Fatal(err)
	}
	model := viewModel{chats: state, options: DefaultOptions()}
	targetID := state.chats[0].messages[state.chats[0].messageCount-1].id
	if !focusNewestVisibleMessage(&model, 100, 30) || !chooseReplyTarget(&model) {
		t.Fatal("could not select initial message")
	}
	var request SendRequest
	model.send = func(got SendRequest) error {
		request = got
		return errTestSendRejected
	}
	if model.replyTarget.id != targetID || !model.composer.insertText("local reply") || submitOutgoingMessage(&model) {
		t.Fatalf("reply target=%q want=%q", model.replyTarget.id, targetID)
	}
	if request.ReplyToID != string(targetID) || request.ChatID != state.chats[0].id || request.Text != "local reply" {
		t.Fatalf("request=%+v", request)
	}
	if model.replyTarget.id != targetID || model.composer.text() != "local reply" {
		t.Fatalf("rejected reply state target=%+v draft=%q", model.replyTarget, model.composer.text())
	}
	if original, ok := state.findMessageByID(0, targetID); !ok || original.id != targetID {
		t.Fatalf("initial reply target lost: %+v found=%t", original, ok)
	}
}
