package wa

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	qrcode "github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
)

const (
	// MaxPairingQRSize includes the largest QR symbol and its quiet zone.
	MaxPairingQRSize     = 185
	maxPairingQRCells    = MaxPairingQRSize * MaxPairingQRSize
	maxPairingCodeBytes  = 4096
	connectionUpdateSize = 1
	qrEventSuccess       = "success"
)

var (
	ErrConnectionRejected = errors.New("WhatsApp connection rejected")
	ErrConnectionStarted  = errors.New("WhatsApp connection already started")
	ErrConnectionFailed   = errors.New("WhatsApp connection failed")
	ErrPairingFailed      = errors.New("WhatsApp pairing failed")
	ErrConnectionClosed   = errors.New("WhatsApp connection closed")
)

// ConnectionState is the current coalescible authentication/connection state.
type ConnectionState uint8

const (
	ConnectionDisconnected ConnectionState = iota + 1
	ConnectionWaitingForQR
	ConnectionConnecting
	ConnectionConnected
	ConnectionFailed
)

// QRFrame is a bounded immutable-by-value QR raster. It never contains the raw
// pairing token and includes the encoder-provided quiet zone.
type QRFrame struct {
	size  uint16
	cells [maxPairingQRCells]bool
}

// NewQRFrame copies a bounded raster into an application-owned QR frame.
func NewQRFrame(size int, cells []bool) (QRFrame, error) {
	if size <= 0 || size > MaxPairingQRSize || len(cells) != size*size {
		return QRFrame{}, errors.New("QR frame rejected")
	}
	var frame QRFrame
	frame.size = uint16(size)
	copy(frame.cells[:], cells)
	return frame, nil
}

// Size returns the square raster width and height in modules.
func (frame QRFrame) Size() int { return int(frame.size) }

// Cell reports whether one validated raster module is dark.
func (frame QRFrame) Cell(x, y int) bool {
	size := frame.Size()
	return x >= 0 && y >= 0 && x < size && y < size && frame.cells[y*size+x]
}

// ConnectionUpdate is the latest bounded state. QR is present only while
// waiting for the phone and is replaced rather than accumulated.
type ConnectionUpdate struct {
	state ConnectionState
	qr    QRFrame
	hasQR bool
}

// State returns the current connection state.
func (update ConnectionUpdate) State() ConnectionState { return update.state }

// QR returns the current bounded QR raster when one is available.
func (update ConnectionUpdate) QR() (QRFrame, bool) { return update.qr, update.hasQR }

// NewConnectionUpdate creates a status-only update.
func NewConnectionUpdate(state ConnectionState) ConnectionUpdate {
	return ConnectionUpdate{state: state}
}

// NewPairingUpdate creates a waiting-for-phone update with a bounded QR frame.
func NewPairingUpdate(frame QRFrame) ConnectionUpdate {
	return ConnectionUpdate{state: ConnectionWaitingForQR, qr: frame, hasQR: true}
}

type protocolEventKind uint8

const (
	protocolConnected protocolEventKind = iota + 1
	protocolDisconnected
	protocolFailed
	protocolLoggedOut
)

type protocolEvent struct {
	kind  protocolEventKind
	cause error
}

type connectionClient interface {
	Linked() bool
	GetQRChannel(context.Context) (<-chan whatsmeow.QRChannelItem, error)
	Connect(context.Context) error
	Events() <-chan protocolEvent
	Disconnect()
	Close() error
}

type realtimeSourceProvider interface {
	RealtimeSource() *RealtimeSource
}

type connectionError struct {
	kind  error
	cause error
}

func (connectionErr *connectionError) Error() string {
	if connectionErr == nil || connectionErr.kind == nil {
		return ErrConnectionFailed.Error()
	}
	return connectionErr.kind.Error()
}

func (connectionErr *connectionError) Unwrap() error {
	if connectionErr == nil {
		return nil
	}
	return connectionErr.cause
}

