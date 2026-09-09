package tui

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

// Inspect terminal-cell coordinates, including the complete grapheme stored
// in a leading cell, rather than treating UTF-8 byte offsets as columns.
func assertDirectionalTextAt(t *testing.T, screen tcell.SimulationScreen, x, y int, text string) {
	t.Helper()
	cells, columns, _ := screen.GetContents()
	clusters := uniseg.NewGraphemes(text)
	for clusters.Next() {
		_, _, _, width := screen.GetContent(x, y)
		got := string(cells[y*columns+x].Runes)
		if got != clusters.Str() || width != clusters.Width() {
			t.Fatalf("cell (%d,%d)=%q width=%d; want %q width=%d", x, y, got, width, clusters.Str(), clusters.Width())
		}
		x += width
	}
}

func directionalScreen(t *testing.T, left, right, height int) tcell.SimulationScreen {
	t.Helper()
	screen := initializedSimulationScreen(t, right+3, height)
	for y := 0; y < height; y++ {
		screen.SetContent(left-1, y, '│', nil, tcell.StyleDefault)
		screen.SetContent(right, y, '│', nil, tcell.StyleDefault)
	}
	return screen
}

func assertDirectionalBorders(t *testing.T, screen tcell.SimulationScreen, left, right, height int) {
	t.Helper()
	for y := 0; y < height; y++ {
		assertDirectionalTextAt(t, screen, left-1, y, "│")
		assertDirectionalTextAt(t, screen, right, y, "│")
	}
}

func TestDirectionalShortMessagesSharePaneWithOppositeAnchors(t *testing.T) {
	for _, showTime := range []bool{true, false} {
		view := defaultDemoView()
		view.options.ShowTimestamps = showTime
		chat, _ := view.chats.selectedChat()
		chat.messageCount = 2
		chat.messages[0] = messageView{id: "in", time: "15:16", text: "incoming"}
		chat.messages[1] = messageView{id: "out", time: "15:17", text: "exactly", fromMe: true}
		screen := initializedSimulationScreen(t, 80, 24)
		draw(screen, &view)
		screen.Show()
		left, right := paneSeparator(80)+2, 78
		incoming, outgoing := "incoming", "exactly"
		if showTime {
			incoming = "15:16  " + incoming
			outgoing = "15:17  " + outgoing
		}
		assertDirectionalTextAt(t, screen, left, 3, incoming)
		assertDirectionalTextAt(t, screen, right-uniseg.StringWidth(outgoing), 4, outgoing)
		assertDirectionalTextAt(t, screen, left, 4, " ")
		assertDirectionalTextAt(t, screen, 79, 4, "│")
	}
}

func TestDirectionalUnicodeUsesGraphemeCellWidths(t *testing.T) {
	for _, text := range []string{"Café é 日本語 👋", "❤️ 👍🏽 ✌️ 👨‍👩‍👧‍👦 🇦🇹"} {
		view := defaultDemoView()
		message := messageView{time: "15:17", text: text, fromMe: true}
		const left, right = 3, 63
		screen := directionalScreen(t, left, right, 5)
		if rows := drawMessage(screen, &view, 0, message, left, 1, right, 5, false); rows != 1 {
			t.Fatalf("rows=%d", rows)
		}
		screen.Show()
		bodyLeft := right - uniseg.StringWidth(text)
		assertDirectionalTextAt(t, screen, bodyLeft-prefixWidth, 1, "15:17  ")
		assertDirectionalTextAt(t, screen, bodyLeft, 1, text)
		assertDirectionalBorders(t, screen, left, right, 5)
	}
}

func TestDirectionalWrappedMessagesUseOneBlockOrigin(t *testing.T) {
	for _, fromMe := range []bool{false, true} {
		view := defaultDemoView()
		const left, right = 3, 43
		message := messageView{time: "15:17", text: strings.Repeat("abcdefghijklmnopqrstuvwxyz", 3), fromMe: fromMe}
		bodyWidth, bodyLeft := 33, left+prefixWidth
		if fromMe {
			bodyWidth = 28
			bodyLeft = right - bodyWidth
		}
		screen := directionalScreen(t, left, right, 8)
		rows := drawMessage(screen, &view, 0, message, left, 0, right, 8, false)
		screen.Show()
		wantRows := (len(message.text) + bodyWidth - 1) / bodyWidth
		if rows != wantRows || wrappedMessageLines(message, right-left, true) != rows {
			t.Fatal("drawing and viewport disagree on wrapping")
		}
		for row, offset := 0, 0; offset < len(message.text); row, offset = row+1, offset+bodyWidth {
			line := message.text[offset:min(len(message.text), offset+bodyWidth)]
			assertDirectionalTextAt(t, screen, bodyLeft, row, line)
			if row == 0 {
				assertDirectionalTextAt(t, screen, bodyLeft-prefixWidth, row, "15:17  ")
			} else {
				assertDirectionalTextAt(t, screen, bodyLeft-prefixWidth, row, "       ")
			}
		}
		assertDirectionalBorders(t, screen, left, right, 8)
	}
}

