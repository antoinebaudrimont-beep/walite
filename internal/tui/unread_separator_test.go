package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestUnreadChatSelectionCapturesSeparatorBeforeClearingBadge(t *testing.T) {
	model := defaultDemoView()
	model.send = func(SendRequest) error { return nil }
	chat := &model.chats.chats[1]
	wantBoundaryID := chat.messages[chat.messageCount-int(chat.unreadCount)].id

	if changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), 100, 30); !changed || exit {
		t.Fatalf("select changed=%t exit=%t", changed, exit)
	}
	if model.chats.chats[1].unreadCount != 0 || !model.chatView.unreadBoundary.valid ||
		model.chatView.unreadBoundary.firstMessageID != wantBoundaryID || model.chatView.unreadBoundary.count != 3 {
		t.Fatalf("unread=%d boundary=%+v", model.chats.chats[1].unreadCount, model.chatView.unreadBoundary)
	}

	screen := initializedSimulationScreen(t, 100, 30)
	draw(screen, &model)
	screen.Show()
	text := screenText(screen)
	separatorRow := rowContaining(text, "3 new messages")
	firstUnreadRow := rowContaining(text, "Project synthetic message 13")
	if separatorRow < 0 || firstUnreadRow != separatorRow+1 {
		t.Fatalf("separator row=%d first unread row=%d:\n%s", separatorRow, firstUnreadRow, text)
	}
}

func TestLocalSendPreservesExistingSeparatorAndCreatesNoFakeOne(t *testing.T) {
	model := defaultDemoView()
	model.send = func(SendRequest) error { return nil }
	model.mode = modeCompose
	if !model.composer.insertText("read chat local send") || !submitOutgoingMessage(&model) {
		t.Fatal("read chat send failed")
	}
	if model.chatView.unreadBoundary.valid {
		t.Fatalf("read chat gained boundary=%+v", model.chatView.unreadBoundary)
	}

	model = defaultDemoView()
	model.send = func(SendRequest) error { return nil }
	if !moveChatSelection(&model, 1) {
		t.Fatal("unread chat selection failed")
	}
	want := model.chatView.unreadBoundary
	model.chatView.scrollOffset = 2
	jumpMessageViewport(&model, 100, 30, false)
	if model.chatView.unreadBoundary != want {
		t.Fatalf("End cleared boundary=%+v want=%+v", model.chatView.unreadBoundary, want)
	}
	model.mode = modeCompose
	if !model.composer.insertText("local after unread") || !submitOutgoingMessage(&model) {
		t.Fatal("unread chat send failed")
	}
	if model.chatView.unreadBoundary != want {
		t.Fatalf("boundary moved from %+v to %+v", want, model.chatView.unreadBoundary)
	}
}

func TestUnreadSeparatorSurvivesResizeAndClearsAfterLeavingChat(t *testing.T) {
	model := defaultDemoView()
	if !moveChatSelection(&model, 1) {
		t.Fatal("Project Room selection failed")
	}
	want := model.chatView.unreadBoundary
	for _, size := range [][2]int{{120, 40}, {60, 20}, {40, 8}, {120, 40}} {
		clampView(&model, size[0], size[1])
		if model.chatView.unreadBoundary != want {
			t.Fatalf("%dx%d boundary=%+v want=%+v", size[0], size[1], model.chatView.unreadBoundary, want)
		}
		if size[1] >= shortHeight {
			screen := initializedSimulationScreen(t, size[0], size[1])
			draw(screen, &model)
			screen.Show()
			start, end := visibleMessageRange(&model, size[0], size[1])
			chat, _ := model.chats.selectedChat()
			boundaryVisible := false
			for index := start; index < end; index++ {
				if chat.messages[index].id == want.firstMessageID {
					boundaryVisible = true
					break
				}
			}
			if text := screenText(screen); boundaryVisible != strings.Contains(text, "3 new messages") {
				t.Fatalf("%dx%d boundaryVisible=%t:\n%s", size[0], size[1], boundaryVisible, text)
			}
		}
	}

	if !moveChatSelection(&model, 1) || model.chatView.unreadBoundary.count != 1 {
		t.Fatalf("Family Demo boundary=%+v", model.chatView.unreadBoundary)
	}
	if !moveChatSelection(&model, -1) {
		t.Fatal("return to Project Room failed")
	}
	if model.chatView.unreadBoundary.valid {
		t.Fatalf("return to read chat recreated boundary=%+v", model.chatView.unreadBoundary)
	}
}

func TestUnreadSeparatorStateIsTransient(t *testing.T) {
	model := defaultDemoView()
	if !moveChatSelection(&model, 1) {
		t.Fatal("unread chat selection failed")
	}
	if !model.chatView.unreadBoundary.valid {
		t.Fatal("separator setup failed")
	}
	wantState := *model.chats
	for _, size := range [][2]int{{100, 30}, {60, 20}, {100, 30}} {
		clampView(&model, size[0], size[1])
	}
	if *model.chats != wantState {
		t.Fatal("transient separator changed chat data")
	}
}
