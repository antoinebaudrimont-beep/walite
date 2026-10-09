package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/gdamore/tcell/v2"
)

type activationSnapshotService struct {
	*authenticationReadyService
	chats         []model.Chat
	messages      map[string][]model.Message
	loads         chan string
	snapshotError error
}

func newActivationSnapshotService(t *testing.T) *activationSnapshotService {
	t.Helper()
	at := time.Date(2100, 1, 2, 3, 4, 5, 0, time.UTC)
	return &activationSnapshotService{
		authenticationReadyService: newAuthenticationReadyService(),
		chats: []model.Chat{
			mustSnapshotChat(t, "first@lid", "First", false, 0, at.Add(time.Minute)),
			mustSnapshotChat(t, "target@lid", "Target", false, 1, at),
		},
		messages: map[string][]model.Message{
			"first@lid":  {mustSnapshotMessage(t, "first@lid", "first-message", at, false, "First conversation content")},
			"target@lid": {mustSnapshotMessage(t, "target@lid", "target-message", at, false, "Target conversation content")},
		},
		loads: make(chan string, 8),
	}
}

func (service *activationSnapshotService) InitialChats(context.Context, int) ([]model.Chat, error) {
	return append([]model.Chat(nil), service.chats...), service.snapshotError
}

func (service *activationSnapshotService) InitialMessages(_ context.Context, id model.ChatID, _ int) ([]model.Message, error) {
	service.loads <- id.String()
	return append([]model.Message(nil), service.messages[id.String()]...), nil
}

func startActivationTestApplication(t *testing.T, base string) (*chatActivationReceiver, *startupObservedScreen, *activationSnapshotService, context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan *chatActivationReceiver, 1)
	capabilities := platformCapabilities{newChatActivation: func(ctx context.Context) (*chatActivationReceiver, error) {
		receiver, err := newChatActivationReceiverInDirectory(ctx, base)
		if err == nil {
			started <- receiver
		}
		return receiver, err
	}}
	service := newActivationSnapshotService(t)
	connection := newFakeApplicationConnection(true)
	screen := newStartupObservedScreen()
	dependencies := authenticatedTestDependencies(connection, func() (applicationService, error) { return service, nil })
	dependencies.capabilities = &capabilities
	dependencies.runConnection = func(context.Context, tcell.Screen, tui.ConnectionInput) error { return nil }
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		done <- runAuthenticatedApplication(ctx, screen, dependencies)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Error("test application failed to stop")
		}
	})
	var receiver *chatActivationReceiver
	select {
	case receiver = <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("application did not start receiver")
	}
	<-screen.shown
	<-screen.shown // initial selected-chat cache page
	if id := <-service.loads; id != "first@lid" {
		t.Fatal("incorrect startup chat")
	}
	return receiver, screen, service, cancel, done
}

func assertApplicationActivationCleanup(t *testing.T, receiver *chatActivationReceiver) {
	t.Helper()
	select {
	case <-receiver.done:
	default:
		t.Fatal("application returned before receiver stopped")
	}
	for _, path := range []string{receiver.socketPath, receiver.directory} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("application left an IPC socket/directory behind")
		}
	}
}

func TestApplicationChatActivationReachesRealTUIAndDropsBusyRequests(t *testing.T) {
	isolateApplicationFiles(t)
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	receiver, screen, service, _, done := startActivationTestApplication(t, activationTestDirectory(t))
	sendActivationBytes(t, receiver, activationJSON(t, receiver, "unknown@lid"))
	sendActivationBytes(t, receiver, activationJSON(t, receiver, "target@lid"))
	<-screen.shown
	<-screen.shown // selection followed by cache page
	if id := <-service.loads; id != "target@lid" {
		t.Fatal("activation loaded wrong chat")
	}
	if text := startupScreenText(screen); !strings.Contains(text, "Target conversation content") || strings.Contains(text, "First conversation content") {
		t.Fatalf("IPC did not select conversation:\n%s", text)
	}
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	<-screen.shown
	screen.InjectKey(tcell.KeyRune, 'x', tcell.ModNone)
	<-screen.shown
	sendActivationBytes(t, receiver, activationJSON(t, receiver, "first@lid"))
	// A settings key supplies a TUI event barrier after the activation arrives.
	screen.InjectKey(tcell.KeyCtrlP, 0, tcell.ModNone)
	<-screen.shown
	screen.InjectKey(tcell.KeyCtrlP, 0, tcell.ModNone)
	<-screen.shown
	if text := startupScreenText(screen); !strings.Contains(text, "Target conversation content") || !strings.Contains(text, "x") || len(service.loads) != 0 {
		t.Fatalf("busy activation disrupted compose:\n%s", text)
	}
	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	<-screen.shown
	// Dropped activation must not switch chats later when compose ends.
	if text := startupScreenText(screen); !strings.Contains(text, "Target conversation content") {
		t.Fatal("busy activation was deferred")
	}
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	assertApplicationActivationCleanup(t, receiver)
}

