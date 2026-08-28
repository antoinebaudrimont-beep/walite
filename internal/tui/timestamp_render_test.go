package tui

import (
	"strings"
	"testing"
)

func TestMessageTimestampsUseFixedPrefixAndAlignedContinuation(t *testing.T) {
	screen := initializedSimulationScreen(t, 30, 8)
	model := defaultDemoView()
	model.options.ShowTimestamps = true

	first := messageView{time: "now", text: "abcdefghijklmno"}
	second := messageView{time: "10:14", text: "second"}
	firstRows := drawMessage(screen, &model, 0, first, 0, 0, 18, 8, false)
	drawMessage(screen, &model, 0, second, 0, firstRows, 18, 8, false)
	screen.Show()
	rows := strings.Split(screenText(screen), "\n")
	if !strings.HasPrefix(rows[0], "now    abcdefghijk") {
		t.Fatalf("first timestamp/body alignment=%q", rows[0])
	}
	if !strings.HasPrefix(rows[1], strings.Repeat(" ", prefixWidth)+"lmno") {
		t.Fatalf("continuation alignment=%q", rows[1])
	}
	if !strings.HasPrefix(rows[firstRows], "10:14  second") {
		t.Fatalf("second timestamp/body alignment=%q", rows[firstRows])
	}
}

func TestHiddenTimestampsReclaimWidthAndChangeWrapping(t *testing.T) {
	screen := initializedSimulationScreen(t, 30, 8)
	model := defaultDemoView()
	model.options.ShowTimestamps = false
	message := messageView{time: "10:14", text: strings.Repeat("x", 30)}

	if shown, hidden := wrappedMessageLines(message, 18, true), wrappedMessageLines(message, 18, false); shown != 3 || hidden != 2 {
		t.Fatalf("wrapped lines shown=%d hidden=%d", shown, hidden)
	}
	if rows := drawMessage(screen, &model, 0, message, 0, 0, 18, 8, false); rows != 2 {
		t.Fatalf("hidden timestamp rows=%d", rows)
	}
	screen.Show()
	text := screenText(screen)
	if strings.Contains(text, "10:14") || !strings.HasPrefix(text, strings.Repeat("x", 18)) {
		t.Fatalf("hidden timestamp did not reclaim row:\n%s", text)
	}
}

func TestReplyAlignmentHonorsTimestampConfiguration(t *testing.T) {
	for _, show := range []bool{true, false} {
		screen := initializedSimulationScreen(t, 50, 8)
		model := defaultDemoView()
		model.options.ShowTimestamps = show
		original := model.chats.chats[0].messages[0]
		reply := messageView{time: "10:20", text: "reply body", hasReply: true, replyToID: original.id}
		drawMessage(screen, &model, 0, reply, 0, 0, 48, 8, false)
		screen.Show()
		rows := strings.Split(screenText(screen), "\n")
		if show {
			if !strings.HasPrefix(rows[0], "10:20  ↪ 09:42 Synthetic message one") ||
				!strings.HasPrefix(rows[1], strings.Repeat(" ", prefixWidth)+"reply body") {
				t.Fatalf("timestamped reply alignment:\n%s", screenText(screen))
			}
		} else if !strings.HasPrefix(rows[0], "↪ Synthetic message one") ||
			!strings.HasPrefix(rows[1], "reply body") || strings.Contains(screenText(screen), "09:42") || strings.Contains(screenText(screen), "10:20") {
			t.Fatalf("timestamp-free reply alignment:\n%s", screenText(screen))
		}
	}
}

func TestTimestampConfigurationAffectsViewportCalculations(t *testing.T) {
	model := defaultDemoView()
	chat := &model.chats.chats[0]
	chat.messageCount = 8
	for index := 0; index < chat.messageCount; index++ {
		chat.messages[index] = messageView{id: testMessageID(index + 1), time: "10:14", text: strings.Repeat("x", 40)}
	}
	width, height := 70, 12
	model.options.ShowTimestamps = true
	shownStart, shownEnd := visibleMessageRange(&model, width, height)
	model.options.ShowTimestamps = false
	hiddenStart, hiddenEnd := visibleMessageRange(&model, width, height)
	if shownEnd != chat.messageCount || hiddenEnd != chat.messageCount || hiddenStart >= shownStart {
		t.Fatalf("shown=%d:%d hidden=%d:%d", shownStart, shownEnd, hiddenStart, hiddenEnd)
	}
	_, rows := conversationViewport(&model, width, height)
	used := 0
	for index := hiddenStart; index < hiddenEnd; index++ {
		used += renderedMessageLines(&model, chat.messages[index], width-paneSeparator(width)-4)
	}
	if used > rows {
		t.Fatalf("hidden viewport uses %d rows, limit %d", used, rows)
	}
}
