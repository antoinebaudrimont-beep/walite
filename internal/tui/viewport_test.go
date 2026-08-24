package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestMessageViewportStartsAtNewest(t *testing.T) {
	model := defaultDemoView()
	_, end := visibleMessageRange(&model, 60, 10)
	chat, _ := model.chats.selectedChat()
	if model.chatView.scrollOffset != 0 || end != chat.messageCount || newerMessageCount(&model, 60, 10) != 0 {
		t.Fatalf("offset=%d end=%d count=%d newer=%d", model.chatView.scrollOffset, end, chat.messageCount,
			newerMessageCount(&model, 60, 10))
	}
}

func TestNewerMessagesIndicatorAppearsOnlyAboveBottom(t *testing.T) {
	screen := initializedSimulationScreen(t, 60, 10)
	model := defaultDemoView()
	model.chatView.scrollOffset = 5
	draw(screen, &model)
	screen.Show()
	if text := screenText(screen); !strings.Contains(text, "↓ 5 newer messages") {
		t.Fatalf("newer indicator missing:\n%s", text)
	}

	model.chatView.reset()
	draw(screen, &model)
	screen.Show()
	if text := screenText(screen); strings.Contains(text, "newer message") {
		t.Fatalf("newer indicator remained at bottom:\n%s", text)
	}
}

func TestResizePreservesValidMessageScrollPosition(t *testing.T) {
	model := defaultDemoView()
	model.chatView.scrollOffset = 3
	for _, size := range [][2]int{{60, 10}, {100, 12}, {50, 10}, {100, 12}} {
		clampView(&model, size[0], size[1])
		if model.chatView.scrollOffset != 3 {
			t.Fatalf("%dx%d offset=%d", size[0], size[1], model.chatView.scrollOffset)
		}
	}
}

func TestControlREntersReplyModeFromScrolledViewport(t *testing.T) {
	model := defaultDemoView()
	width, height := 60, 10
	model.chatView.scrollOffset = 5
	_, end := visibleMessageRange(&model, width, height)
	changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyCtrlR, 0, tcell.ModNone), width, height)
	if !changed || exit || !model.replySelect.valid || model.replySelect.index != end-1 {
		t.Fatalf("changed=%t exit=%t selection=%+v end=%d", changed, exit, model.replySelect, end)
	}
}
