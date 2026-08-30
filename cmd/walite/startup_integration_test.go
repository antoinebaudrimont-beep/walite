package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/antoinebaudrimont-beep/walite/internal/config"
	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/gdamore/tcell/v2"
)

type startupObservedScreen struct {
	tcell.SimulationScreen
	shown       chan struct{}
	finiOnce    sync.Once
	initErr     error
	initialized bool
	initCount   atomic.Int64
	finiCount   atomic.Int64
}

func newStartupObservedScreen() *startupObservedScreen {
	return &startupObservedScreen{
		SimulationScreen: tcell.NewSimulationScreen("UTF-8"),
		shown:            make(chan struct{}, 4),
	}
}

func (screen *startupObservedScreen) Init() error {
	if screen.initErr != nil {
		return screen.initErr
	}
	screen.initCount.Add(1)
	if err := screen.SimulationScreen.Init(); err != nil {
		return err
	}
	screen.initialized = true
	screen.SetSize(100, 30)
	return nil
}

func (screen *startupObservedScreen) Show() {
	screen.SimulationScreen.Show()
	screen.shown <- struct{}{}
}

func (screen *startupObservedScreen) Fini() {
	screen.finiCount.Add(1)
	screen.finiOnce.Do(screen.SimulationScreen.Fini)
}

type staticUIStore struct {
	settings config.UI
	loadErr  error
}

func (store staticUIStore) Load() (config.UI, error) { return store.settings, store.loadErr }
func (staticUIStore) Save(config.UI) error           { return nil }

type observedApplicationService struct {
	delegate applicationService
	started  chan struct{}
	done     chan struct{}
}

func observeApplicationService(delegate applicationService) *observedApplicationService {
	return &observedApplicationService{
		delegate: delegate,
		started:  make(chan struct{}),
		done:     make(chan struct{}),
	}
}

func (observed *observedApplicationService) Run(ctx context.Context) error {
	close(observed.started)
	defer close(observed.done)
	return observed.delegate.Run(ctx)
}

func (observed *observedApplicationService) Updates() <-chan model.Update {
	return observed.delegate.Updates()
}

func (observed *observedApplicationService) LiveEvents() <-chan model.LiveEvent {
	return observed.delegate.LiveEvents()
}

func (observed *observedApplicationService) InitialChats(ctx context.Context, limit int) ([]model.Chat, error) {
	return observed.delegate.InitialChats(ctx, limit)
}

func (observed *observedApplicationService) InitialMessages(ctx context.Context, chatID model.ChatID, limit int) ([]model.Message, error) {
	return observed.delegate.InitialMessages(ctx, chatID, limit)
}

func scenarioApplicationService(t *testing.T, scenario demoScenario) applicationService {
	t.Helper()
	chats, err := seedOfflineInitialSnapshot(context.Background(), scenario.store)
	if err != nil {
		t.Fatal(err)
	}
	return &offlineApplicationService{core: scenario.core, store: scenario.store, chats: chats}
}

func TestConfigOptionsMapping(t *testing.T) {
	settings := config.UI{
		Theme:          config.ThemeDefault,
		ShowTimestamps: false,
		ConfirmQuit:    true,
	}
	if got, want := optionsFromConfig(settings), (tui.Options{
		Theme:          tui.ThemeDefault,
		ShowTimestamps: false,
		ConfirmQuit:    true,
	}); got != want {
		t.Fatalf("optionsFromConfig()=%+v want=%+v", got, want)
	}
}

func TestProductionServiceStartsBeforeInteractiveTUIAndIsJoined(t *testing.T) {
	isolateApplicationFiles(t)
	scenario, err := newDemoScenario(defaultDemoValues(t))
	if err != nil {
		t.Fatal(err)
	}
	observedService := observeApplicationService(scenarioApplicationService(t, scenario))
	screen := newStartupObservedScreen()
	applicationDone := make(chan error, 1)
	go func() {
		applicationDone <- runApplication(context.Background(), screen, applicationDependencies{
			configuration: staticUIStore{settings: config.UI{
				Theme:          config.ThemeDefault,
				ShowTimestamps: false,
				ConfirmQuit:    true,
			}},
			newService: func() (applicationService, error) {
				return observedService, nil
			},
			runTUI: tui.Run,
		})
	}()

	<-observedService.started
	<-screen.shown
	if text := startupScreenText(screen); strings.Contains(text, "09:42") || !strings.Contains(text, "Synthetic message one") {
		t.Fatalf("mapped hidden-timestamp option not applied:\n%s", text)
	}
	select {
	case err := <-applicationDone:
		t.Fatalf("application stopped before input: %v", err)
	default:
	}
	select {
	case <-scenario.historyStart:
		t.Fatal("fake history was released before the interactive TUI frame")
	default:
	}

	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	<-screen.shown
	if text := startupScreenText(screen); !strings.Contains(text, "Enter send") {
		t.Fatalf("TUI did not process input while service was alive:\n%s", text)
	}
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if err := <-applicationDone; err != nil {
		t.Fatalf("runApplication=%v", err)
	}
	select {
	case <-observedService.done:
	default:
		t.Fatal("service owner was not joined before application return")
	}
}

