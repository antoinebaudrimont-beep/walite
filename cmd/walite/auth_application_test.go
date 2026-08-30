package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/antoinebaudrimont-beep/walite/internal/config"
	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/antoinebaudrimont-beep/walite/internal/wa"
	"github.com/gdamore/tcell/v2"
)

type fakeApplicationConnection struct {
	linked          bool
	updates         chan wa.ConnectionUpdate
	started         chan struct{}
	run             func(context.Context) error
	disconnectOnce  sync.Once
	disconnectCount atomic.Int64
	closeCount      atomic.Int64
}

func newFakeApplicationConnection(linked bool) *fakeApplicationConnection {
	return &fakeApplicationConnection{
		linked:  linked,
		updates: make(chan wa.ConnectionUpdate, 4),
		started: make(chan struct{}),
	}
}

func (connection *fakeApplicationConnection) Linked() bool { return connection.linked }
func (connection *fakeApplicationConnection) Updates() <-chan wa.ConnectionUpdate {
	return connection.updates
}
func (connection *fakeApplicationConnection) Run(ctx context.Context) error {
	close(connection.started)
	defer close(connection.updates)
	defer connection.Disconnect()
	if connection.run != nil {
		return connection.run(ctx)
	}
	<-ctx.Done()
	return ctx.Err()
}
func (connection *fakeApplicationConnection) Disconnect() {
	connection.disconnectOnce.Do(func() { connection.disconnectCount.Add(1) })
}
func (connection *fakeApplicationConnection) Close() error {
	connection.closeCount.Add(1)
	connection.Disconnect()
	return nil
}

type authenticationReadyService struct {
	updates chan model.Update
	live    chan model.LiveEvent
}

func newAuthenticationReadyService() *authenticationReadyService {
	return &authenticationReadyService{updates: make(chan model.Update, 1), live: make(chan model.LiveEvent)}
}

func (service *authenticationReadyService) Run(ctx context.Context) error {
	defer close(service.updates)
	defer close(service.live)
	ready, _ := model.NewUpdate(model.UpdateInput{Kind: model.UpdateReady})
	service.updates <- ready
	<-ctx.Done()
	return ctx.Err()
}
func (service *authenticationReadyService) Updates() <-chan model.Update       { return service.updates }
func (service *authenticationReadyService) LiveEvents() <-chan model.LiveEvent { return service.live }
func (*authenticationReadyService) InitialChats(context.Context, int) ([]model.Chat, error) {
	return nil, nil
}
func (*authenticationReadyService) InitialMessages(context.Context, model.ChatID, int) ([]model.Message, error) {
	return nil, errors.New("unexpected message snapshot")
}

func TestAuthenticatedApplicationPairsBeforeConstructingService(t *testing.T) {
	connection := newFakeApplicationConnection(false)
	screen := newStartupObservedScreen()
	serviceConstructed := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- runAuthenticatedApplication(context.Background(), screen, authenticatedTestDependencies(
			connection,
			func() (applicationService, error) {
				close(serviceConstructed)
				return newAuthenticationReadyService(), nil
			},
		))
	}()

	<-connection.started
	<-screen.shown
	select {
	case <-serviceConstructed:
		t.Fatal("service constructed before authentication")
	default:
	}
	connection.updates <- testWAQRUpdate(t)
	<-screen.shown
	if text := startupScreenText(screen); !strings.Contains(text, "Linked devices") || !strings.Contains(text, "Waiting for phone") {
		t.Fatalf("pairing screen:\n%s", text)
	}
	connection.updates <- wa.NewConnectionUpdate(wa.ConnectionConnected)
	<-screen.shown
	<-serviceConstructed
	<-screen.shown
	if text := startupScreenText(screen); !strings.Contains(text, "No chats") || strings.Contains(text, "Scan using") {
		t.Fatalf("normal screen:\n%s", text)
	}
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if err := <-result; err != nil {
		t.Fatalf("runAuthenticatedApplication=%v", err)
	}
	assertAuthenticatedShutdown(t, screen, connection)
}

