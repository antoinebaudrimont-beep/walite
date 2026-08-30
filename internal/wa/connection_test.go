package wa

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
)

type fakeConnectionClient struct {
	linked bool
	qr     chan whatsmeow.QRChannelItem
	events chan protocolEvent

	connectStarted chan struct{}
	connectOnce    sync.Once
	connect        func(context.Context) error
	qrCalls        atomic.Int32
	disconnects    atomic.Int32
	closes         atomic.Int32
}

func newFakeConnectionClient(linked bool) *fakeConnectionClient {
	return &fakeConnectionClient{
		linked: linked, qr: make(chan whatsmeow.QRChannelItem), events: make(chan protocolEvent, 4),
		connectStarted: make(chan struct{}),
	}
}

func (client *fakeConnectionClient) Linked() bool { return client.linked }
func (client *fakeConnectionClient) GetQRChannel(context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	client.qrCalls.Add(1)
	return client.qr, nil
}
func (client *fakeConnectionClient) Connect(ctx context.Context) error {
	client.connectOnce.Do(func() { close(client.connectStarted) })
	if client.connect != nil {
		return client.connect(ctx)
	}
	return nil
}
func (client *fakeConnectionClient) Events() <-chan protocolEvent { return client.events }
func (client *fakeConnectionClient) Disconnect()                  { client.disconnects.Add(1) }
func (client *fakeConnectionClient) Close() error {
	client.closes.Add(1)
	return nil
}

func TestUnlinkedConnectionPublishesNewestBoundedQR(t *testing.T) {
	client := newFakeConnectionClient(false)
	connection, err := newConnection(client)
	if err != nil {
		t.Fatal(err)
	}
	published := make(chan ConnectionUpdate, 8)
	connection.afterPublish = func(update ConnectionUpdate) { published <- update }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- connection.Run(ctx) }()

	if update := receiveConnectionUpdate(t, published); update.State() != ConnectionWaitingForQR {
		t.Fatalf("initial state=%v", update.State())
	}
	<-client.connectStarted
	client.qr <- whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventCode, Code: "first synthetic pairing value"}
	first := receiveConnectionUpdate(t, published)
	client.qr <- whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventCode, Code: "second synthetic pairing value"}
	second := receiveConnectionUpdate(t, published)
	firstFrame, firstOK := first.QR()
	secondFrame, secondOK := second.QR()
	if !firstOK || !secondOK || firstFrame.Size() == 0 || secondFrame.Size() == 0 || firstFrame == secondFrame {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	if len(connection.updates) != 1 {
		t.Fatalf("coalesced update entries=%d want=1", len(connection.updates))
	}
	latest := <-connection.Updates()
	latestFrame, ok := latest.QR()
	if !ok || latestFrame != secondFrame {
		t.Fatal("public update did not retain only the newest QR")
	}

	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run=%v", err)
	}
	if client.disconnects.Load() != 1 {
		t.Fatalf("disconnects=%d", client.disconnects.Load())
	}
}

func TestPairingSuccessTransitionsToConnected(t *testing.T) {
	client := newFakeConnectionClient(false)
	connection, _ := newConnection(client)
	published := make(chan ConnectionUpdate, 8)
	connection.afterPublish = func(update ConnectionUpdate) { published <- update }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- connection.Run(ctx) }()

	receiveConnectionUpdate(t, published)
	<-client.connectStarted
	client.qr <- whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventCode, Code: "synthetic pairing success"}
	if update := receiveConnectionUpdate(t, published); update.State() != ConnectionWaitingForQR {
		t.Fatalf("QR state=%v", update.State())
	}
	client.qr <- whatsmeow.QRChannelSuccess
	if update := receiveConnectionUpdate(t, published); update.State() != ConnectionConnecting {
		t.Fatalf("post-pair state=%v", update.State())
	}
	client.events <- protocolEvent{kind: protocolConnected}
	if update := receiveConnectionUpdate(t, published); update.State() != ConnectionConnected {
		t.Fatalf("connected state=%v", update.State())
	}

	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run=%v", err)
	}
}

