package tui

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/config"
	"github.com/gdamore/tcell/v2"
)

func BenchmarkFirstFrameCold(b *testing.B) {
	root := b.TempDir()
	benchmarkFirstFrame(b, func(iteration int) (config.UIStore, ChatStateStore) {
		directory := filepath.Join(root, strconv.Itoa(iteration))
		return config.NewUIFileStore(filepath.Join(directory, "config.json")),
			newFileChatStateStore(filepath.Join(directory, "state.json"))
	})
}

func BenchmarkFirstFrameWarm(b *testing.B) {
	root := b.TempDir()
	configurationStore := config.NewUIFileStore(filepath.Join(root, "config.json"))
	chatStore := newFileChatStateStore(filepath.Join(root, "state.json"))
	if err := configurationStore.Save(config.DefaultUI()); err != nil {
		b.Fatal(err)
	}
	if err := chatStore.Save(newDemoChatState()); err != nil {
		b.Fatal(err)
	}
	benchmarkFirstFrame(b, func(int) (config.UIStore, ChatStateStore) {
		return configurationStore, chatStore
	})
}

func benchmarkFirstFrame(b *testing.B, stores func(int) (config.UIStore, ChatStateStore)) {
	b.ReportAllocs()
	for iteration := 0; iteration < b.N; iteration++ {
		b.StopTimer()
		configurationStore, chatStore := stores(iteration)
		screen := newObservedScreen(100, 30)
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		b.StartTimer()
		go func() {
			result <- runWithDependencies(ctx, screen, configurationStore, "", chatStore)
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
