package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/gdamore/tcell/v2"
)

func linuxActivationHintFromArguments(t *testing.T, arguments []string) linuxChatActivationHintValue {
	t.Helper()
	prefix := "--hint=string:" + linuxChatActivationHint + ":"
	for _, argument := range arguments {
		if !strings.HasPrefix(argument, prefix) {
			continue
		}
		var hint linuxChatActivationHintValue
		if err := json.Unmarshal([]byte(strings.TrimPrefix(argument, prefix)), &hint); err != nil {
			t.Fatal(err)
		}
		return hint
	}
	t.Fatal("Linux notification omitted chat activation hint")
	return linuxChatActivationHintValue{}
}

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

func TestLinuxStoredNotificationTicketsSelectIndependentDirectAndGroupChats(t *testing.T) {
	isolateApplicationFiles(t)
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	fixture := newLinuxActionFixture()
	base := activationTestDirectory(t)
	receivers := make(chan *chatActivationReceiver, 1)
	actionsStarted := make(chan *linuxNotificationActions, 1)
	capabilities := platformCapabilities{
		notifier: fixture.backend(),
		newChatActivation: func(ctx context.Context) (*chatActivationReceiver, error) {
			receiver, err := newChatActivationReceiverInDirectory(ctx, base)
			if err == nil {
				receivers <- receiver
			}
			return receiver, err
		},
		newLinuxNotificationActions: func(ctx context.Context) (*linuxNotificationActions, error) {
			actions, err := newLinuxNotificationActions(ctx, fixture.backend(), fixture.options())
			if err == nil {
				actionsStarted <- actions
			}
			return actions, err
		},
	}
	service := newActivationSnapshotService(t)
	at := service.chats[len(service.chats)-1].LastMessageAt().Add(-time.Minute)
	service.chats = append(service.chats, mustSnapshotChat(t, "family@g.us", "Family", false, 1, at))
	service.messages["family@g.us"] = []model.Message{mustSnapshotMessage(t, "family@g.us", "group-message", at, false, "Family group content")}
	connection := newFakeApplicationConnection(true)
	screen := newStartupObservedScreen()
	dependencies := authenticatedTestDependencies(connection, func() (applicationService, error) { return service, nil })
	dependencies.capabilities = &capabilities
	dependencies.runConnection = func(context.Context, tcell.Screen, tui.ConnectionInput) error { return nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runAuthenticatedApplication(ctx, screen, dependencies) }()
	receiver := receiveLinuxAction(t, receivers)
	actions := receiveLinuxAction(t, actionsStarted)
	<-screen.shown
	<-screen.shown
	if <-service.loads != "first@lid" {
		t.Fatal("incorrect initial selection")
	}
	for _, test := range []struct {
		chatID, content string
	}{
		{chatID: "target@lid", content: "Target conversation content"},
		{chatID: "family@g.us", content: "Family group content"},
	} {
		if err := actions.Notify(ctx, tui.Notification{ChatID: test.chatID, Title: "walite", Body: "New WhatsApp message"}); err != nil {
			t.Fatal(err)
		}
		call := receiveLinuxAction(t, fixture.calls)
		hint := linuxActivationHintFromArguments(t, call.args)
		if hint.ActivationSocket != receiver.socketPath || hint.ActivationID == "" {
			t.Fatalf("wrong per-instance activation hint: %+v", hint)
		}
		sendActivationBytes(t, receiver, activationTicketJSON(t, hint.ActivationID))
		call.reply <- linuxActionResult{} // notification-center selection, not notify-send's live Open action
		<-screen.shown
		<-screen.shown
		if id := <-service.loads; id != test.chatID || !strings.Contains(startupScreenText(screen), test.content) {
			t.Fatalf("stored notification selected wrong chat: got=%q want=%q", id, test.chatID)
		}
	}
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if err := receiveLinuxAction(t, done); err != nil {
		t.Fatal(err)
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
