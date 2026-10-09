package tui

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func TestChatActivationSelectsStableIDAfterReordering(t *testing.T) {
	model := defaultDemoView()
	model.chatView.scrollOffset = 3
	model.narrowPane = narrowPaneChats
	model.chats.chats[2].activityTime = model.chats.chats[0].activityTime.Add(time.Hour)
	model.chats.sortByActivity()
	if model.chats.selectedIndex() != 1 {
		t.Fatal("fixture did not reorder the selected chat")
	}
	if !activateChat(&model, ChatActivationRequest{ChatID: "family"}) {
		t.Fatal("activation rejected")
	}
	selected, ok := model.chats.selectedChat()
	if !ok || selected.id != "family" || selected.unreadCount != 0 || model.chatView.scrollOffset != 0 ||
		model.selectionEpoch != 1 || model.narrowPane != narrowPaneConversation || model.localReadRequest.ChatID != "family" {
		t.Fatalf("selection=%+v viewport=%+v epoch=%d local=%+v", selected, model.chatView, model.selectionEpoch, model.localReadRequest)
	}
	if !model.chatView.unreadBoundary.valid || model.readRequest.ChatID != "family" {
		t.Fatalf("boundary=%+v receipt=%+v", model.chatView.unreadBoundary, model.readRequest)
	}
}

func TestChatActivationRejectsWithoutChangingViewState(t *testing.T) {
	for _, test := range []struct {
		name  string
		id    string
		setup func(*viewModel)
	}{
		{name: "already selected", id: "demo", setup: func(model *viewModel) { model.chats.chats[0].unreadCount = 2 }},
		{name: "unknown", id: "missing@lid"},
		{name: "empty"},
		{name: "composition", id: "project", setup: func(model *viewModel) {
			model.mode = modeCompose
			model.composer.insertText("draft 👋")
			model.composer.moveLeft()
		}},
		{name: "empty composition", id: "project", setup: func(model *viewModel) { model.mode = modeCompose }},
		{name: "file path", id: "project", setup: func(model *viewModel) { model.mode = modeFile; model.composer.insertText("/tmp/input.png") }},
		{name: "retained draft", id: "project", setup: func(model *viewModel) { model.composer.insertText("kept draft") }},
		{name: "pending send", id: "project", setup: func(model *viewModel) { model.sendPending = true }},
		{name: "uncertain send", id: "project", setup: func(model *viewModel) { model.sendUncertain = true }},
		{name: "reply selection", id: "project", setup: func(model *viewModel) { model.replySelect = replySelectionState{valid: true, index: 3} }},
		{name: "reply target", id: "project", setup: func(model *viewModel) { model.replyTarget = replyTarget{valid: true, id: "1"} }},
		{name: "reaction", id: "project", setup: func(model *viewModel) {
			model.reactionTarget = reactionTargetState{valid: true, chatID: "demo", targetID: "1"}
		}},
		{name: "settings", id: "project", setup: openSettings},
		{name: "emoji popup", id: "project", setup: func(model *viewModel) { model.emojiPicker.open = true }},
		{name: "links popup", id: "project", setup: func(model *viewModel) { model.linkPicker.open = true }},
		{name: "quit confirmation", id: "project", setup: func(model *viewModel) { model.quitConfirm = true }},
		{name: "media", id: "project", setup: func(model *viewModel) {
			model.mediaTarget = mediaTargetState{active: true, previewing: true, chatID: "demo", messageID: "1"}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := defaultDemoView()
			model.chatView.scrollOffset = 2
			if test.setup != nil {
				test.setup(&model)
			}
			before := model
			chatsBefore := *model.chats
			if activateChat(&model, ChatActivationRequest{ChatID: test.id}) {
				t.Fatal("unsafe or unnecessary activation accepted")
			}
			if !reflect.DeepEqual(model, before) || !reflect.DeepEqual(*model.chats, chatsBefore) {
				t.Fatal("rejected activation changed application or UI state")
			}
		})
	}
	if activateChat(nil, ChatActivationRequest{ChatID: "project"}) || activateChat(&viewModel{}, ChatActivationRequest{ChatID: "project"}) {
		t.Fatal("empty view accepted activation")
	}
}

