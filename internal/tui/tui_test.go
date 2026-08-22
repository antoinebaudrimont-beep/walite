package tui

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/gdamore/tcell/v2"
)

type observedScreen struct {
	tcell.SimulationScreen
	shown     chan struct{}
	finalized chan struct{}
	showOnce  sync.Once
	finiOnce  sync.Once
	initErr   error
}

func newObservedScreen() *observedScreen {
	return &observedScreen{
		SimulationScreen: tcell.NewSimulationScreen("UTF-8"),
		shown:            make(chan struct{}),
		finalized:        make(chan struct{}),
	}
}

func (screen *observedScreen) Init() error {
	if screen.initErr != nil {
		return screen.initErr
	}
	return screen.SimulationScreen.Init()
}

func (screen *observedScreen) Show() {
	screen.SimulationScreen.Show()
	screen.showOnce.Do(func() { close(screen.shown) })
}

func (screen *observedScreen) Fini() {
	screen.SimulationScreen.Fini()
	screen.finiOnce.Do(func() { close(screen.finalized) })
}

func TestRunShowsFirstFrameAndFinalizesOnCancellation(t *testing.T) {
	screen := newObservedScreen()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- Run(ctx, screen) }()

	<-screen.shown
	cells, width, height := screen.GetContents()
	if width < len(title) || height < 1 {
		t.Fatalf("screen size=%dx%d", width, height)
	}
	for index, want := range title {
		if got := cells[index].Runes; len(got) != 1 || got[0] != want {
			t.Fatalf("cell %d=%q want %q", index, string(got), string(want))
		}
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