func TestDirectionalMultilinePreservesBlankLinesAndIndentation(t *testing.T) {
	for _, fromMe := range []bool{false, true} {
		for _, lineEnding := range []string{"\n", "\r\n"} {
			view := defaultDemoView()
			lines := []string{"first 👋", "second é", "", "  indented 🙂"}
			message := messageView{time: "15:17", text: strings.Join(lines, lineEnding), fromMe: fromMe}
			const left, right = 3, 63
			screen := directionalScreen(t, left, right, 8)
			if rows := drawMessage(screen, &view, 0, message, left, 0, right, 8, false); rows != len(lines) || wrappedMessageLines(message, right-left, true) != rows {
				t.Fatalf("explicit line rows=%d", rows)
			}
			screen.Show()
			bodyLeft := left + prefixWidth
			if fromMe {
				bodyLeft = right - uniseg.StringWidth(lines[3])
			}
			for y, line := range lines {
				assertDirectionalTextAt(t, screen, bodyLeft, y, line)
			}
			assertDirectionalTextAt(t, screen, left, 2, strings.Repeat(" ", right-left))
			assertDirectionalBorders(t, screen, left, right, 8)
		}
	}
}

func TestDirectionalWordWrapBeforeExplicitNewline(t *testing.T) {
	view := defaultDemoView()
	message := messageView{time: "15:17", text: "alpha beta gamma delta epsilon\n  next 👋", fromMe: true}
	const left, right = 3, 43
	screen := directionalScreen(t, left, right, 8)
	lines := []string{"alpha beta gamma delta", "epsilon", "  next 👋"}
	if rows := drawMessage(screen, &view, 0, message, left, 0, right, 8, false); rows != len(lines) || wrappedMessageLines(message, right-left, true) != rows {
		t.Fatalf("soft/hard wrapping rows=%d", rows)
	}
	screen.Show()
	bodyLeft := right - uniseg.StringWidth(lines[0])
	for row, line := range lines {
		assertDirectionalTextAt(t, screen, bodyLeft, row, line)
	}
	assertDirectionalBorders(t, screen, left, right, 8)
}

func TestDirectionalReplyQuoteAndBodyShareBlock(t *testing.T) {
	for _, fromMe := range []bool{false, true} {
		for _, showTime := range []bool{false, true} {
			view := defaultDemoView()
			view.options.ShowTimestamps = showTime
			original := &view.chats.chats[0].messages[0]
			original.time, original.text = "12:47", "Bus is coming in one minute 👋"
			message := messageView{time: "15:17", text: "reply é 👋", fromMe: fromMe, hasReply: true, replyToID: original.id, replyText: original.text}
			before := message
			const left, right = 3, 73
			screen := directionalScreen(t, left, right, 8)
			if rows := drawMessage(screen, &view, 0, message, left, 0, right, 8, true); rows != 2 {
				t.Fatalf("reply rows=%d", rows)
			}
			screen.Show()
			quote := "↪ " + original.text
			prefix := 0
			if showTime {
				quote = "↪ 12:47 " + original.text
				prefix = prefixWidth
			}
			bodyLeft := left + prefix
			if fromMe {
				bodyLeft = right - uniseg.StringWidth(quote)
			}
			assertDirectionalTextAt(t, screen, bodyLeft, 0, quote)
			assertDirectionalTextAt(t, screen, bodyLeft, 1, message.text)
			if showTime {
				assertDirectionalTextAt(t, screen, bodyLeft-prefix, 0, "15:17  ")
			}
			if attributesAt(screen, left, 0)&tcell.AttrReverse == 0 || attributesAt(screen, bodyLeft, 1)&tcell.AttrReverse == 0 {
				t.Fatal("reply-selection highlight lost")
			}
			if message != before {
				t.Fatal("rendering mutated reply metadata")
			}
			assertDirectionalBorders(t, screen, left, right, 8)
		}
	}
}

