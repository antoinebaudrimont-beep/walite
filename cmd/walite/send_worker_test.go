package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/config"
	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/service"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/antoinebaudrimont-beep/walite/internal/wa"
	"github.com/gdamore/tcell/v2"
)

type workerTestApplication struct {
	*receiveOnlyChatApplication
	send     func(context.Context, service.SendTextRequest) error
	validate func(service.SendTextRequest) error
	calls    atomic.Int32
}

type workerMediaApplication struct {
	*workerTestApplication
	media      func(context.Context, service.SendMediaRequest) error
	mediaCalls atomic.Int32
}

func (application *workerMediaApplication) SendMedia(ctx context.Context, request service.SendMediaRequest) error {
	application.mediaCalls.Add(1)
	return application.media(ctx, request)
}

func newWorkerTestApplication() *workerTestApplication {
	return &workerTestApplication{receiveOnlyChatApplication: &receiveOnlyChatApplication{authenticationReadyService: newAuthenticationReadyService()}}
}
func (application *workerTestApplication) SendText(ctx context.Context, request service.SendTextRequest) error {
	application.calls.Add(1)
	return application.send(ctx, request)
}
func (application *workerTestApplication) ValidateText(request service.SendTextRequest) error {
	if application.validate != nil {
		return application.validate(request)
	}
	return nil
}