func (connectionErr *connectionError) Is(target error) bool {
	return connectionErr != nil && (target == connectionErr.kind || errors.Is(connectionErr.cause, target))
}

// Connection owns one single-use WhatsApp authentication/connection client.
// Its public update channel holds only the latest current state.
type Connection struct {
	client connectionClient

	updates chan ConnectionUpdate
	stop    chan struct{}
	started atomic.Bool

	disconnectOnce sync.Once
	closeOnce      sync.Once
	closeErr       error
	afterPublish   func(ConnectionUpdate)
}

// NewConnection opens the persistent device store and constructs the real
// WhatsApp client without connecting it.
func NewConnection(ctx context.Context, sessionPath string) (*Connection, error) {
	client, err := newWhatsmeowConnectionClient(ctx, sessionPath)
	if err != nil {
		return nil, err
	}
	return newConnection(client)
}

// SessionLinked inspects and closes the persistent device store without
// connecting to WhatsApp. No client remains open after this function returns.
func SessionLinked(ctx context.Context, sessionPath string) (bool, error) {
	client, err := newWhatsmeowConnectionClient(ctx, sessionPath)
	if err != nil {
		return false, err
	}
	linked := client.Linked()
	if err := client.Close(); err != nil {
		return false, err
	}
	return linked, nil
}

func newConnection(client connectionClient) (*Connection, error) {
	if client == nil || client.Events() == nil {
		return nil, &connectionError{kind: ErrConnectionRejected}
	}
	return &Connection{
		client:  client,
		updates: make(chan ConnectionUpdate, connectionUpdateSize),
		stop:    make(chan struct{}),
	}, nil
}

// Linked reports whether the loaded whatsmeow device store has a linked JID.
func (connection *Connection) Linked() bool {
	return connection != nil && connection.client != nil && connection.client.Linked()
}

// Updates returns the bounded, coalescible current-state stream.
func (connection *Connection) Updates() <-chan ConnectionUpdate {
	if connection == nil {
		return nil
	}
	return connection.updates
}

// RealtimeSource returns the bounded message source owned by the same
// long-lived client as this connection. Test-only lifecycle clients may not
// provide one.
func (connection *Connection) RealtimeSource() *RealtimeSource {
	if connection == nil || connection.client == nil {
		return nil
	}
	provider, ok := connection.client.(realtimeSourceProvider)
	if !ok {
		return nil
	}
	return provider.RealtimeSource()
}

