package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestDrawReplySelectionHighlightsLogicalMessage(t *testing.T) {
	screen := initializedSimulationScreen(t, 100, 30)
	model := defaultDemoView()
	if !focusNewestVisibleMessage(&model, 100, 30) {
		t.Fatal("reply selection did not open")
	}
	draw(screen, &model)
	screen.Show()
	text := screenText(screen)
	if !strings.Contains(text, "Enter reply") || !strings.Contains(text, "Demo synthetic message 18") {
		t.Fatalf("reply-selection frame incomplete:\n%s", text)
	}
	row := rowContaining(text, "Demo synthetic message 18")
	if row < 0 || attributesAt(screen, paneSeparator(100)+2, row)&tcell.AttrReverse == 0 {
		t.Fatalf("selected message row=%d is not reversed", row)
	}
	if _, _, visible := screen.GetCursor(); visible {
		t.Fatal("cursor visible during reply selection")
	}
}

func TestDrawComposeReplyPreviewAndSentReference(t *testing.T) {
	screen := initializedSimulationScreen(t, 100, 30)
	model := defaultDemoView()
	if !focusNewestVisibleMessage(&model, 100, 30) || !chooseReplyTarget(&model) {
		t.Fatal("reply target setup failed")
	}
	if !model.composer.insertText("Thanks") {
		t.Fatal("draft insertion failed")
	}
	draw(screen, &model)
	screen.Show()
	text := screenText(screen)
	if !strings.Contains(text, "Replying to:") || !strings.Contains(text, "Demo synthetic message 18") || !strings.Contains(text, "> Thanks") {
		t.Fatalf("reply composer preview incomplete:\n%s", text)
	}
	if !submitLocalMessage(&model) {
		t.Fatal("reply send failed")
	}
	draw(screen, &model)
	screen.Show()
	text = screenText(screen)
	if !strings.Contains(text, "↪ 10:17 Demo synthetic message 18") || !strings.Contains(text, "Thanks") {
		t.Fatalf("sent reply reference missing:\n%s", text)
	}
}

func TestDrawReplyToEvictedOriginalUsesFallback(t *testing.T) {
	screen := initializedSimulationScreen(t, 100, 30)
	model := defaultDemoView()
	chat := &model.chats.chats[0]
	chat.messageCount = maxMessages
	for index := 0; index < maxMessages; index++ {
		chat.messages[index] = messageView{id: messageID(100 + index), time: "old", text: "old-" + twoDigits(index)}
	}
	model.chats.nextMessageID = 1000
	model.mode = modeCompose
	model.replyTarget = replyTarget{valid: true, id: 100}
	if !model.composer.insertText("reply after eviction") || !submitLocalMessage(&model) {
		t.Fatal("reply send failed")
	}
	draw(screen, &model)
	screen.Show()
	if text := screenText(screen); !strings.Contains(text, "original message unavailable") {
		t.Fatalf("evicted fallback missing:\n%s", text)
	}
}

func TestRunPreservesReplySelectionAndTargetAcrossResize(t *testing.T) {
	screen := newObservedScreen(100, 30)
	result := make(chan error, 1)
	go func() { result <- Run(context.Background(), screen) }()

	<-screen.shown
	injectKeyAndWait(screen, tcell.KeyUp, 0)
	injectKeyAndWait(screen, tcell.KeyUp, 0)
	resizeObservedScreen(t, screen, 60, 20)
	resizeObservedScreen(t, screen, 30, 6)
	resizeObservedScreen(t, screen, 100, 30)
	if text := screenText(screen); !strings.Contains(text, "Enter reply") {
		t.Fatalf("resize lost reply selection:\n%s", text)
	}
	injectKeyAndWait(screen, tcell.KeyEnter, 0)
	injectRuneAndWait(screen, 'O')
	injectRuneAndWait(screen, 'K')
	resizeObservedScreen(t, screen, 60, 20)
	if text := screenText(screen); !strings.Contains(text, "Replying to:") || !strings.Contains(text, "OK") {
		t.Fatalf("resize lost reply target or draft:\n%s", text)
	}
	injectKeyAndWait(screen, tcell.KeyEscape, 0)
	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	if err := <-result; err != nil {
		t.Fatalf("Run=%v", err)
	}
}

func TestDrawComposeReplySelectionKeepsDraftButHidesCursor(t *testing.T) {
	screen := initializedSimulationScreen(t, 100, 24)
	model := defaultDemoView()
	model.mode = modeCompose
	if !model.composer.insertText("preserved draft") || !focusReplyFromCompose(&model, 100, 24) {
		t.Fatal("compose reply selection setup failed")
	}
	draw(screen, &model)
	screen.Show()
	text := screenText(screen)
	if !strings.Contains(text, "preserved draft") || !strings.Contains(text, "Enter reply") || !strings.Contains(text, "Esc cancel") {
		t.Fatalf("compose reply selection frame incomplete:\n%s", text)
	}
	if _, _, visible := screen.GetCursor(); visible {
		t.Fatal("composer cursor visible during message selection")
	}
}
