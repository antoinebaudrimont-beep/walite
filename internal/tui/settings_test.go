package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antoinebaudrimont-beep/walite/internal/config"
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
	model.configuration = config.UI{
		Theme:          config.ThemeDefault,
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
	if text := screenText(screen); !strings.Contains(text, "Ctrl-P settings") || !strings.Contains(text, "Esc cancel") {
		t.Fatalf("compose footer missing settings shortcut:\n%s", text)
	}
}

func TestNavigationFooterIncludesSettingsShortcut(t *testing.T) {
	model := defaultDemoView()
	want := "↑/↓ messages  j/k chats  Enter compose  Ctrl-P settings  Esc quit"
	if got := navigationFooter(&model, false); got != want {
		t.Fatalf("navigation footer=%q want=%q", got, want)
	}
}

func TestRunLoadsConfigurationBeforeStartingTUI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "walite", "config.json")
	configurationStore := config.NewUIFileStore(path)
	settings := config.UI{Theme: config.ThemeDefault, ShowTimestamps: false, ConfirmQuit: true}
	if err := configurationStore.Save(settings); err != nil {
		t.Fatal(err)
	}
	screen := newObservedScreen(100, 30)
	result := make(chan error, 1)
	go func() {
		result <- runWithDependencies(context.Background(), screen, configurationStore, "", nil)
	}()

	<-screen.shown
	screen.InjectKey(tcell.KeyCtrlP, 0, tcell.ModNone)
	<-screen.shown
	text := screenText(screen)
	if !strings.Contains(text, "Time: disabled") || !strings.Contains(text, "Confirm quit: yes") {
		t.Fatalf("loaded configuration missing from popup:\n%s", text)
	}
	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	<-screen.shown
	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	if err := <-result; err != nil {
		t.Fatalf("runWithDependencies=%v", err)
	}
}

func TestFirstStartupCreatesDefaultConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "walite", "config.json")
	configurationStore := config.NewUIFileStore(path)
	screen := newObservedScreen(100, 30)
	result := make(chan error, 1)
	go func() {
		result <- runWithDependencies(context.Background(), screen, configurationStore, "", nil)
	}()

	<-screen.shown
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config was not created before first frame: %v", err)
	}
	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	if err := <-result; err != nil {
		t.Fatalf("runWithDependencies=%v", err)
	}
	loaded, err := configurationStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded != config.DefaultUI() {
		t.Fatalf("created configuration=%+v want=%+v", loaded, config.DefaultUI())
	}
}