// Run performs pairing when required, establishes the connection, and remains
// alive until cancellation, explicit disconnect, or an irrecoverable failure.
func (connection *Connection) Run(ctx context.Context) error {
	if connection == nil || ctx == nil || connection.client == nil || connection.updates == nil || connection.stop == nil {
		return &connectionError{kind: ErrConnectionRejected}
	}
	if !connection.started.CompareAndSwap(false, true) {
		return &connectionError{kind: ErrConnectionStarted}
	}
	defer close(connection.updates)
	defer connection.Disconnect()
	if err := ctx.Err(); err != nil {
		return err
	}

	linked := connection.client.Linked()
	var qrEvents <-chan whatsmeow.QRChannelItem
	if linked {
		connection.publish(ConnectionUpdate{state: ConnectionConnecting})
	} else {
		connection.publish(ConnectionUpdate{state: ConnectionWaitingForQR})
		var err error
		qrEvents, err = connection.client.GetQRChannel(ctx)
		if err != nil || qrEvents == nil {
			connection.publish(ConnectionUpdate{state: ConnectionFailed})
			return &connectionError{kind: ErrPairingFailed, cause: err}
		}
	}

	if err := connection.client.Connect(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		connection.publish(ConnectionUpdate{state: ConnectionFailed})
		return &connectionError{kind: ErrConnectionFailed, cause: err}
	}

	paired := linked
	connected := false
	clientEvents := connection.client.Events()
	for {
		select {
		case <-ctx.Done():
			connection.publish(ConnectionUpdate{state: ConnectionDisconnected})
			return ctx.Err()
		case <-connection.stop:
			connection.publish(ConnectionUpdate{state: ConnectionDisconnected})
			return nil
		case item, ok := <-qrEvents:
			if !ok {
				qrEvents = nil
				if !paired && !connected {
					connection.publish(ConnectionUpdate{state: ConnectionFailed})
					return &connectionError{kind: ErrPairingFailed}
				}
				continue
			}
			switch item.Event {
			case whatsmeow.QRChannelEventCode:
				frame, err := encodePairingQR(item.Code)
				if err != nil {
					connection.publish(ConnectionUpdate{state: ConnectionFailed})
					return &connectionError{kind: ErrPairingFailed, cause: err}
				}
				connection.publish(ConnectionUpdate{state: ConnectionWaitingForQR, qr: frame, hasQR: true})
			case qrEventSuccess:
				paired = true
				qrEvents = nil
				connection.publish(ConnectionUpdate{state: ConnectionConnecting})
			default:
				connection.publish(ConnectionUpdate{state: ConnectionFailed})
				return &connectionError{kind: ErrPairingFailed, cause: item.Error}
			}
		case event, ok := <-clientEvents:
			if !ok {
				connection.publish(ConnectionUpdate{state: ConnectionFailed})
				return &connectionError{kind: ErrConnectionClosed}
			}
			switch event.kind {
			case protocolConnected:
				connected = true
				paired = true
				qrEvents = nil
				connection.publish(ConnectionUpdate{state: ConnectionConnected})
			case protocolDisconnected:
				if !connected {
					connection.publish(ConnectionUpdate{state: ConnectionFailed})
					return &connectionError{kind: ErrConnectionFailed, cause: event.cause}
				}
				connected = false
				connection.publish(ConnectionUpdate{state: ConnectionDisconnected})
			case protocolFailed, protocolLoggedOut:
				connection.publish(ConnectionUpdate{state: ConnectionFailed})
				return &connectionError{kind: ErrConnectionFailed, cause: event.cause}
			default:
				connection.publish(ConnectionUpdate{state: ConnectionFailed})
				return &connectionError{kind: ErrConnectionFailed}
			}
		}
	}
}

func (connection *Connection) publish(update ConnectionUpdate) {
	select {
	case <-connection.updates:
	default:
	}
	select {
	case connection.updates <- update:
	default:
	}
	if connection.afterPublish != nil {
		connection.afterPublish(update)
	}
}

// Disconnect requests shutdown and disconnects the underlying client once.
func (connection *Connection) Disconnect() {
	if connection == nil {
		return
	}
	connection.disconnectOnce.Do(func() {
		close(connection.stop)
		if connection.client != nil {
			connection.client.Disconnect()
		}
	})
}

// Close disconnects and closes the persistent session container once.
func (connection *Connection) Close() error {
	if connection == nil {
		return nil
	}
	connection.closeOnce.Do(func() {
		connection.Disconnect()
		if connection.client != nil {
			connection.closeErr = connection.client.Close()
		}
	})
	return connection.closeErr
}

func encodePairingQR(code string) (QRFrame, error) {
	if code == "" || len(code) > maxPairingCodeBytes {
		return QRFrame{}, ErrPairingFailed
	}
	value, err := qrcode.New(code, qrcode.Low)
	if err != nil {
		return QRFrame{}, err
	}
	bitmap := value.Bitmap()
	if len(bitmap) == 0 || len(bitmap) > MaxPairingQRSize {
		return QRFrame{}, ErrPairingFailed
	}
	frame := QRFrame{size: uint16(len(bitmap))}
	for y, row := range bitmap {
		if len(row) != len(bitmap) {
			return QRFrame{}, ErrPairingFailed
		}
		for x, dark := range row {
			frame.cells[y*len(bitmap)+x] = dark
		}
	}
	return frame, nil
}
