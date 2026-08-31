package tui

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func ghostingTestView(t *testing.T, empty bool) viewModel {
	t.Helper()
	shortMessages := []InitialMessage{liveInitialMessage("b-short", 1, false, "short")}
	if empty {
		shortMessages = nil
	}
	state, err := chatStateFromInitial(InitialState{Chats: []InitialChat{
		{ID: "a", Title: "Long synthetic chat A", ActivityTime: liveTestBase,
			Messages: []InitialMessage{
				liveInitialMessage("a-long", 1, false, strings.Repeat("OLD CHAT A Café 日本語 🐧 e\u0301 ", 12)),
				liveInitialMessage("a-tail", 2, false, "OLD CHAT A trailing text across the previous row"),
			}},
		{ID: "b", Title: "B", ActivityTime: liveTestBase, Messages: shortMessages},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return viewModel{chats: state, options: DefaultOptions()}
}

// Inspect the displayed (front) cells, not GetContent's logical back buffer.
// A fresh render supplies the complete expected rectangle, including blanks,
// styles, trailing columns, and rows with no messages.
func assertMessagePaneMatchesFresh(t *testing.T, screen tcell.SimulationScreen, model *viewModel) {
	t.Helper()
	width, height := screen.Size()
	fresh := initializedSimulationScreen(t, width, height)
	copyModel := *model
	copyChats := *model.chats
	copyModel.chats = &copyChats
	draw(fresh, &copyModel)
	fresh.Show()
	got, _, _ := screen.GetContents()
	want, _, _ := fresh.GetContents()
	left, top, right, bottom := ghostingTestRectangle(width, height)
	for y := top; y < bottom; y++ {
		for x := left; x < right; {
			index := y*width + x
			if string(got[index].Runes) != string(want[index].Runes) || got[index].Style != want[index].Style ||
				string(got[index].Bytes) != string(want[index].Bytes) {
				t.Fatalf("displayed pane cell (%d,%d): got %q/%v, want %q/%v", x, y,
					string(got[index].Runes), got[index].Style, string(want[index].Runes), want[index].Style)
			}
			// tcell v2.13.10 simulation.go simscreen.draw/drawCell (Apache-2.0)
			// advances by glyph width: the front-buffer continuation slots are
			// not painted and can contain old data covered by the wide glyph.
			// Compare every visible leading cell (and all uncovered blanks),
			// not those undefined slots. Only the API semantics are used here.
			_, _, _, cells := fresh.GetContent(x, y)
			x += max(1, cells)
		}
	}
}

func ghostingTestRectangle(width, height int) (left, top, right, bottom int) {
	if height < shortHeight {
		return 0, 0, width, height
	}
	if width < narrowWidth {
		return 0, 2, width, height - 1
	}
	return paneSeparator(width) + 1, 1, width - 1, height - 3
}

func TestMessagePaneGhostingLiveScrollAndResize(t *testing.T) {
	screen := initializedSimulationScreen(t, 80, 24)
	model := defaultDemoView()
	model.chats.chats[1].messageCount = 1
	model.chats.chats[1].messages[0].text = "short"
	draw(screen, &model)
	screen.Show()
	if !applyLiveMessage(&model, liveTestMessage("demo", "new-short", 60, 60, false, "new text", 1)) {
		t.Fatal("selected-chat live event rejected")
	}
	draw(screen, &model)
	screen.Show()
	assertMessagePaneMatchesFresh(t, screen, &model)
	for _, key := range []tcell.Key{tcell.KeyHome, tcell.KeyPgDn, tcell.KeyEnd} {
		if changed, _ := handleKey(&model, tcell.NewEventKey(key, 0, tcell.ModNone), 80, 24); changed {
			draw(screen, &model)
			screen.Show()
		}
		assertMessagePaneMatchesFresh(t, screen, &model)
	}
	if !moveChatSelection(&model, 1) {
		t.Fatal("switch after incoming message failed")
	}
	draw(screen, &model)
	screen.Show()
	assertMessagePaneMatchesFresh(t, screen, &model)
	for _, size := range [][2]int{{60, 12}, {40, 6}, {80, 24}} {
		screen.SetSize(size[0], size[1])
		screen.Sync()
		clampView(&model, size[0], size[1])
		draw(screen, &model)
		screen.Show()
		assertMessagePaneMatchesFresh(t, screen, &model)
		if selected, _ := model.chats.selectedChat(); selected.id != "project" {
			t.Fatal("resize changed selection")
		}
	}
}

func TestMessagePaneGhostingShorterLinesUnreadAndReplyChanges(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {60, 20}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			screen := initializedSimulationScreen(t, size[0], size[1])
			model := defaultDemoView()
			chat, _ := model.chats.selectedChat()
			chat.messageCount = 1
			chat.messages[0].text = strings.Repeat("previous long line ", 9)
			model.chatView.unreadBoundary = unreadBoundaryState{valid: true, firstMessageID: chat.messages[0].id, count: 3}
			model.replySelect = replySelectionState{valid: true, index: 0}
			draw(screen, &model)
			screen.Show()

			// A shorter replacement and removal of the separator/focus must
			// erase tails, unused rows, and their former styles.
			chat.messages[0].text = "short"
			chat.title = "B"
			model.chatView.unreadBoundary = unreadBoundaryState{}
			model.replySelect = replySelectionState{}
			draw(screen, &model)
			screen.Show()
			assertMessagePaneMatchesFresh(t, screen, &model)

			model.mode = modeCompose
			model.composer.insertText("preserved draft")
			model.replyTarget = replyTarget{valid: true, id: chat.messages[0].id}
			draw(screen, &model)
			screen.Show()
			assertMessagePaneMatchesFresh(t, screen, &model)
			model.replyTarget = replyTarget{}
			draw(screen, &model)
			screen.Show()
			assertMessagePaneMatchesFresh(t, screen, &model)
		})
	}
}

