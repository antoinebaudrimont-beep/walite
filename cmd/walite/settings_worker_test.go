package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/config"
	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/gdamore/tcell/v2"
)

type blockedSettingsStore struct {
	started chan config.UI
	release chan struct{}
	err     error
}

func (s *blockedSettingsStore) Load() (config.UI, error) { return config.DefaultUI(), nil }
func (s *blockedSettingsStore) Save(value config.UI) error {
	s.started <- value
	<-s.release
	return s.err
}

func TestSettingsWorkerBoundedNonblockingSaveAndFailure(t *testing.T) {
	failure := errors.New("synthetic config failure")
	store := &blockedSettingsStore{started: make(chan config.UI, 1), release: make(chan struct{}), err: failure}
	w := newSettingsWorker(store)
	value := tui.DefaultOptions()
	value.SendReadReceipts = false
	if !w.admit(value) {
		t.Fatal("initial save rejected")
	}
	got := <-store.started
	if got.SendReadReceipts {
		t.Fatal("receipt preference lost")
	}
	for i := 0; i < 100; i++ {
		if w.admit(value) {
			t.Fatal("blocked disk write accepted another request")
		}
	}
	close(store.release)
	if err := <-w.results; !errors.Is(err, failure) {
		t.Fatalf("result=%v", err)
	}
	w.stop()
	if w.admit(value) {
		t.Fatal("stopped worker accepted save")
	}
}

func TestSettingsWorkerFinishesAcceptedSaveOnShutdown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	store := config.NewUIFileStore(path)
	w := newSettingsWorker(store)
	options := tui.DefaultOptions()
	options.ConfirmQuit = true
	if !w.admit(options) {
		t.Fatal("save rejected")
	}
	w.stop()
	loaded, err := store.Load()
	if err != nil || !loaded.ConfirmQuit {
		t.Fatalf("saved=%+v err=%v", loaded, err)
	}
}

func TestApplicationSettingsSaveSurvivesRestart(t *testing.T) {
	isolateApplicationFiles(t)
	path := filepath.Join(t.TempDir(), "config.json")
	for launch := 0; launch < 2; launch++ {
		err := runApplication(context.Background(), newStartupObservedScreen(), applicationDependencies{
			configuration: config.NewUIFileStore(path),
			newService:    newOfflineApplicationService,
			runTUI: func(_ context.Context, _ tcell.Screen, input tui.Input) error {
				if input.SaveOptions == nil || input.OptionsResults == nil {
					return errors.New("production config wiring missing")
				}
				if launch == 1 {
					if input.Options.SendReadReceipts || input.Options.ShowTimestamps || !input.Options.ConfirmQuit {
						return errors.New("saved settings lost on restart")
					}
					return nil
				}
				if !input.Options.SendReadReceipts || !input.Options.ShowTimestamps {
					return errors.New("first launch defaults wrong")
				}
				value := input.Options
				value.SendReadReceipts = false
				value.ShowTimestamps = false
				value.ConfirmQuit = true
				if !input.SaveOptions(value) {
					return errors.New("save admission failed")
				}
				return <-input.OptionsResults
			},
		})
		if err != nil {
			t.Fatalf("launch %d: %v", launch, err)
		}
	}
}

func TestSettingsDiskWriteDoesNotBlockTerminal(t *testing.T) {
	isolateApplicationFiles(t)
	store := &blockedSettingsStore{started: make(chan config.UI, 1), release: make(chan struct{})}
	screen := newStartupObservedScreen()
	done := make(chan error, 1)
	go func() {
		done <- runConfiguredTUI(context.Background(), screen, tui.Input{Options: tui.DefaultOptions()}, store, tui.Run)
	}()
	<-screen.shown
	press := func(key tcell.Key, r rune) { screen.InjectKey(key, r, tcell.ModNone); <-screen.shown }
	press(tcell.KeyCtrlP, 0)
	press(tcell.KeyEnter, 0)
	for i := 0; i < 3; i++ {
		press(tcell.KeyDown, 0)
	}
	press(tcell.KeyEnter, 0)
	<-store.started
	if !strings.Contains(startupScreenText(screen), "Saving") {
		t.Fatal("save status missing")
	}
	// Escape closes the pending save view, but the accepted write still finishes.
	press(tcell.KeyEscape, 0)
	press(tcell.KeyCtrlP, 0)
	if !strings.Contains(startupScreenText(screen), "Saving") {
		t.Fatal("TUI unresponsive during save")
	}
	close(store.release)
	<-screen.shown
	if strings.Contains(startupScreenText(screen), "Settings") {
		t.Fatal("successful save did not close panel")
	}
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSavedReadPreferenceOffPreventsMarkReadAndSettingsCauseNoNetwork(t *testing.T) {
	isolateApplicationFiles(t)
	var admissions, networkCalls atomic.Int32
	application := &readWorkerApplication{workerTestApplication: newWorkerTestApplication(), mark: func(context.Context, model.ReadReceiptRequest) error {
		networkCalls.Add(1)
		return nil
	}}
	worker := newReadReceiptWorker(context.Background(), application)
	defer worker.stop()
	store := config.NewUIFileStore(filepath.Join(t.TempDir(), "config.json"))
	screen := newStartupObservedScreen()
	done := make(chan error, 1)
	at := time.Date(2100, 9, 5, 10, 0, 0, 0, time.UTC)
	go func() {
		done <- runConfiguredTUI(context.Background(), screen, tui.Input{
			Options: tui.DefaultOptions(),
			InitialState: tui.InitialState{Chats: []tui.InitialChat{
				{ID: "first@lid", Title: "First", ActivityTime: at},
				{ID: "second@lid", Title: "Second", ActivityTime: at, UnreadCount: 1, Messages: []tui.InitialMessage{
					{ID: "known-unread", SentAt: at, Text: "日本語 🙂", BodyRetained: true},
				}},
			}},
			SendReadReceipt: func(request tui.ReadReceiptRequest) bool { admissions.Add(1); return worker.admit(request) },
		}, store, tui.Run)
	}()
	<-screen.shown
	press := func(key tcell.Key, r rune) { screen.InjectKey(key, r, tcell.ModNone); <-screen.shown }
	press(tcell.KeyCtrlP, 0)
	press(tcell.KeyDown, 0)
	press(tcell.KeyDown, 0)
	press(tcell.KeyRune, ' ')
	press(tcell.KeyDown, 0)
	press(tcell.KeyEnter, 0)
	<-screen.shown // asynchronous save completion
	if admissions.Load() != 0 {
		t.Fatal("opening/changing settings generated receipt")
	}
	press(tcell.KeyRune, 'j')
	if text := startupScreenText(screen); !strings.Contains(text, "> Second") || strings.Contains(text, "> Second (1)") {
		t.Fatalf("local unread not cleared:\n%s", text)
	}
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	worker.stop()
	if admissions.Load() != 0 || networkCalls.Load() != 0 {
		t.Fatalf("admissions=%d MarkRead=%d", admissions.Load(), networkCalls.Load())
	}
	loaded, err := store.Load()
	if err != nil || loaded.SendReadReceipts {
		t.Fatalf("saved preference=%+v err=%v", loaded, err)
	}
}
