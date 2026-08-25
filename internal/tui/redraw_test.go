package tui

import (
	"sync"
	"testing"

	"github.com/gdamore/tcell/v2"
)

type redrawMetrics struct {
	clears, shows, cells int64
}

type instrumentedScreen struct {
	tcell.SimulationScreen
	mu      sync.Mutex
	metrics redrawMetrics
}

func newInstrumentedScreen(t *testing.T, width, height int) *instrumentedScreen {
	t.Helper()
	screen := &instrumentedScreen{SimulationScreen: tcell.NewSimulationScreen("UTF-8")}
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(width, height)
	t.Cleanup(screen.Fini)
	return screen
}

func (screen *instrumentedScreen) Clear() {
	width, height := screen.Size()
	screen.mu.Lock()
	screen.metrics.clears++
	screen.metrics.cells += int64(width * height)
	screen.mu.Unlock()
	screen.SimulationScreen.Clear()
}

func (screen *instrumentedScreen) Show() {
	screen.mu.Lock()
	screen.metrics.shows++
	screen.mu.Unlock()
	screen.SimulationScreen.Show()
}

func (screen *instrumentedScreen) SetContent(x, y int, main rune, combining []rune, style tcell.Style) {
	screen.mu.Lock()
	screen.metrics.cells++
	screen.mu.Unlock()
	screen.SimulationScreen.SetContent(x, y, main, combining, style)
}

func (screen *instrumentedScreen) Put(x, y int, value string, style tcell.Style) (string, int) {
	rest, width := screen.SimulationScreen.Put(x, y, value, style)
	screen.mu.Lock()
	screen.metrics.cells += int64(width)
	screen.mu.Unlock()
	return rest, width
}

func (screen *instrumentedScreen) takeMetrics() redrawMetrics {
	screen.mu.Lock()
	defer screen.mu.Unlock()
	metrics := screen.metrics
	screen.metrics = redrawMetrics{}
	return metrics
}

func TestRedrawObservabilityReportsCurrentFullScreenBehavior(t *testing.T) {
	screen := newInstrumentedScreen(t, 100, 30)
	model := defaultDemoView()
	draw(screen, &model)
	screen.Show()
	first := screen.takeMetrics()
	assertFullRedrawMetrics(t, "first-frame", first, 100, 30)

	model.mode = modeCompose
	handleKey(&model, tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone), 100, 30)
	draw(screen, &model)
	screen.Show()
	input := screen.takeMetrics()
	assertFullRedrawMetrics(t, "compose-input", input, 100, 30)

	recordIncomingMessage(&model, model.chats.selectedIndex(), "Synthetic observed live update")
	draw(screen, &model)
	screen.Show()
	live := screen.takeMetrics()
	assertFullRedrawMetrics(t, "live-update", live, 100, 30)

	chat, _ := model.chats.selectedChat()
	chat.messageCount = 0
	for index := 0; index < maxMessages; index++ {
		model.chats.appendMessage(model.chats.selectedIndex(), messageView{time: "10:00", text: "Synthetic history burst"})
	}
	draw(screen, &model)
	screen.Show()
	history := screen.takeMetrics()
	assertFullRedrawMetrics(t, "history-burst-32", history, 100, 30)

	screen.SetSize(60, 20)
	clampView(&model, 60, 20)
	draw(screen, &model)
	screen.Show()
	resize := screen.takeMetrics()
	assertFullRedrawMetrics(t, "resize", resize, 60, 20)

	t.Logf("redraw_metrics first=%+v input=%+v live=%+v history32=%+v resize=%+v", first, input, live, history, resize)
}

func assertFullRedrawMetrics(t *testing.T, operation string, metrics redrawMetrics, width, height int) {
	t.Helper()
	if metrics.shows != 1 || metrics.clears != 1 || metrics.cells < int64(width*height) {
		t.Fatalf("%s metrics=%+v screen=%dx%d", operation, metrics, width, height)
	}
}
