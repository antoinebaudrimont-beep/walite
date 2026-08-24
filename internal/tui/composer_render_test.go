package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestDrawNavigationComposerPlaceholder(t *testing.T) {
	screen := initializedSimulationScreen(t, 100, 30)
	model := defaultDemoView()
	draw(screen, &model)
	screen.Show()
	text := screenText(screen)
	if !strings.Contains(text, "Write a message") || !strings.Contains(text, "Enter compose") {
		t.Fatalf("navigation composer missing:\n%s", text)
	}
	if _, _, visible := screen.GetCursor(); visible {
		t.Fatal("navigation cursor is visible")
	}
}

func TestDrawComposeDraftAndCursor(t *testing.T) {
	screen := initializedSimulationScreen(t, 100, 30)
	model := defaultDemoView()
	model.mode = modeCompose
	insertComposerText(t, &model.composer, "hello")
	draw(screen, &model)
	screen.Show()
	text := screenText(screen)
	if !strings.Contains(text, "> hello") || !strings.Contains(text, "Demo synthetic message") || !strings.Contains(text, "Ctrl-E emoji") {
		t.Fatalf("compose frame incomplete:\n%s", text)
	}
	x, y, visible := screen.GetCursor()
	if !visible || y != 26 || x <= paneSeparator(100)+2 || x >= 98 {
		t.Fatalf("cursor=(%d,%d) visible=%t", x, y, visible)
	}
}

func TestDrawSubmittedLocalMessage(t *testing.T) {
	screen := initializedSimulationScreen(t, 100, 30)
	model := defaultDemoView()
	model.mode = modeCompose
	insertComposerText(t, &model.composer, "hello rendered demo")
	if !submitLocalMessage(&model) {
		t.Fatal("send rejected")
	}
	draw(screen, &model)
	screen.Show()
	text := screenText(screen)
	if !strings.Contains(text, "now") || !strings.Contains(text, "hello rendered demo") {
		t.Fatalf("submitted message missing:\n%s", text)
	}
}

func TestDrawNarrowComposeAndSend(t *testing.T) {
	screen := initializedSimulationScreen(t, 60, 20)
	model := defaultDemoView()
	model.mode = modeCompose
	insertComposerText(t, &model.composer, "narrow draft")
	draw(screen, &model)
	screen.Show()
	if text := screenText(screen); !strings.Contains(text, "narrow draft") || !strings.Contains(text, "Enter send") {
		t.Fatalf("narrow draft missing:\n%s", text)
	}
	if !submitLocalMessage(&model) {
		t.Fatal("narrow send rejected")
	}
	draw(screen, &model)
	screen.Show()
	if text := screenText(screen); !strings.Contains(text, "narrow draft") || !strings.Contains(text, "now") {
		t.Fatalf("narrow sent message missing:\n%s", text)
	}
}

func TestRunPreservesDraftAcrossNarrowAndShortResize(t *testing.T) {
	screen := newObservedScreen(100, 20)
	result := make(chan error, 1)
	go func() { result <- runWithPreferences(context.Background(), screen, "") }()

	<-screen.shown
	resizeObservedScreen(t, screen, 40, 6)
	if text := screenText(screen); !strings.Contains(text, "Esc quit") || strings.Contains(text, "Esc cancel") {
		t.Fatalf("short navigation action is wrong:\n%s", text)
	}
	resizeObservedScreen(t, screen, 100, 20)
	injectKeyAndWait(screen, tcell.KeyEnter, 0)
	for _, character := range "héλ🙂" {
		injectRuneAndWait(screen, character)
	}
	resizeObservedScreen(t, screen, 60, 20)
	if text := screenText(screen); !strings.Contains(text, "héλ🙂") || !strings.Contains(text, "Enter send") {
		t.Fatalf("narrow resize lost draft:\n%s", text)
	}
	if _, _, visible := screen.GetCursor(); !visible {
		t.Fatal("narrow compose cursor hidden")
	}
	resizeObservedScreen(t, screen, 40, 6)
	if text := screenText(screen); !strings.Contains(text, "terminal too small") || !strings.Contains(text, "Esc cancel") || strings.Contains(text, "Esc quit") {
		t.Fatalf("short compose action is wrong:\n%s", text)
	}
	resizeObservedScreen(t, screen, 100, 20)
	if text := screenText(screen); !strings.Contains(text, "héλ🙂") || !strings.Contains(text, "Ctrl-E emoji") {
		t.Fatalf("wide resize lost compose state:\n%s", text)
	}

	injectKeyAndWait(screen, tcell.KeyEscape, 0)
	if text := screenText(screen); !strings.Contains(text, "Write a message") {
		t.Fatalf("compose cancel did not restore navigation:\n%s", text)
	}
	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	if err := <-result; err != nil {
		t.Fatalf("Run=%v", err)
	}
}

func injectKeyAndWait(screen *observedScreen, key tcell.Key, character rune) {
	screen.InjectKey(key, character, tcell.ModNone)
	<-screen.shown
}

func injectRuneAndWait(screen *observedScreen, character rune) {
	injectKeyAndWait(screen, tcell.KeyRune, character)
}