func TestPersistedLinkedSessionReconnectSkipsPairing(t *testing.T) {
	for launch := 1; launch <= 2; launch++ {
		client := newFakeConnectionClient(true)
		connection, _ := newConnection(client)
		published := make(chan ConnectionUpdate, 4)
		connection.afterPublish = func(update ConnectionUpdate) { published <- update }
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- connection.Run(ctx) }()

		if update := receiveConnectionUpdate(t, published); update.State() != ConnectionConnecting {
			t.Fatalf("launch %d state=%v", launch, update.State())
		}
		<-client.connectStarted
		if client.qrCalls.Load() != 0 {
			t.Fatalf("launch %d QR calls=%d", launch, client.qrCalls.Load())
		}
		client.events <- protocolEvent{kind: protocolConnected}
		if update := receiveConnectionUpdate(t, published); update.State() != ConnectionConnected {
			t.Fatalf("launch %d connected=%v", launch, update.State())
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("launch %d Run=%v", launch, err)
		}
	}
}

func TestConnectionFailureAndPairingChannelClosureAreControlled(t *testing.T) {
	t.Run("connect failure", func(t *testing.T) {
		client := newFakeConnectionClient(true)
		failure := errors.New("synthetic connect failure")
		client.connect = func(context.Context) error { return failure }
		connection, _ := newConnection(client)
		done := make(chan error, 1)
		go func() { done <- connection.Run(context.Background()) }()
		if err := <-done; !errors.Is(err, ErrConnectionFailed) || !errors.Is(err, failure) {
			t.Fatalf("Run=%v", err)
		}
		if update := drainLastConnectionUpdate(connection.Updates()); update.State() != ConnectionFailed {
			t.Fatalf("state=%v", update.State())
		}
	})

	t.Run("pairing channel closed", func(t *testing.T) {
		client := newFakeConnectionClient(false)
		connection, _ := newConnection(client)
		done := make(chan error, 1)
		go func() { done <- connection.Run(context.Background()) }()
		<-client.connectStarted
		close(client.qr)
		if err := <-done; !errors.Is(err, ErrPairingFailed) {
			t.Fatalf("Run=%v", err)
		}
	})
}

func TestConnectionCancellationWhileWaitingAndConnecting(t *testing.T) {
	t.Run("waiting for QR", func(t *testing.T) {
		client := newFakeConnectionClient(false)
		connection, _ := newConnection(client)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- connection.Run(ctx) }()
		<-client.connectStarted
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("Run=%v", err)
		}
	})

	t.Run("connecting", func(t *testing.T) {
		client := newFakeConnectionClient(true)
		client.connect = func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}
		connection, _ := newConnection(client)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- connection.Run(ctx) }()
		<-client.connectStarted
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("Run=%v", err)
		}
	})
}

func TestConnectionDisconnectAndCloseAreIdempotent(t *testing.T) {
	client := newFakeConnectionClient(true)
	connection, _ := newConnection(client)
	connection.Disconnect()
	connection.Disconnect()
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if client.disconnects.Load() != 1 || client.closes.Load() != 1 {
		t.Fatalf("disconnects=%d closes=%d", client.disconnects.Load(), client.closes.Load())
	}
}

func TestConnectionIsSingleUse(t *testing.T) {
	client := newFakeConnectionClient(true)
	connection, _ := newConnection(client)
	connection.Disconnect()
	if err := connection.Run(context.Background()); err != nil {
		t.Fatalf("first Run=%v", err)
	}
	if err := connection.Run(context.Background()); !errors.Is(err, ErrConnectionStarted) {
		t.Fatalf("second Run=%v", err)
	}
}

func TestEncodedPairingQRIsBoundedAndKeepsQuietZone(t *testing.T) {
	frame, err := encodePairingQR("synthetic QR content with Unicode Café 東京")
	if err != nil {
		t.Fatal(err)
	}
	if frame.Size() <= 0 || frame.Size() > MaxPairingQRSize {
		t.Fatalf("size=%d", frame.Size())
	}
	for offset := 0; offset < frame.Size(); offset++ {
		if frame.Cell(offset, 0) || frame.Cell(offset, frame.Size()-1) || frame.Cell(0, offset) || frame.Cell(frame.Size()-1, offset) {
			t.Fatal("encoder quiet zone is not clear")
		}
	}
	if _, err := encodePairingQR(""); err == nil {
		t.Fatal("empty QR accepted")
	}
}

func receiveConnectionUpdate(t *testing.T, updates <-chan ConnectionUpdate) ConnectionUpdate {
	t.Helper()
	select {
	case update := <-updates:
		return update
	case <-time.After(5 * time.Second):
		t.Fatal("connection update timeout")
		return ConnectionUpdate{}
	}
}

func drainLastConnectionUpdate(updates <-chan ConnectionUpdate) ConnectionUpdate {
	var last ConnectionUpdate
	for update := range updates {
		last = update
	}
	return last
}