func TestRunChatActivationLoadsMessagesAndPreservesKeyboardReadSemantics(t *testing.T) {
	at := time.Date(2100, 1, 2, 3, 4, 5, 0, time.UTC)
	activations := make(chan ChatActivationRequest)
	loads := make(chan ChatLoadRequest, 4)
	results := make(chan ChatLoadResult)
	localReads := make(chan LocalReadRequest, 4)
	receipts := make(chan ReadReceiptRequest, 4)
	screen := newObservedScreen(60, 24)
	done := make(chan error, 1)
	go func() {
		done <- runWithDependencies(context.Background(), screen, Input{
			Options: DefaultOptions(), InitialState: InitialState{Chats: []InitialChat{
				{ID: "first@lid", Title: "First", ActivityTime: at},
				{ID: "middle@lid", Title: "Middle", ActivityTime: at},
				{ID: "target@lid", Title: "Target", UnreadCount: 1, ActivityTime: at},
			}},
			ChatActivations: activations, ChatLoads: results,
			LoadChat:         func(request ChatLoadRequest) bool { loads <- request; return true },
			PersistLocalRead: func(request LocalReadRequest) bool { localReads <- request; return true },
			SendReadReceipt:  func(request ReadReceiptRequest) bool { receipts <- request; return true },
		}, "")
	}()
	<-screen.shown
	if request := <-loads; request.ChatID != "first@lid" {
		t.Fatalf("startup load=%+v", request)
	}
	// No-op requests must not redraw or trigger another selected-chat load.
	for _, id := range []string{"first@lid", "missing@lid", ""} {
		activations <- ChatActivationRequest{ChatID: id}
	}
	// Show the list first: activation should then open the narrow conversation.
	screen.InjectKey(tcell.KeyTAB, 0, tcell.ModNone)
	<-screen.shown
	activations <- ChatActivationRequest{ChatID: "target@lid"}
	request := <-loads
	<-screen.shown
	if request.ChatID != "target@lid" || screen.showCount.Load() != 3 {
		t.Fatalf("activation load=%+v shows=%d", request, screen.showCount.Load())
	}
	if read := <-localReads; read.ChatID != "target@lid" || !read.ActivityTime.Equal(at) {
		t.Fatalf("local read=%+v", read)
	}
	select {
	case receipt := <-receipts:
		t.Fatalf("receipt before cache load=%+v", receipt)
	default:
	}
	results <- ChatLoadResult{ChatID: request.ChatID, Revision: request.Revision, Messages: []InitialMessage{
		{ID: "received", SentAt: at, Text: "Loaded Unicode café 👋", BodyRetained: true},
		{ID: "outgoing", SentAt: at, Text: "Own message", BodyRetained: true, FromMe: true},
	}}
	<-screen.shown
	if receipt := <-receipts; receipt.ChatID != "target@lid" || len(receipt.Messages) != 1 || receipt.Messages[0].MessageID != "received" {
		t.Fatalf("receipt=%+v", receipt)
	}
	if text := screenText(screen); !strings.Contains(text, "Loaded Unicode café 👋") || !strings.Contains(text, "Target") {
		t.Fatalf("loaded conversation not visible:\n%s", text)
	}
	// Closing the optional request channel must not spin or disable keyboard navigation.
	close(activations)
	screen.InjectKey(tcell.KeyRune, 'k', tcell.ModNone)
	if request := <-loads; request.ChatID != "middle@lid" {
		t.Fatalf("keyboard k load=%+v", request)
	}
	<-screen.shown
	screen.InjectKey(tcell.KeyRune, 'j', tcell.ModNone)
	if request := <-loads; request.ChatID != "target@lid" {
		t.Fatalf("keyboard j load=%+v", request)
	}
	<-screen.shown
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(localReads) != 0 || len(receipts) != 0 || len(loads) != 0 {
		t.Fatal("read chat reopened with redundant read or load requests")
	}
}

func TestRunChatActivationDropsCompositionAndPendingSendRequests(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "draft", true: "pending send"}[pending], func(t *testing.T) {
			activations := make(chan ChatActivationRequest)
			loads := make(chan ChatLoadRequest, 4)
			sent := make(chan SendRequest, 1)
			sendResults := make(chan SendResult)
			screen := newObservedScreen(100, 24)
			done := make(chan error, 1)
			go func() {
				done <- runWithDependencies(context.Background(), screen, Input{
					Options: DefaultOptions(), InitialState: testInitialState(), ChatActivations: activations,
					LoadChat:    func(request ChatLoadRequest) bool { loads <- request; return true },
					SendResults: sendResults, Send: func(_ context.Context, request SendRequest) error { sent <- request; return nil },
				}, "")
			}()
			<-screen.shown
			<-loads
			screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
			<-screen.shown
			screen.InjectKey(tcell.KeyRune, 'x', tcell.ModNone)
			<-screen.shown
			if pending {
				screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
				if request := <-sent; request.ChatID != "demo" || request.Text != "x" {
					t.Fatalf("send=%+v", request)
				}
				<-screen.shown
			}
			activations <- ChatActivationRequest{ChatID: "project"}
			if pending {
				sendResults <- SendResult{}
				<-screen.shown
			}
			// A discarded request must not reappear after compose ends.
			screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
			<-screen.shown
			screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if len(loads) != 0 {
				t.Fatal("blocked activation changed chats or reloaded")
			}
		})
	}
}
