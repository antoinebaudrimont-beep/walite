package tui

import (
	"context"
	"errors"

	"github.com/gdamore/tcell/v2"
)

const (
	MaxPairingQRSize = 185
	pairingQRCells   = MaxPairingQRSize * MaxPairingQRSize
)

var (
	ErrConnectionViewExit    = errors.New("connection view exit")
	ErrConnectionViewFailed  = errors.New("connection failed")
	ErrConnectionUpdatesDone = errors.New("connection updates closed")
)

type ConnectionState uint8

const (
	ConnectionDisconnected ConnectionState = iota
	ConnectionWaitingForQR
	ConnectionConnecting
	ConnectionConnected
	ConnectionFailed
)

type PairingQRFrame struct {
	size  uint16
	cells [pairingQRCells]bool
}

func NewPairingQRFrame(size int, cells []bool) (PairingQRFrame, error) {
	if size <= 0 || size > MaxPairingQRSize || len(cells) != size*size {
		return PairingQRFrame{}, errors.New("pairing QR frame rejected")
	}
	var frame PairingQRFrame
	frame.size = uint16(size)
	copy(frame.cells[:], cells)
	return frame, nil
}

func (frame PairingQRFrame) Size() int { return int(frame.size) }

func (frame PairingQRFrame) Cell(x, y int) bool {
	size := frame.Size()
	return x >= 0 && y >= 0 && x < size && y < size && frame.cells[y*size+x]
}

type ConnectionUpdate struct {
	State ConnectionState
	QR    PairingQRFrame
	HasQR bool
}

type ConnectionInput struct {
	Linked  bool
	Updates <-chan ConnectionUpdate
}

// RunConnection owns the screen lifecycle while presenting connection state.
func RunConnection(ctx context.Context, screen tcell.Screen, input ConnectionInput) error {
	if ctx == nil || screen == nil || input.Updates == nil {
		return errors.New("connection view rejected")
	}
	if err := screen.Init(); err != nil {
		return err
	}
	defer screen.Fini()
	return RunConnectionInitialized(ctx, screen, input)
}

// RunConnectionInitialized presents connection state without owning the
// already-initialized screen lifecycle.
func RunConnectionInitialized(ctx context.Context, screen tcell.Screen, input ConnectionInput) error {
	if ctx == nil || screen == nil || input.Updates == nil {
		return errors.New("connection view rejected")
	}
	state := ConnectionWaitingForQR
	if input.Linked {
		state = ConnectionConnecting
	}
	current := ConnectionUpdate{State: state}
	drawConnection(screen, current)
	screen.Show()

	events := make(chan tcell.Event, 1)
	stopEvents := make(chan struct{})
	eventsDone := make(chan struct{})
	go func() {
		defer close(eventsDone)
		screen.ChannelEvents(events, stopEvents)
	}()
	defer func() {
		close(stopEvents)
		<-eventsDone
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case update, ok := <-input.Updates:
			if !ok {
				return ErrConnectionUpdatesDone
			}
			if err := validateConnectionUpdate(update); err != nil {
				return err
			}
			if update != current {
				current = update
				drawConnection(screen, current)
				screen.Show()
			}
			switch update.State {
			case ConnectionConnected:
				return nil
			case ConnectionFailed:
				return ErrConnectionViewFailed
			}
		case event, ok := <-events:
			if !ok {
				return ErrConnectionViewExit
			}
			switch event := event.(type) {
			case *tcell.EventKey:
				if event.Key() == tcell.KeyEscape || event.Key() == tcell.KeyCtrlC {
					return ErrConnectionViewExit
				}
			case *tcell.EventResize:
				screen.Sync()
				drawConnection(screen, current)
				screen.Show()
			}
		}
	}
}

func validateConnectionUpdate(update ConnectionUpdate) error {
	if update.State > ConnectionFailed {
		return errors.New("connection update rejected")
	}
	if update.HasQR && update.QR.Size() == 0 {
		return errors.New("connection QR rejected")
	}
	return nil
}

func drawConnection(screen tcell.Screen, update ConnectionUpdate) {
	screen.Clear()
	screen.HideCursor()
	width, height := screen.Size()
	putText(screen, 0, 0, width, "walite", tcell.StyleDefault.Bold(true))
	putText(screen, 0, 2, width, "Link WhatsApp", tcell.StyleDefault.Bold(true))

	status := connectionStatus(update.State)
	escapeAction := "Esc quit"
	if update.HasQR && update.State == ConnectionWaitingForQR {
		status = "Waiting for phone… This window will close after pairing."
		escapeAction = "Esc cancel"
		if pairingQRFits(screen, update.QR, 5, height-3) {
			putText(screen, 0, 4, width, "Scan using WhatsApp → Linked devices → Link a device", tcell.StyleDefault)
			drawPairingQR(screen, update.QR, 5, height-3)
		} else {
			putText(screen, 0, 4, width, "Terminal too small to display pairing QR.", tcell.StyleDefault)
			putText(screen, 0, 5, width, "Enlarge the terminal and try again.", tcell.StyleDefault)
		}
	}
	putText(screen, 0, height-2, width, status, tcell.StyleDefault)
	putText(screen, 0, height-1, width, escapeAction, tcell.StyleDefault.Dim(true))
}

func connectionStatus(state ConnectionState) string {
	switch state {
	case ConnectionWaitingForQR:
		return "Waiting for phone…"
	case ConnectionConnecting:
		return "Connecting…"
	case ConnectionConnected:
		return "Connected"
	case ConnectionFailed:
		return "Connection failed"
	default:
		return "Disconnected"
	}
}

func drawPairingQR(screen tcell.Screen, frame PairingQRFrame, top, bottom int) bool {
	if !pairingQRFits(screen, frame, top, bottom) {
		return false
	}
	size := frame.Size()
	qrHeight := (size + 1) / 2
	width, _ := screen.Size()
	left := (width - size) / 2
	for cellY := 0; cellY < qrHeight; cellY++ {
		for x := 0; x < size; x++ {
			topDark := frame.Cell(x, cellY*2)
			bottomY := cellY*2 + 1
			bottomDark := bottomY < size && frame.Cell(x, bottomY)
			cell, style := halfBlockCell(topDark, bottomDark)
			screen.SetContent(left+x, top+cellY, cell, nil, style)
		}
	}
	return true
}

func pairingQRFits(screen tcell.Screen, frame PairingQRFrame, top, bottom int) bool {
	size := frame.Size()
	if size == 0 || !screen.CanDisplay('\u2580', true) {
		return false
	}
	width, _ := screen.Size()
	return size <= width && top+(size+1)/2 <= bottom
}

func halfBlockCell(topDark, bottomDark bool) (rune, tcell.Style) {
	foreground := tcell.ColorWhite
	if topDark {
		foreground = tcell.ColorBlack
	}
	background := tcell.ColorWhite
	if bottomDark {
		background = tcell.ColorBlack
	}
	return '\u2580', tcell.StyleDefault.Foreground(foreground).Background(background)
}