func TestSendWorkerBoundedSingleInflightAndUnconsumedCompletion(t *testing.T) {
	application := newWorkerTestApplication()
	started, release := make(chan struct{}), make(chan struct{})
	application.send = func(ctx context.Context, request service.SendTextRequest) error {
		if request.Text() != "é 日本語 🐧" || request.ChatID().String() != "123@lid" {
			t.Error("request changed")
		}
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	worker := newSendWorker(context.Background(), application)
	defer worker.stop()
	request := tui.SendRequest{ChatID: "123@lid", Text: "é 日本語 🐧"}
	if cap(worker.requests) != 1 || cap(worker.results) != 1 {
		t.Fatal("mailboxes are not capacity one")
	}
	if err := worker.admit(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := worker.admit(context.Background(), request); !errors.Is(err, &service.CoreError{Kind: service.CoreBusy}) {
		t.Fatalf("second admission=%v", err)
	}
	close(release)
	result := <-worker.results
	if result.Failed || application.calls.Load() != 1 {
		t.Fatalf("result=%+v calls=%d", result, application.calls.Load())
	}
	// A queued completion, independently of network activity, owns the slot.
	worker.mu.Lock()
	worker.results <- tui.SendResult{}
	worker.mu.Unlock()
	if err := worker.admit(context.Background(), request); !errors.Is(err, &service.CoreError{Kind: service.CoreBusy}) {
		t.Fatalf("completion admission=%v", err)
	}
	<-worker.results
}

func TestSendWorkerPreflightRejectionMakesZeroCalls(t *testing.T) {
	application := newWorkerTestApplication()
	worker := newSendWorker(context.Background(), application)
	defer worker.stop()
	request := tui.SendRequest{ChatID: "123@lid", Text: "keep draft"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := worker.admit(ctx, request); err == nil {
		t.Fatal("cancelled admitted")
	}
	for _, bad := range []tui.SendRequest{{ChatID: "123@lid"}, {ChatID: "123@lid", Text: "reply", ReplyToID: "id"}} {
		if err := worker.admit(context.Background(), bad); err == nil {
			t.Fatal("invalid admitted")
		}
	}
	application.validate = func(service.SendTextRequest) error { return wa.ErrTextUnavailable }
	if err := worker.admit(context.Background(), request); !errors.Is(err, wa.ErrTextUnavailable) {
		t.Fatal(err)
	}
	if application.calls.Load() != 0 {
		t.Fatal("preflight called service")
	}
}

func TestSendWorkerCancellationBeforeExecutionMakesZeroCalls(t *testing.T) {
	application := newWorkerTestApplication()
	ctx, cancel := context.WithCancel(context.Background())
	// Delay the owned worker's start to make queued-before-execution cancellation deterministic.
	worker := &sendWorker{ctx: ctx, cancel: cancel, application: application, requests: make(chan tui.SendRequest, 1), results: make(chan tui.SendResult, 1), done: make(chan struct{})}
	if err := worker.admit(ctx, tui.SendRequest{ChatID: "123@lid", Text: "not sent"}); err != nil {
		t.Fatal(err)
	}
	cancel()
	go worker.run()
	worker.stop()
	if application.calls.Load() != 0 {
		t.Fatal("cancelled queued work called transport")
	}
	if err := worker.admit(context.Background(), tui.SendRequest{ChatID: "123@lid", Text: "closed"}); err == nil {
		t.Fatal("admission after shutdown")
	}
}

func TestSendWorkerCancellationAndSuccessCompletion(t *testing.T) {
	for _, success := range []bool{false, true} {
		t.Run(map[bool]string{false: "blocked transport", true: "successful transport"}[success], func(t *testing.T) {
			application := newWorkerTestApplication()
			started := make(chan struct{})
			application.send = func(ctx context.Context, _ service.SendTextRequest) error {
				close(started)
				<-ctx.Done()
				if success {
					return nil
				}
				return wa.ErrTextUncertain
			}
			worker := newSendWorker(context.Background(), application)
			if err := worker.admit(context.Background(), tui.SendRequest{ChatID: "123@lid", Text: "text"}); err != nil {
				t.Fatal(err)
			}
			<-started
			worker.stop()
			result := <-worker.results
			if result.Failed == success || result.Uncertain == success || application.calls.Load() != 1 {
				t.Fatalf("result=%+v calls=%d", result, application.calls.Load())
			}
		})
	}
}

func TestSendWorkerFailureDoesNotRetry(t *testing.T) {
	application := newWorkerTestApplication()
	application.send = func(context.Context, service.SendTextRequest) error { return wa.ErrTextUncertain }
	worker := newSendWorker(context.Background(), application)
	if err := worker.admit(context.Background(), tui.SendRequest{ChatID: "123@lid", Text: "keep"}); err != nil {
		t.Fatal(err)
	}
	result := <-worker.results
	worker.stop()
	if !result.Failed || !result.Uncertain || application.calls.Load() != 1 {
		t.Fatalf("result=%+v calls=%d", result, application.calls.Load())
	}
}

func TestSendWorkerClassifiesMediaOffLoopAndCallsApplicationOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Café $(not-shell).pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7\nsynthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	application := &workerMediaApplication{workerTestApplication: newWorkerTestApplication()}
	application.media = func(_ context.Context, request service.SendMediaRequest) error {
		if request.Path() != path || request.ChatID().String() != "123@lid" || request.Media().Kind() != model.MediaDocument ||
			request.Media().Name() != "Café $(not-shell).pdf" || request.Media().MIMEType() != "application/pdf" {
			t.Fatalf("request path=%q chat=%q media=%+v", request.Path(), request.ChatID().String(), request.Media())
		}
		return nil
	}
	worker := newSendWorker(context.Background(), application)
	if err := worker.admit(context.Background(), tui.SendRequest{ChatID: "123@lid", FilePath: path}); err != nil {
		t.Fatal(err)
	}
	result := <-worker.results
	worker.stop()
	if result.Failed || !result.Media || application.mediaCalls.Load() != 1 || application.calls.Load() != 0 {
		t.Fatalf("result=%+v media calls=%d text calls=%d", result, application.mediaCalls.Load(), application.calls.Load())
	}
}

func TestSendWorkerInvalidFileCreatesControlledFailureWithoutTransport(t *testing.T) {
	application := &workerMediaApplication{workerTestApplication: newWorkerTestApplication(), media: func(context.Context, service.SendMediaRequest) error {
		return errors.New("must not be called")
	}}
	worker := newSendWorker(context.Background(), application)
	path := filepath.Join(t.TempDir(), "missing")
	if err := worker.admit(context.Background(), tui.SendRequest{ChatID: "123@lid", FilePath: path}); err != nil {
		t.Fatal(err)
	}
	result := <-worker.results
	worker.stop()
	if !result.Failed || result.Uncertain || !result.Media || application.mediaCalls.Load() != 0 {
		t.Fatalf("result=%+v media calls=%d", result, application.mediaCalls.Load())
	}
}

func TestOutgoingTUIStaysResponsiveDuringBlockedSendAndShutsDown(t *testing.T) {
	isolateApplicationFiles(t)
	application := newWorkerTestApplication()
	base := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	application.chat = mustSnapshotChat(t, "123@lid", "Synthetic", false, 0, base)
	application.message = mustSnapshotMessage(t, "123@lid", "before", base, false, "incoming")
	started, stopped := make(chan struct{}), make(chan struct{})
	application.send = func(ctx context.Context, _ service.SendTextRequest) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return wa.ErrTextUncertain
	}
	screen := newStartupObservedScreen()
	done := make(chan error, 1)
	go func() {
		done <- runStartedApplication(context.Background(), screen, tui.DefaultOptions(), application, tui.Run)
	}()
	<-screen.shown
	<-screen.shown // asynchronous selected-chat cache page
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	<-screen.shown
	for _, r := range "draft 🐧" {
		screen.InjectKey(tcell.KeyRune, r, tcell.ModNone)
		<-screen.shown
	}
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	<-screen.shown
	<-started
	if text := startupScreenText(screen); !strings.Contains(text, "Sending") || !strings.Contains(text, "draft 🐧") {
		t.Fatalf("pending screen:\n%s", text)
	}
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone) // no duplicate send
	screen.InjectKey(tcell.KeyCtrlP, 0, tcell.ModNone)
	<-screen.shown
	if text := startupScreenText(screen); !strings.Contains(text, "Settings") {
		t.Fatal("settings blocked by network")
	}
	screen.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	<-screen.shown
	if err := screen.PostEvent(tcell.NewEventResize(100, 30)); err != nil {
		t.Fatal(err)
	}
	<-screen.shown
	if application.calls.Load() != 1 {
		t.Fatal("duplicate transport call")
	}
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	<-stopped
	if screen.finiCount.Load() != 1 {
		t.Fatal("terminal lifecycle changed")
	}
}

