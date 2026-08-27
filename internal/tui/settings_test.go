package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestControlPOpensAndEscapeClosesSettings(t *testing.T) {
	model := defaultDemoView()
	changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyCtrlP, 0, tcell.ModNone), 100, 30)
	if !changed || exit || !model.settingsOpen {
		t.Fatalf("open changed=%t exit=%t open=%t", changed, exit, model.settingsOpen)
	}
	changed, exit = handleKey(&model, tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), 100, 30)
	if !changed || exit || model.settingsOpen {
		t.Fatalf("close changed=%t exit=%t open=%t", changed, exit, model.settingsOpen)
	}
}

func TestSettingsFromComposePreservesDraftAndMode(t *testing.T) {
	model := defaultDemoView()
	model.mode = modeCompose
	if !model.composer.insertText("unfinished draft") {
		t.Fatal("draft setup failed")
	}
	if changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyCtrlP, 0, tcell.ModNone), 100, 30); !changed || exit {
		t.Fatalf("open changed=%t exit=%t", changed, exit)
	}
	if changed, exit := handleKey(&model, tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), 100, 30); !changed || exit {
		t.Fatalf("close changed=%t exit=%t", changed, exit)
	}
	if model.mode != modeCompose || model.composer.text() != "unfinished draft" {
		t.Fatalf("mode=%d draft=%q", model.mode, model.composer.text())
	}
}

func TestSettingsPopupDisplaysConfiguration(t *testing.T) {
	screen := initializedSimulationScreen(t, 100, 30)
	model := defaultDemoView()
	model.options = Options{
		Theme:          ThemeDefault,
		ShowTimestamps: false,
		ConfirmQuit:    true,
	}
	model.settingsOpen = true
	draw(screen, &model)
	screen.Show()
	text := screenText(screen)
	for _, want := range []string{"Settings", "Theme: default", "Time: disabled", "Confirm quit: yes", "Esc close"} {
		if !strings.Contains(text, want) {
			t.Fatalf("settings popup missing %q:\n%s", want, text)
		}
	}
}

func TestResizePreservesSettingsPopupState(t *testing.T) {
	screen := initializedSimulationScreen(t, 100, 30)
	model := defaultDemoView()
	model.settingsOpen = true
	for _, size := range [][2]int{{100, 30}, {60, 20}, {30, 6}, {100, 30}} {
		screen.SetSize(size[0], size[1])
		clampView(&model, size[0], size[1])
		draw(screen, &model)
		screen.Show()
		if !model.settingsOpen {
			t.Fatalf("%dx%d resize closed settings", size[0], size[1])
		}
		if text := screenText(screen); !strings.Contains(text, "Settings") || !strings.Contains(text, "Theme: default") {
			t.Fatalf("%dx%d settings content missing:\n%s", size[0], size[1], text)
		}
	}
}

func TestComposeFooterIncludesSettingsShortcut(t *testing.T) {
	screen := initializedSimulationScreen(t, 100, 30)
	model := defaultDemoView()
	model.mode = modeCompose
	draw(screen, &model)
	screen.Show()
	if text := screenText(screen); !strings.Contains(text, "↑/↓ scroll") || !strings.Contains(text, "Ctrl-P settings") || !strings.Contains(text, "Esc cancel") {
		t.Fatalf("compose footer missing settings shortcut:\n%s", text)
	}
}

func TestNavigationFooterIncludesSettingsShortcut(t *testing.T) {
	model := defaultDemoView()
	want := "↑/↓ scroll  j/k chats  Enter compose  Ctrl-P settings  Esc quit"
	if got := navigationFooter(&model, false); got != want {
		t.Fatalf("navigation footer=%q want=%q", got, want)
	}
}

func TestPolishedFooterModesAndNarrowFallbacks(t *testing.T) {
	model := defaultDemoView()
	if got := navigationFooter(&model, false); got != "↑/↓ scroll  j/k chats  Enter compose  Ctrl-P settings  Esc quit" {
		t.Fatalf("navigation footer=%q", got)
	}
	model.mode = modeCompose
	if got := narrowComposeFooter(70); got != "↑/↓ scroll  Enter send  Ctrl-R  Ctrl-E  Ctrl-P  Esc cancel" {
		t.Fatalf("narrow compose footer=%q", got)
	}
	if got := narrowComposeFooter(40); got != "↑/↓  Enter send  Ctrl-R  Ctrl-E  Esc" {
		t.Fatalf("compact compose footer=%q", got)
	}
	model.replySelect = replySelectionState{valid: true}
	if got := navigationFooter(&model, false); got != "↑/↓ message  Enter reply  Esc cancel" {
		t.Fatalf("reply footer=%q", got)
	}
	if got := narrowNavigationFooter(&model, 24); got != "↑/↓  Enter  Esc cancel" {
		t.Fatalf("compact reply footer=%q", got)
	}
}

func TestRunUsesSuppliedOptions(t *testing.T) {
	screen := newObservedScreen(100, 30)
	result := make(chan error, 1)
	go func() {
		result <- runWithDependencies(context.Background(), screen, Options{
			Theme:          ThemeDefault,
			ShowTimestamps: false,
			ConfirmQuit:    true,
		}, "")
	}()

	<-screen.shown
	screen.InjectKey(tcell.KeyCtrlP, 0, tcell.ModNone)
	<-screen.shown
	text := screenText(screen)
	if !strings.Contains(text, "Time: disabled") || !strings.Contains(text, "Confirm quit: yes") {
		t.Fatalf("supplied options missing from popup:\n%s", text)
	}
	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	<-screen.shown
	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	if err := <-result; err != nil {
		t.Fatalf("runWithDependencies=%v", err)
	}
}

func TestRunAppliesHiddenTimestampOptionToMessages(t *testing.T) {
	screen := newObservedScreen(100, 30)
	result := make(chan error, 1)
	go func() {
		result <- runWithDependencies(context.Background(), screen, Options{
			Theme:          ThemeDefault,
			ShowTimestamps: false,
		}, "")
	}()

	<-screen.shown
	text := screenText(screen)
	if strings.Contains(text, "09:42") || !strings.Contains(text, "Synthetic message one") {
		t.Fatalf("timestamp option not applied to first frame:\n%s", text)
	}
	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	if err := <-result; err != nil {
		t.Fatalf("runWithDependencies=%v", err)
	}
}