func TestDirectionalGroupSenderRemainsWithIncomingBlock(t *testing.T) {
	view := defaultDemoView()
	const left, right = 3, 63
	screen := directionalScreen(t, left, right, 8)
	incoming := messageView{time: "15:16", text: "group body 👋", isGroup: true, senderID: "person", senderName: "Elena Café"}
	outgoing := messageView{time: "15:17", text: "own body", isGroup: true, fromMe: true, senderName: "Do not show"}
	if rows := drawMessage(screen, &view, 0, incoming, left, 0, right, 8, false); rows != 2 {
		t.Fatalf("group rows=%d", rows)
	}
	drawMessage(screen, &view, 0, outgoing, left, 2, right, 8, false)
	screen.Show()
	assertDirectionalTextAt(t, screen, left, 0, "15:16  Elena Café")
	assertDirectionalTextAt(t, screen, left+prefixWidth, 1, incoming.text)
	assertDirectionalTextAt(t, screen, right-prefixWidth-len(outgoing.text), 2, "15:17  own body")
	if attributesAt(screen, left+prefixWidth, 0)&tcell.AttrBold == 0 || strings.Contains(replyScreenText(screen), "Do not show") {
		t.Fatal("group sender style/direction changed")
	}
	assertDirectionalBorders(t, screen, left, right, 8)
}

func TestDirectionalMultilineQuoteRemainsOneRow(t *testing.T) {
	view := defaultDemoView()
	original := &view.chats.chats[0].messages[0]
	original.time, original.text = "12:47", "first 👋\r\nsecond é"
	message := messageView{time: "15:17", text: "reply", fromMe: true, hasReply: true, replyToID: original.id, replyText: original.text}
	const left, right = 3, 63
	screen := directionalScreen(t, left, right, 5)
	if rows := drawMessage(screen, &view, 0, message, left, 0, right, 5, false); rows != 2 {
		t.Fatalf("quote rows=%d", rows)
	}
	screen.Show()
	quote := "↪ 12:47 first 👋 second é"
	bodyLeft := right - uniseg.StringWidth(quote)
	assertDirectionalTextAt(t, screen, bodyLeft, 0, quote)
	assertDirectionalTextAt(t, screen, bodyLeft, 1, "reply")
	if message.replyText != original.text {
		t.Fatal("quote layout changed stored excerpt")
	}
	assertDirectionalBorders(t, screen, left, right, 5)
}

func TestDirectionalUnreadSeparatorStaysFullWidth(t *testing.T) {
	view := defaultDemoView()
	chat, _ := view.chats.selectedChat()
	chat.messageCount = 2
	chat.messages[0] = messageView{id: "before", time: "15:16", text: "incoming"}
	chat.messages[1] = messageView{id: "boundary", time: "15:17", text: "outgoing", fromMe: true}
	view.chatView.unreadBoundary = unreadBoundaryState{valid: true, firstMessageID: "boundary", count: 5}
	const left, right = 3, 63
	screen := directionalScreen(t, left, right, 8)
	drawMessages(screen, &view, 0, chat, 0, 2, left, 0, right, 8)
	screen.Show()
	label := " 5 new messages "
	assertDirectionalTextAt(t, screen, left, 1, "─")
	assertDirectionalTextAt(t, screen, right-1, 1, "─")
	assertDirectionalTextAt(t, screen, left+(right-left-uniseg.StringWidth(label))/2, 1, label)
	assertDirectionalTextAt(t, screen, right-prefixWidth-8, 2, "15:17  outgoing")
	assertDirectionalBorders(t, screen, left, right, 8)
}

func TestDirectionalNarrowFallbackNeverOverwritesBorders(t *testing.T) {
	for _, width := range []int{1, 2, 7, 8, 16, 31} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			view := defaultDemoView()
			const left = 3
			right := left + width
			screen := directionalScreen(t, left, right, 10)
			message := messageView{time: "15:17", text: "A👋é"}
			rows := drawMessage(screen, &view, 0, message, left, 0, right, 10, false)
			screen.Show()
			incoming := replyScreenText(screen)
			message.fromMe = true
			if got := drawMessage(screen, &view, 0, message, left, 0, right, 10, false); got != rows {
				t.Fatal("narrow direction changed wrapping")
			}
			screen.Show()
			if replyScreenText(screen) != incoming {
				t.Fatal("narrow pane did not reclaim full width")
			}
			if width > 8 {
				assertDirectionalTextAt(t, screen, left, 0, "15:17  A👋é")
			}
			if message.text != "A👋é" {
				t.Fatal("narrow rendering changed Unicode data")
			}
			assertDirectionalBorders(t, screen, left, right, 10)
		})
	}
}