func TestConfigLoadFailureDoesNotConstructServiceOrInitializeScreen(t *testing.T) {
	failure := errors.New("synthetic config failure")
	serviceConstructed := false
	screen := newStartupObservedScreen()
	err := runApplication(context.Background(), screen, applicationDependencies{
		configuration: staticUIStore{loadErr: failure},
		newService: func() (applicationService, error) {
			serviceConstructed = true
			return nil, nil
		},
		runTUI: tui.Run,
	})
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "load configuration") {
		t.Fatalf("runApplication=%v", err)
	}
	if serviceConstructed || screen.initialized {
		t.Fatalf("serviceConstructed=%t screenInitialized=%t", serviceConstructed, screen.initialized)
	}
}

func TestServiceConstructionFailureDoesNotInitializeScreen(t *testing.T) {
	failure := errors.New("synthetic construction failure")
	screen := newStartupObservedScreen()
	err := runApplication(context.Background(), screen, applicationDependencies{
		configuration: staticUIStore{settings: config.DefaultUI()},
		newService: func() (applicationService, error) {
			return nil, failure
		},
		runTUI: tui.Run,
	})
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "construct service") {
		t.Fatalf("runApplication=%v", err)
	}
	if screen.initialized {
		t.Fatal("screen initialized after service construction failure")
	}
}

type startupFailingService struct {
	updates chan model.Update
	failure error
	done    chan struct{}
}

func newStartupFailingService(failure error) *startupFailingService {
	return &startupFailingService{updates: make(chan model.Update), failure: failure, done: make(chan struct{})}
}

func (failing *startupFailingService) Run(context.Context) error {
	close(failing.updates)
	close(failing.done)
	return failing.failure
}

func (failing *startupFailingService) Updates() <-chan model.Update { return failing.updates }
func (*startupFailingService) LiveEvents() <-chan model.LiveEvent   { return nil }

func (failing *startupFailingService) InitialChats(context.Context, int) ([]model.Chat, error) {
	return nil, failing.failure
}

func (failing *startupFailingService) InitialMessages(context.Context, model.ChatID, int) ([]model.Message, error) {
	return nil, failing.failure
}

func TestServiceStartFailureDoesNotInitializeScreen(t *testing.T) {
	failure := errors.New("synthetic service start failure")
	failingService := newStartupFailingService(failure)
	screen := newStartupObservedScreen()
	err := runApplication(context.Background(), screen, applicationDependencies{
		configuration: staticUIStore{settings: config.DefaultUI()},
		newService: func() (applicationService, error) {
			return failingService, nil
		},
		runTUI: tui.Run,
	})
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "start service") {
		t.Fatalf("runApplication=%v", err)
	}
	if screen.initialized {
		t.Fatal("screen initialized after service start failure")
	}
	select {
	case <-failingService.done:
	default:
		t.Fatal("failed service owner was not joined")
	}
}

func TestScreenInitializationFailureStopsAndJoinsService(t *testing.T) {
	scenario, err := newDemoScenario(defaultDemoValues(t))
	if err != nil {
		t.Fatal(err)
	}
	observedService := observeApplicationService(scenarioApplicationService(t, scenario))
	failure := errors.New("synthetic screen initialization failure")
	screen := newStartupObservedScreen()
	screen.initErr = failure
	err = runApplication(context.Background(), screen, applicationDependencies{
		configuration: staticUIStore{settings: config.DefaultUI()},
		newService: func() (applicationService, error) {
			return observedService, nil
		},
		runTUI: tui.Run,
	})
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "run tui") {
		t.Fatalf("runApplication=%v", err)
	}
	select {
	case <-observedService.done:
	default:
		t.Fatal("service owner was not joined after TUI initialization failure")
	}
}

func TestApplicationStopsAndJoinsServiceOnEscapeAndContextCancellation(t *testing.T) {
	for _, test := range []struct {
		name string
		stop func(context.CancelFunc, *startupObservedScreen)
		want error
	}{
		{
			name: "escape",
			stop: func(_ context.CancelFunc, screen *startupObservedScreen) {
				screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
			},
		},
		{
			name: "context cancellation",
			stop: func(cancel context.CancelFunc, _ *startupObservedScreen) {
				cancel()
			},
			want: context.Canceled,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			isolateApplicationFiles(t)
			scenario, err := newDemoScenario(defaultDemoValues(t))
			if err != nil {
				t.Fatal(err)
			}
			observedService := observeApplicationService(scenarioApplicationService(t, scenario))
			screen := newStartupObservedScreen()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			applicationDone := make(chan error, 1)
			go func() {
				applicationDone <- runApplication(ctx, screen, applicationDependencies{
					configuration: staticUIStore{settings: config.DefaultUI()},
					newService: func() (applicationService, error) {
						return observedService, nil
					},
					runTUI: tui.Run,
				})
			}()
			<-screen.shown
			test.stop(cancel, screen)
			err = <-applicationDone
			if !errors.Is(err, test.want) || (test.want == nil && err != nil) {
				t.Fatalf("runApplication=%v want=%v", err, test.want)
			}
			select {
			case <-observedService.done:
			default:
				t.Fatal("service owner was not joined")
			}
		})
	}
}

func isolateApplicationFiles(t *testing.T) {
	t.Helper()
	privateHome := t.TempDir()
	t.Setenv("HOME", privateHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(privateHome, "config"))
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
