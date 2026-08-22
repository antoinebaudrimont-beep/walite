package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/gdamore/tcell/v2"
)

type observedScreen struct {
	tcell.SimulationScreen
	shown     chan struct{}
	finalized chan struct{}
	finiOnce  sync.Once
	initErr   error
	width     int
	height    int
}

func newObservedScreen(size ...int) *observedScreen {
	width, height := 80, 25
	if len(size) == 2 {
		width, height = size[0], size[1]
	}
	return &observedScreen{
		SimulationScreen: tcell.NewSimulationScreen("UTF-8"),
		shown:            make(chan struct{}, 4),
		finalized:        make(chan struct{}),
		width:            width,
		height:           height,
	}
}

func (screen *observedScreen) Init() error {
	if screen.initErr != nil {
		return screen.initErr
	}
	if err := screen.SimulationScreen.Init(); err != nil {
		return err
	}
	screen.SetSize(screen.width, screen.height)
	return nil
}

func (screen *observedScreen) Show() {
	screen.SimulationScreen.Show()
	screen.shown <- struct{}{}
}

func (screen *observedScreen) Fini() {
	screen.SimulationScreen.Fini()
	screen.finiOnce.Do(func() { close(screen.finalized) })
}

func TestRunShowsFirstFrameAndFinalizesOnCancellation(t *testing.T) {
	screen := newObservedScreen(100, 30)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- Run(ctx, screen) }()

	<-screen.shown
	text := screenText(screen)
	for _, want := range []string{"walite", "Demo Chat", "Synthetic message one", "Esc quit"} {
		if !strings.Contains(text, want) {
			t.Fatalf("first frame missing %q:\n%s", want, text)
		}
	}
	cells, width, _ := screen.GetContents()
	separator := width / 3
	if got := cells[2*width+separator].Runes; len(got) != 1 || got[0] != '│' {
		t.Fatalf("pane separator=%q at column %d:\n%s", string(got), separator, text)
	}

	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run=%v", err)
	}
	select {
	case <-screen.finalized:
	default:
		t.Fatal("screen was not finalized")
	}
}

func TestDrawNarrowFallback(t *testing.T) {
	screen := initializedSimulationScreen(t, 60, 20)
	draw(screen, defaultDemoView())
	screen.Show()
	text := screenText(screen)
	for _, want := range []string{"walite", "Demo Chat", "Synthetic message one", "Synthetic reply", "Esc quit"} {
		if !strings.Contains(text, want) {
			t.Fatalf("narrow frame missing %q:\n%s", want, text)
		}
	}
}

func TestDrawShortFallback(t *testing.T) {
	screen := initializedSimulationScreen(t, 40, 6)
	draw(screen, defaultDemoView())
	screen.Show()
	text := screenText(screen)
	for _, want := range []string{"walite", "terminal too small", "Esc quit"} {
		if !strings.Contains(text, want) {
			t.Fatalf("short frame missing %q:\n%s", want, text)
		}
	}
}

func TestRunRedrawsNarrowFallbackAfterResize(t *testing.T) {
	screen := newObservedScreen(100, 30)
	result := make(chan error, 1)
	go func() { result <- Run(context.Background(), screen) }()

	<-screen.shown
	screen.SetSize(60, 20)
	if err := screen.PostEvent(tcell.NewEventResize(60, 20)); err != nil {
		t.Fatal(err)
	}
	<-screen.shown
	text := screenText(screen)
	if !strings.Contains(text, "Synthetic message one") || strings.Contains(text, "┬") {
		t.Fatalf("resized frame is not narrow fallback:\n%s", text)
	}

	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	if err := <-result; err != nil {
		t.Fatalf("Run=%v", err)
	}
}

func TestRunFinalizesOnEscape(t *testing.T) {
	screen := newObservedScreen()
	result := make(chan error, 1)
	go func() { result <- Run(context.Background(), screen) }()

	<-screen.shown
	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	if err := <-result; err != nil {
		t.Fatalf("Run=%v", err)
	}
	select {
	case <-screen.finalized:
	default:
		t.Fatal("screen was not finalized")
	}
}

func TestRunFinalizesOnControlC(t *testing.T) {
	screen := newObservedScreen()
	result := make(chan error, 1)
	go func() { result <- Run(context.Background(), screen) }()

	<-screen.shown
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if err := <-result; err != nil {
		t.Fatalf("Run=%v", err)
	}
	select {
	case <-screen.finalized:
	default:
		t.Fatal("screen was not finalized")
	}
}

func TestRunRejectsInvalidInputAndDoesNotFinalizeFailedInit(t *testing.T) {
	if err := Run(nil, newObservedScreen()); err == nil {
		t.Fatal("nil context accepted")
	}
	if err := Run(context.Background(), nil); err == nil {
		t.Fatal("nil screen accepted")
	}

	screen := newObservedScreen()
	screen.initErr = errors.New("synthetic init failure")
	if err := Run(context.Background(), screen); !errors.Is(err, screen.initErr) {
		t.Fatalf("Run=%v", err)
	}
	select {
	case <-screen.finalized:
		t.Fatal("failed initialization was finalized")
	default:
	}
}

func initializedSimulationScreen(t *testing.T, width, height int) tcell.SimulationScreen {
	t.Helper()
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(width, height)
	t.Cleanup(screen.Fini)
	return screen
}

func screenText(screen tcell.SimulationScreen) string {
	cells, width, height := screen.GetContents()
	var text strings.Builder
	text.Grow((width + 1) * height)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			runes := cells[y*width+x].Runes
			if len(runes) == 0 {
				text.WriteByte(' ')
				continue
			}
			text.WriteRune(runes[0])
		}
		text.WriteByte('\n')
	}
	return text.String()
}