func TestMessagePaneClearPreservesModelAndPopupState(t *testing.T) {
	screen := initializedSimulationScreen(t, 80, 24)
	model := defaultDemoView()
	model.terminalWidth, model.terminalHeight = 80, 24
	model.mode = modeCompose
	model.composer.insertText("draft Café 日本語 🐧")
	model.composer.cursor = 5
	model.chatView.scrollOffset = 1
	model.replyTarget = replyTarget{valid: true, id: model.chats.chats[0].messages[0].id}
	model.replySelect = replySelectionState{valid: true, fromCompose: true, index: 1}
	model.emojiPicker.prepareOpen()
	model.settingsOpen = true
	beforeModel, beforeChats := model, *model.chats
	for cycle := 0; cycle < 3; cycle++ {
		draw(screen, &model)
		screen.Show()
		if !reflect.DeepEqual(model, beforeModel) || *model.chats != beforeChats {
			t.Fatal("pane repaint changed chat data, selection, draft, cursor, reply, scroll, or popup state")
		}
		if text := screenText(screen); !strings.Contains(text, "Settings") {
			t.Fatal("pane reset erased the settings overlay")
		}
	}
}

func TestMessagePaneChangedFramesShowOnce(t *testing.T) {
	screen := newObservedScreen(80, 24)
	live := make(chan LiveMessage)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runWithDependencies(ctx, screen, Input{Options: DefaultOptions(), InitialState: testInitialState(), LiveEvents: live}, "")
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	})
	<-screen.shown
	<-screen.eventsStarted
	if screen.showCount.Load() != 1 {
		t.Fatal("idle redraw after initial frame")
	}
	screen.InjectKey(tcell.KeyRune, 'j', tcell.ModNone)
	<-screen.shown
	if screen.showCount.Load() != 2 {
		t.Fatal("chat switch did not show exactly once")
	}
	live <- liveTestMessage("project", "counted-live", 60, 60, false, "incoming", 1)
	<-screen.shown
	if screen.showCount.Load() != 3 {
		t.Fatal("incoming event did not show exactly once")
	}
	resizeObservedScreen(t, screen, 60, 20)
	if screen.showCount.Load() != 4 {
		t.Fatal("resize did not show exactly one changed frame")
	}
	select {
	case <-screen.shown:
		t.Fatal("idle TUI produced another frame")
	default:
	}
}

func TestMessagePaneGhostingChatTransitions(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {100, 30}, {60, 20}} {
		for _, empty := range []bool{false, true} {
			t.Run(fmt.Sprintf("%dx%d/empty=%t", size[0], size[1], empty), func(t *testing.T) {
				screen := initializedSimulationScreen(t, size[0], size[1])
				model := ghostingTestView(t, empty)
				for cycle := 0; cycle < 4; cycle++ {
					draw(screen, &model)
					screen.Show()
					if !moveChatSelection(&model, 1) {
						t.Fatal("select B failed")
					}
					draw(screen, &model)
					screen.Show()
					assertMessagePaneMatchesFresh(t, screen, &model)
					if !moveChatSelection(&model, -1) {
						t.Fatal("select A failed")
					}
				}
			})
		}
	}
}

func TestMessagePaneGhostingRepaintsPhysicallyStaleBlanks(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {60, 20}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			screen := initializedSimulationScreen(t, size[0], size[1])
			model := ghostingTestView(t, false)
			draw(screen, &model)
			screen.Show()
			// Model the reported physical/back-buffer disagreement. Clear already
			// blanks the logical frame; a terminal fragment in a cell considered
			// clean by tcell must nevertheless be erased on the next pane redraw.
			front, width, height := screen.GetContents()
			_, top, right, bottom := ghostingTestRectangle(width, height)
			for _, point := range [][2]int{{right - 1, top}, {right - 1, bottom - 3}} {
				x, y := point[0], point[1]
				if value, _, _ := screen.Get(x, y); value != " " {
					t.Fatalf("fault fixture requires a logical blank at %d,%d: %q", x, y, value)
				}
				front[y*width+x].Runes = []rune{'A'}
				front[y*width+x].Bytes = []byte{'A'}
			}
			if !moveChatSelection(&model, 1) {
				t.Fatal("select B failed")
			}
			draw(screen, &model)
			screen.Show()
			assertMessagePaneMatchesFresh(t, screen, &model)
		})
	}
}