func TestDirectionalResizeRecomputesRightAnchor(t *testing.T) {
	view := defaultDemoView()
	chat, _ := view.chats.selectedChat()
	chat.messageCount = 1
	chat.messages[0] = messageView{id: "out", time: "15:17", text: "right 👋", fromMe: true}
	before := *view.chats
	screen := initializedSimulationScreen(t, 80, 24)
	for _, width := range []int{80, 110, 60, 28, 80} {
		screen.SetSize(width, 24)
		screen.Sync()
		clampView(&view, width, 24)
		draw(screen, &view)
		screen.Show()
		left, right, y := 0, width, 4
		if width >= narrowWidth {
			left, right, y = paneSeparator(width)+2, width-2, 3
		}
		start := left
		if right-left >= 32 {
			start = right - uniseg.StringWidth("15:17  right 👋")
		}
		assertDirectionalTextAt(t, screen, start, y, "15:17  right 👋")
		assertMessagePaneMatchesFresh(t, screen, &view)
		if !chatStatesEqual(*view.chats, before) {
			t.Fatal("resize mutated chat data")
		}
	}
}

func TestDirectionalChatSwitchAndShorterReplacementDoNotGhost(t *testing.T) {
	for _, width := range []int{80, 60} {
		view := ghostingTestView(t, false)
		for ci := 0; ci < view.chats.chatCount; ci++ {
			chat := &view.chats.chats[ci]
			for mi := 0; mi < chat.messageCount; mi++ {
				chat.messages[mi].fromMe = true
			}
		}
		screen := initializedSimulationScreen(t, width, 24)
		for cycle := 0; cycle < 3; cycle++ {
			draw(screen, &view)
			screen.Show()
			assertMessagePaneMatchesFresh(t, screen, &view)
			moveChatSelection(&view, 1)
			draw(screen, &view)
			screen.Show()
			assertMessagePaneMatchesFresh(t, screen, &view)
			moveChatSelection(&view, -1)
		}
		chat, _ := view.chats.selectedChat()
		chat.messageCount = 1
		chat.messages[0].text = strings.Repeat("OLD RIGHT TEXT 👋 ", 12)
		view.replySelect = replySelectionState{valid: true, index: 0}
		draw(screen, &view)
		screen.Show()
		chat.messages[0].text = "ok"
		view.replySelect = replySelectionState{}
		draw(screen, &view)
		screen.Show()
		assertMessagePaneMatchesFresh(t, screen, &view)
		right, y := width, 5
		if width >= narrowWidth {
			right, y = width-2, 4
		}
		assertDirectionalTextAt(t, screen, right-2, y, "ok")
		if strings.Contains(replyScreenText(screen), "OLD RIGHT TEXT") {
			t.Fatal("shorter outgoing block left stale text")
		}
	}
}

func TestDirectionalRenderingPreservesInteractionState(t *testing.T) {
	view := defaultDemoView()
	view.terminalWidth, view.terminalHeight = 80, 24
	view.mode = modeCompose
	view.composer.insertText("draft Café 👋")
	view.composer.cursor = 3
	view.replyTarget = replyTarget{valid: true, id: view.chats.chats[0].messages[0].id}
	view.replySelect = replySelectionState{valid: true, index: 1, fromCompose: true}
	view.chatView.scrollOffset = 1
	view.emojiPicker.prepareOpen()
	view.settingsOpen = true
	before, chats := view, *view.chats
	screen := initializedSimulationScreen(t, 80, 24)
	draw(screen, &view)
	screen.Show()
	if !reflect.DeepEqual(view, before) || !chatStatesEqual(*view.chats, chats) {
		t.Fatal("rendering changed state")
	}
}

func TestDirectionalChangedFramesShowOnce(t *testing.T) {
	screen := newObservedScreen(80, 24)
	live := make(chan LiveMessage)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runWithDependencies(ctx, screen, Input{Options: DefaultOptions(), InitialState: testInitialState(), LiveEvents: live}, "")
	}()
	<-screen.shown
	<-screen.eventsStarted
	if screen.showCount.Load() != 1 {
		t.Fatal("initial/idle redraw count")
	}
	screen.InjectKey(tcell.KeyRune, 'j', tcell.ModNone)
	<-screen.shown
	if screen.showCount.Load() != 2 {
		t.Fatal("chat switch redraw count")
	}
	live <- liveTestMessage("project", "directional-send", 60, 60, true, "outgoing", 0)
	<-screen.shown
	if screen.showCount.Load() != 3 {
		t.Fatal("outgoing event redraw count")
	}
	resizeObservedScreen(t, screen, 60, 20)
	if screen.showCount.Load() != 4 {
		t.Fatal("resize redraw count")
	}
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if screen.showCount.Load() != 4 {
		t.Fatal("extra redraw without a changed frame")
	}
}
