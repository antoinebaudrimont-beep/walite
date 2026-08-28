package tui

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func BenchmarkFirstFrame(b *testing.B) {
	benchmarkFirstFrame(b)
}

func benchmarkFirstFrame(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		b.StopTimer()
		screen := newObservedScreen(100, 30)
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		b.StartTimer()
		go func() {
			result <- runWithDependencies(ctx, screen, Input{Options: DefaultOptions(), InitialState: testInitialState()}, "")
		}()
		<-screen.shown
		b.StopTimer()
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			b.Fatalf("Run=%v", err)
		}
	}
}

func BenchmarkComposeASCIIInsertion(b *testing.B) {
	var composer composerState
	b.ReportAllocs()
	for iteration := 0; iteration < b.N; iteration++ {
		composer.insert('a')
		composer.clear()
	}
}

func BenchmarkComposeUTF8Insertion(b *testing.B) {
	var composer composerState
	b.ReportAllocs()
	for iteration := 0; iteration < b.N; iteration++ {
		composer.insert('é')
		composer.clear()
	}
}

func BenchmarkComposeEmojiInsertion(b *testing.B) {
	var composer composerState
	b.ReportAllocs()
	for iteration := 0; iteration < b.N; iteration++ {
		composer.insertText("👨‍👩‍👧‍👦")
		composer.clear()
	}
}

func BenchmarkComposeCursorMovement(b *testing.B) {
	var composer composerState
	composer.insertText("Café 🙂 cursor")
	b.ReportAllocs()
	for iteration := 0; iteration < b.N; iteration++ {
		if iteration%2 == 0 {
			composer.moveLeft()
		} else {
			composer.moveRight()
		}
	}
}

func BenchmarkComposeBackspaceAndRestore(b *testing.B) {
	var composer composerState
	composer.insertText("👍🏽")
	b.ReportAllocs()
	for iteration := 0; iteration < b.N; iteration++ {
		composer.backspace()
		composer.insertText("👍🏽")
	}
	b.ReportMetric(2, "mutations/op")
}

func BenchmarkComposeInputRedraw(b *testing.B) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		b.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(100, 30)
	model := defaultDemoView()
	model.mode = modeCompose
	event := tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone)
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		handleKey(&model, event, 100, 30)
		draw(screen, &model)
		screen.Show()
		model.composer.backspace()
	}
}

func BenchmarkExistingChatLiveMessageRedraw(b *testing.B) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		b.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(100, 30)
	b.ReportAllocs()
	for iteration := 0; iteration < b.N; iteration++ {
		b.StopTimer()
		model := defaultDemoView()
		model.terminalWidth, model.terminalHeight = 100, 30
		event := LiveMessage{
			ChatID: "demo", MessageID: "benchmark-live-" + strconv.Itoa(iteration),
			SentAt: liveTestBase.Add(time.Duration(iteration) * time.Second), Text: "Synthetic existing-chat live benchmark",
			BodyRetained: true, UnreadCount: 1, ActivityTime: liveTestBase.Add(time.Duration(iteration) * time.Second),
		}
		b.StartTimer()
		applyLiveMessage(&model, event)
		draw(screen, &model)
		screen.Show()
	}
}

func BenchmarkBackgroundChatPromotionRedraw(b *testing.B) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		b.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(100, 30)
	b.ReportAllocs()
	for iteration := 0; iteration < b.N; iteration++ {
		b.StopTimer()
		model := defaultDemoView()
		model.terminalWidth, model.terminalHeight = 100, 30
		activity := liveTestBase.Add(time.Duration(iteration+1) * time.Second)
		event := LiveMessage{
			ChatID: "contact", MessageID: "benchmark-promotion-" + strconv.Itoa(iteration), SentAt: activity,
			Text: "Synthetic background promotion benchmark", BodyRetained: true, UnreadCount: 13, ActivityTime: activity,
		}
		b.StartTimer()
		applyLiveMessage(&model, event)
		draw(screen, &model)
		screen.Show()
	}
}

func TestComposeInputLatencyDistribution(t *testing.T) {
	const samples = 1000
	screen := initializedSimulationScreen(t, 100, 30)
	model := defaultDemoView()
	model.mode = modeCompose
	event := tcell.NewEventKey(tcell.KeyRune, 'é', tcell.ModNone)
	var durations [samples]time.Duration
	for index := range durations {
		started := time.Now()
		handleKey(&model, event, 100, 30)
		draw(screen, &model)
		screen.Show()
		durations[index] = time.Since(started)
		model.composer.backspace()
	}
	sort.Slice(durations[:], func(left, right int) bool { return durations[left] < durations[right] })
	t.Logf("compose_input_samples=%d p50=%s p95=%s max=%s", samples, durations[samples/2], durations[samples*95/100], durations[samples-1])
}
