package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/gdamore/tcell/v2"
)

func TestLinuxNotificationApplicationOwnsActionsAndActivatesRealTUI(t *testing.T) {
	isolateApplicationFiles(t)
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	fixture := newLinuxActionFixture()
	started := make(chan *linuxNotificationActions, 1)
	capabilities := platformCapabilities{notifier: fixture.backend(), newLinuxNotificationActions: func(ctx context.Context) (*linuxNotificationActions, error) {
		actions, err := newLinuxNotificationActions(ctx, fixture.backend(), fixture.options())
		if err == nil {
			started <- actions
		}
		return actions, err
	}}
	service := newActivationSnapshotService(t)
	connection := newFakeApplicationConnection(true)
	screen := newStartupObservedScreen()
	dependencies := authenticatedTestDependencies(connection, func() (applicationService, error) { return service, nil })
	dependencies.capabilities = &capabilities
	dependencies.runConnection = func(context.Context, tcell.Screen, tui.ConnectionInput) error { return nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runAuthenticatedApplication(ctx, screen, dependencies) }()
	actions := receiveLinuxAction(t, started)
	<-screen.shown
	<-screen.shown
	if <-service.loads != "first@lid" {
		t.Fatal("incorrect initial selection")
	}
	if err := actions.Notify(ctx, tui.Notification{ChatID: "target@lid"}); err != nil {
		t.Fatal(err)
	}
	call := receiveLinuxAction(t, fixture.calls)
	call.reply <- linuxActionResult{output: "default"}
	receiveLinuxAction(t, fixture.focus)
	<-screen.shown
	<-screen.shown
	if <-service.loads != "target@lid" || !strings.Contains(startupScreenText(screen), "Target conversation content") {
		t.Fatal("click did not select/load the conversation")
	}
	// Draft protection is owned by the existing TUI, not the window dispatcher.
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	<-screen.shown
	screen.InjectKey(tcell.KeyRune, 'x', tcell.ModNone)
	<-screen.shown
	if err := actions.Notify(ctx, tui.Notification{ChatID: "first@lid"}); err != nil {
		t.Fatal(err)
	}
	call = receiveLinuxAction(t, fixture.calls)
	call.reply <- linuxActionResult{output: "default"}
	receiveLinuxAction(t, fixture.focus)
	<-call.ctx.Done() // activation has been enqueued
	// Selection checks themselves have deterministic exhaustive TUI tests; here
	// assert the application never loads another chat while composing.
	screen.InjectKey(tcell.KeyCtrlP, 0, tcell.ModNone)
	<-screen.shown
	screen.InjectKey(tcell.KeyCtrlP, 0, tcell.ModNone)
	<-screen.shown
	if len(service.loads) != 0 || !strings.Contains(startupScreenText(screen), "Target conversation content") {
		t.Fatal("click disrupted a draft")
	}
	// Leave an action waiter pending when the application exits.
	if err := actions.Notify(ctx, tui.Notification{ChatID: "first@lid"}); err != nil {
		t.Fatal(err)
	}
	pending := receiveLinuxAction(t, fixture.calls)
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if err := receiveLinuxAction(t, done); err != nil {
		t.Fatal(err)
	}
	<-pending.ctx.Done()
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.markers) != 0 {
		t.Fatal("application left window identity marker behind")
	}
}

func TestLinuxNotificationApplicationInitializationFailureIsNonfatal(t *testing.T) {
	isolateApplicationFiles(t)
	fixture := newLinuxActionFixture()
	called := false
	capabilities := platformCapabilities{notifier: fixture.backend(), newLinuxNotificationActions: func(context.Context) (*linuxNotificationActions, error) {
		called = true
		return nil, errors.New("desktop unavailable")
	}}
	service := newAuthenticationReadyService()
	err := runStartedApplicationWithCapabilities(context.Background(), newStartupObservedScreen(), tui.DefaultOptions(), service, func(ctx context.Context, _ tcell.Screen, input tui.Input) error {
		if input.ChatActivations != nil {
			t.Fatal("failed dispatcher supplied activations")
		}
		if !input.NotificationsAvailable || !input.Notify(tui.Notification{Title: "ordinary"}) {
			t.Fatal("ordinary notifications unavailable")
		}
		receiveLinuxAction(t, fixture.plain)
		return nil
	}, capabilities)
	if err != nil || !called {
		t.Fatalf("startup result=%v called=%v", err, called)
	}
}
