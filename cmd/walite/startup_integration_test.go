package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/antoinebaudrimont-beep/walite/internal/config"
	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/gdamore/tcell/v2"
)

type startupObservedScreen struct {
	tcell.SimulationScreen
	shown    chan struct{}
	finiOnce sync.Once
}

func newStartupObservedScreen(t *testing.T) *startupObservedScreen {
	t.Helper()
	screen := &startupObservedScreen{SimulationScreen: tcell.NewSimulationScreen("UTF-8"), shown: make(chan struct{}, 2)}
	return screen
}

func (screen *startupObservedScreen) Init() error {
	if err := screen.SimulationScreen.Init(); err != nil {
		return err
	}
	screen.SetSize(100, 30)
	return nil
}

func (screen *startupObservedScreen) Show() {
	screen.SimulationScreen.Show()
	screen.shown <- struct{}{}
}

func (screen *startupObservedScreen) Fini() {
	screen.finiOnce.Do(screen.SimulationScreen.Fini)
}

func TestTUIInteractiveBeforeFakeHistoryRelease(t *testing.T) {
	privateHome := t.TempDir()
	t.Setenv("HOME", privateHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(privateHome, "config"))

	scenario, err := newDemoScenario(defaultDemoValues(t))
	if err != nil {
		t.Fatal(err)
	}
	serviceDone := make(chan error, 1)
	go func() { serviceDone <- scenario.core.Run(context.Background()) }()
	first, ok := <-scenario.core.Updates()
	if !ok || first.Kind() != model.UpdateReady {
		t.Fatalf("first service update=%v open=%t", first.Kind(), ok)
	}

	screen := newStartupObservedScreen(t)
	tuiCtx, cancelTUI := context.WithCancel(context.Background())
	defer cancelTUI()
	tuiDone := make(chan error, 1)
	go func() { tuiDone <- tui.Run(tuiCtx, screen) }()
	<-screen.shown
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	<-screen.shown
	if text := startupScreenText(screen); !strings.Contains(text, "Enter send") {
		t.Fatalf("TUI was not interactive before history release:\n%s", text)
	}
	select {
	case <-scenario.historyStart:
		t.Fatal("fake history was released before the interactive TUI frame")
	default:
	}

	close(scenario.historyStart)
	for update := range scenario.core.Updates() {
		if update.Kind() == model.UpdateLive {
			select {
			case <-scenario.gated.releaseFirstHistory:
			default:
				close(scenario.gated.releaseFirstHistory)
			}
		}
	}
	if err := <-serviceDone; err != nil {
		t.Fatal(err)
	}
	cancelTUI()
	if err := <-tuiDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("TUI Run=%v", err)
	}
}

func defaultDemoValues(t *testing.T) config.Values {
	t.Helper()
	values := config.DefaultValues()
	if err := values.Validate(); err != nil {
		t.Fatal(err)
	}
	return values
}

func startupScreenText(screen tcell.SimulationScreen) string {
	cells, width, height := screen.GetContents()
	var text strings.Builder
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			runes := cells[y*width+x].Runes
			if len(runes) == 0 {
				text.WriteByte(' ')
			} else {
				text.WriteRune(runes[0])
			}
		}
		text.WriteByte('\n')
	}
	return text.String()
}