func TestApplicationChatActivationInstancesRemainIsolatedAndCancellationCleansUp(t *testing.T) {
	isolateApplicationFiles(t)
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	base := activationTestDirectory(t)
	first, firstScreen, firstService, _, firstDone := startActivationTestApplication(t, base)
	second, secondScreen, secondService, cancelSecond, secondDone := startActivationTestApplication(t, base)
	if first.socketPath == second.socketPath || first.token == second.token {
		t.Fatal("application instances shared receiver identity")
	}
	sendActivationBytes(t, second, activationJSON(t, first, "target@lid"))
	sendActivationBytes(t, first, activationJSON(t, first, "target@lid"))
	<-firstScreen.shown
	<-firstScreen.shown
	if id := <-firstService.loads; id != "target@lid" {
		t.Fatal("wrong first instance chat")
	}
	if text := startupScreenText(secondScreen); !strings.Contains(text, "First conversation content") || len(secondService.loads) != 0 {
		t.Fatal("activation affected another instance")
	}
	firstScreen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	assertApplicationActivationCleanup(t, first)
	// The second owner must keep working after the first application exits.
	sendActivationBytes(t, second, activationJSON(t, second, "target@lid"))
	<-secondScreen.shown
	<-secondScreen.shown
	if id := <-secondService.loads; id != "target@lid" {
		t.Fatal("second receiver stopped with first application")
	}
	cancelSecond()
	if err := <-secondDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel result=%v", err)
	}
	assertApplicationActivationCleanup(t, second)
}

func TestApplicationChatActivationInitializationFailurePreservesNotifications(t *testing.T) {
	isolateApplicationFiles(t)
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	delivered := make(chan struct{}, 1)
	helper, _, _ := newTestDarwinHelperBackend(t, testITermSessionID, func(_ context.Context, command string, _ []string) error {
		if command != darwinOpenCommand {
			t.Error("notification helper command changed")
		}
		delivered <- struct{}{}
		return nil
	})
	starts := 0
	capabilities := platformCapabilities{
		notifier: helper,
		newChatActivation: func(ctx context.Context) (*chatActivationReceiver, error) {
			starts++
			return newChatActivationReceiverInDirectory(ctx, "relative-unavailable-directory")
		},
	}
	err := runStartedApplicationWithCapabilities(context.Background(), newStartupObservedScreen(), tui.DefaultOptions(), newAuthenticationReadyService(),
		func(_ context.Context, _ tcell.Screen, input tui.Input) error {
			if input.ChatActivations != nil || !input.NotificationsAvailable || input.Notify == nil {
				return errors.New("IPC failure disabled notification or startup")
			}
			if !input.Notify(tui.Notification{ChatID: "chat@lid", Title: "walite", Body: "New WhatsApp message"}) {
				return errors.New("notification admission failed")
			}
			<-delivered
			return nil
		}, capabilities)
	if err != nil || starts != 1 {
		t.Fatalf("optional initialization result=%v starts=%d", err, starts)
	}
}

func TestApplicationChatActivationStartupFailuresCleanUp(t *testing.T) {
	for _, stage := range []string{"snapshot", "TUI"} {
		t.Run(stage, func(t *testing.T) {
			isolateApplicationFiles(t)
			t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
			base := activationTestDirectory(t)
			var receiver *chatActivationReceiver
			capabilities := platformCapabilities{newChatActivation: func(ctx context.Context) (*chatActivationReceiver, error) {
				var err error
				receiver, err = newChatActivationReceiverInDirectory(ctx, base)
				return receiver, err
			}}
			failure := errors.New("synthetic startup failure")
			service := newActivationSnapshotService(t)
			if stage == "snapshot" {
				service.snapshotError = failure
			}
			err := runStartedApplicationWithCapabilities(context.Background(), newStartupObservedScreen(), tui.DefaultOptions(), service,
				func(context.Context, tcell.Screen, tui.Input) error { return failure }, capabilities)
			if !errors.Is(err, failure) || receiver == nil {
				t.Fatalf("startup result=%v", err)
			}
			assertApplicationActivationCleanup(t, receiver)
		})
	}
}