func TestAuthenticatedApplicationLinkedStartupSkipsQR(t *testing.T) {
	connection := newFakeApplicationConnection(true)
	screen := newStartupObservedScreen()
	result := make(chan error, 1)
	go func() {
		result <- runAuthenticatedApplication(context.Background(), screen, authenticatedTestDependencies(
			connection, func() (applicationService, error) { return newAuthenticationReadyService(), nil },
		))
	}()
	<-connection.started
	<-screen.shown
	if text := startupScreenText(screen); !strings.Contains(text, "Connecting") || strings.Contains(text, "Scan using") {
		t.Fatalf("linked startup:\n%s", text)
	}
	connection.updates <- wa.NewConnectionUpdate(wa.ConnectionConnected)
	<-screen.shown
	<-screen.shown
	screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	assertAuthenticatedShutdown(t, screen, connection)
}

func TestAuthenticatedApplicationConnectionFailureSkipsService(t *testing.T) {
	failure := errors.New("synthetic connection failure")
	release := make(chan struct{})
	connection := newFakeApplicationConnection(false)
	connection.run = func(context.Context) error {
		<-release
		connection.updates <- wa.NewConnectionUpdate(wa.ConnectionFailed)
		return failure
	}
	serviceConstructed := false
	screen := newStartupObservedScreen()
	result := make(chan error, 1)
	go func() {
		result <- runAuthenticatedApplication(context.Background(), screen, authenticatedTestDependencies(
			connection,
			func() (applicationService, error) {
				serviceConstructed = true
				return nil, nil
			},
		))
	}()
	<-connection.started
	<-screen.shown
	close(release)
	err := <-result
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "connect WhatsApp") {
		t.Fatalf("runAuthenticatedApplication=%v", err)
	}
	if serviceConstructed {
		t.Fatal("service constructed after connection failure")
	}
	assertAuthenticatedShutdown(t, screen, connection)
}

func TestAuthenticatedApplicationPairingCancellation(t *testing.T) {
	connection := newFakeApplicationConnection(false)
	screen := newStartupObservedScreen()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- runAuthenticatedApplication(ctx, screen, authenticatedTestDependencies(
			connection, func() (applicationService, error) { return newAuthenticationReadyService(), nil },
		))
	}()
	<-connection.started
	<-screen.shown
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("runAuthenticatedApplication=%v", err)
	}
	assertAuthenticatedShutdown(t, screen, connection)
}

func TestConnectionUpdateAdapterPreservesBoundedQRExactly(t *testing.T) {
	update := testWAQRUpdate(t)
	presentation, err := adaptConnectionUpdate(update)
	if err != nil {
		t.Fatal(err)
	}
	qr, _ := update.QR()
	if presentation.State != tui.ConnectionWaitingForQR || !presentation.HasQR || presentation.QR.Size() != qr.Size() {
		t.Fatalf("presentation=%+v", presentation)
	}
	for y := 0; y < qr.Size(); y++ {
		for x := 0; x < qr.Size(); x++ {
			if presentation.QR.Cell(x, y) != qr.Cell(x, y) {
				t.Fatalf("cell %d,%d changed", x, y)
			}
		}
	}
}

func authenticatedTestDependencies(
	connection applicationConnection,
	newService func() (applicationService, error),
) authenticatedApplicationDependencies {
	return authenticatedApplicationDependencies{
		configuration: staticUIStore{settings: config.DefaultUI()},
		newConnection: func(context.Context) (applicationConnection, error) { return connection, nil },
		newService:    newService,
		runConnection: tui.RunConnectionInitialized,
		runTUI:        tui.RunInitialized,
	}
}

func testWAQRUpdate(t *testing.T) wa.ConnectionUpdate {
	t.Helper()
	const size = 21
	cells := make([]bool, size*size)
	for y := 4; y < size-4; y++ {
		for x := 4; x < size-4; x++ {
			cells[y*size+x] = (x+y)%2 == 0
		}
	}
	frame, err := wa.NewQRFrame(size, cells)
	if err != nil {
		t.Fatal(err)
	}
	return wa.NewPairingUpdate(frame)
}

func assertAuthenticatedShutdown(t *testing.T, screen *startupObservedScreen, connection *fakeApplicationConnection) {
	t.Helper()
	if got := screen.initCount.Load(); got != 1 {
		t.Fatalf("screen Init count=%d", got)
	}
	if got := screen.finiCount.Load(); got != 1 {
		t.Fatalf("screen Fini count=%d", got)
	}
	if got := connection.disconnectCount.Load(); got != 1 {
		t.Fatalf("Disconnect count=%d", got)
	}
	if got := connection.closeCount.Load(); got != 1 {
		t.Fatalf("Close count=%d", got)
	}
}