func TestAuthenticatedQuitDisconnectsBeforeWaitingForSender(t *testing.T) {
	connection := newFakeApplicationConnection(true)
	disconnected, sendStarted := make(chan struct{}), make(chan struct{})
	connection.run = func(ctx context.Context) error {
		<-ctx.Done()
		connection.Disconnect()
		close(disconnected)
		return ctx.Err()
	}
	application := newWorkerTestApplication()
	application.send = func(context.Context, service.SendTextRequest) error {
		close(sendStarted)
		// Model the upstream non-context-aware send lock / peer response waiter.
		<-disconnected
		return wa.ErrTextUncertain
	}
	base := time.Now().UTC()
	application.chat = mustSnapshotChat(t, "123@lid", "Synthetic", false, 0, base)
	application.message = mustSnapshotMessage(t, "123@lid", "before", base, false, "incoming")
	screen := newStartupObservedScreen()
	err := runAuthenticatedApplication(context.Background(), screen, authenticatedApplicationDependencies{
		configuration: staticUIStore{settings: config.DefaultUI()},
		newConnection: func(context.Context) (applicationConnection, error) { return connection, nil },
		newService:    func() (applicationService, error) { return application, nil },
		runConnection: func(context.Context, tcell.Screen, tui.ConnectionInput) error { return nil },
		runTUI: func(ctx context.Context, _ tcell.Screen, input tui.Input) error {
			if err := input.Send(ctx, tui.SendRequest{ChatID: "123@lid", Text: "text"}); err != nil {
				return err
			}
			<-sendStarted
			return nil // explicit user quit, not parent cancellation
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if connection.disconnectCount.Load() != 1 || connection.closeCount.Load() != 1 || screen.finiCount.Load() != 1 {
		t.Fatal("shutdown ownership changed")
	}
}

type connectedTestSender struct {
	calls atomic.Int32
	at    time.Time
}

func (*connectedTestSender) ValidateText(model.ChatID, string, ...model.TextQuote) error { return nil }
func (sender *connectedTestSender) SendText(_ context.Context, chatID model.ChatID, text string, _ ...model.TextQuote) (model.Event, error) {
	sender.calls.Add(1)
	message, err := model.NewMessage(model.MessageInput{ChatID: chatID.String(), MessageID: "real-result-fixture", SentAt: sender.at, FromMe: true, Text: text})
	if err != nil {
		return model.Event{}, err
	}
	return model.NewEvent(message, sender.at)
}

func TestConnectedOutgoingWorkerUsesCommittedLivePath(t *testing.T) {
	connection, err := wa.NewConnection(context.Background(), filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close() // never Connect or Run this connection
	sender := &connectedTestSender{at: time.Now().UTC()}
	application, err := newConnectedApplicationService(connection.RealtimeSource(), sender, connection.ReadReceiptSender(), openConnectedTestCache(t))
	if err != nil {
		t.Fatal(err)
	}
	var presented tui.LiveMessage
	view := func(ctx context.Context, _ tcell.Screen, input tui.Input) error {
		if len(input.InitialState.Chats) != 0 || input.SendResults == nil {
			return errors.New("connected initial/async wiring")
		}
		if err := input.Send(ctx, tui.SendRequest{ChatID: "123@lid", Text: "é 日本語 🐧"}); err != nil {
			return err
		}
		if result := <-input.SendResults; result.Failed {
			return errors.New("send failed")
		}
		presented = <-input.LiveEvents
		return nil
	}
	if err := runStartedApplication(context.Background(), newStartupObservedScreen(), tui.DefaultOptions(), application, view); err != nil {
		t.Fatal(err)
	}
	if presented.MessageID != "real-result-fixture" || !presented.FromMe || presented.Text != "é 日本語 🐧" || presented.SentAt.UnixMilli() != sender.at.UnixMilli() || sender.calls.Load() != 1 {
		t.Fatalf("presented=%+v calls=%d", presented, sender.calls.Load())
	}
	chatID, _ := model.NewChatID("123@lid")
	messages, _, err := application.(*connectedApplicationService).store.Page(context.Background(), chatID, model.NoCursor(), 10)
	if err != nil || len(messages) != 1 || messages[0].MessageID().String() != presented.MessageID {
		t.Fatalf("messages=%v err=%v", messages, err)
	}
}
