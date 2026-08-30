package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestConnectionViewShowsPairingQRAndNewestFrame(t *testing.T) {
	updates := make(chan ConnectionUpdate, 2)
	screen := newObservedScreen(80, 28)
	result := make(chan error, 1)
	go func() {
		result <- RunConnection(context.Background(), screen, ConnectionInput{Updates: updates})
	}()
	<-screen.shown

	first := testPairingQR(t, false)
	second := testPairingQR(t, true)
	updates <- ConnectionUpdate{State: ConnectionWaitingForQR, QR: first, HasQR: true}
	<-screen.shown
	firstFrame := screenStyleFingerprint(screen)
	updates <- ConnectionUpdate{State: ConnectionWaitingForQR, QR: second, HasQR: true}
	<-screen.shown
	secondFrame := screenStyleFingerprint(screen)
	if firstFrame == secondFrame {
		t.Fatal("new QR did not replace previous frame")
	}
	rendered := screenText(screen)
	for _, want := range []string{"Link WhatsApp", "Linked devices", "Waiting for phone", "close after pairing", "Esc cancel"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("pairing frame missing %q:\n%s", want, rendered)
		}
	}

	updates <- ConnectionUpdate{State: ConnectionConnected}
	<-screen.shown
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	assertRunRestoredTerminal(t, screen)
}

func TestConnectionViewLinkedStartupDoesNotFlashQR(t *testing.T) {
	updates := make(chan ConnectionUpdate, 1)
	screen := newObservedScreen(80, 25)
	result := make(chan error, 1)
	go func() {
		result <- RunConnection(context.Background(), screen, ConnectionInput{Linked: true, Updates: updates})
	}()
	<-screen.shown
	if text := screenText(screen); !strings.Contains(text, "Connecting") || strings.Contains(text, "Scan using") {
		t.Fatalf("linked first frame:\n%s", text)
	}
	updates <- ConnectionUpdate{State: ConnectionConnected}
	<-screen.shown
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestConnectionViewDoesNotRedrawWhileIdle(t *testing.T) {
	updates := make(chan ConnectionUpdate, 1)
	screen := newObservedScreen(80, 25)
	result := make(chan error, 1)
	go func() { result <- RunConnection(context.Background(), screen, ConnectionInput{Updates: updates}) }()
	<-screen.shown
	if err := screen.PostEvent(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone)); err != nil {
		t.Fatal(err)
	}
	if err := screen.PostEvent(tcell.NewEventResize(79, 25)); err != nil {
		t.Fatal(err)
	}
	<-screen.shown
	if got := screen.showCount.Load(); got != 2 {
		t.Fatalf("shows=%d want 2", got)
	}
	updates <- ConnectionUpdate{State: ConnectionConnected}
	<-screen.shown
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestConnectionViewResizeWhileQRVisible(t *testing.T) {
	updates := make(chan ConnectionUpdate, 2)
	screen := newObservedScreen(80, 28)
	result := make(chan error, 1)
	go func() { result <- RunConnection(context.Background(), screen, ConnectionInput{Updates: updates}) }()
	<-screen.shown
	updates <- ConnectionUpdate{State: ConnectionWaitingForQR, QR: testPairingQR(t, false), HasQR: true}
	<-screen.shown
	resizeObservedScreen(t, screen, 30, 8)
	if text := screenText(screen); !strings.Contains(text, "Terminal too small to display") ||
		!strings.Contains(text, "Enlarge the terminal and try") {
		t.Fatalf("compact pairing view:\n%s", text)
	}
	updates <- ConnectionUpdate{State: ConnectionConnected}
	<-screen.shown
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestActualPairingQRUsesConfiguredWindowAndOneColumnTooSmallFails(t *testing.T) {
	frame := testPairingQRSize(t, 69)
	fitting := initializedSimulationScreen(t, 80, 46)
	if !pairingQRFits(fitting, frame, 5, 46-3) {
		t.Fatal("69x35 QR did not fit configured 80x46 pairing window")
	}
	oneColumnShort := initializedSimulationScreen(t, 68, 46)
	if pairingQRFits(oneColumnShort, frame, 5, 46-3) {
		t.Fatal("69-column QR fit a 68-column terminal")
	}
}

func TestResizeMakesPreviouslyNonFittingActualQRVisible(t *testing.T) {
	updates := make(chan ConnectionUpdate, 2)
	screen := newObservedScreen(68, 46)
	result := make(chan error, 1)
	go func() { result <- RunConnection(context.Background(), screen, ConnectionInput{Updates: updates}) }()
	<-screen.shown
	updates <- ConnectionUpdate{State: ConnectionWaitingForQR, QR: testPairingQRSize(t, 69), HasQR: true}
	<-screen.shown
	if text := screenText(screen); !strings.Contains(text, "Terminal too small") || strings.ContainsRune(text, '\u2580') {
		t.Fatalf("non-fitting frame:\n%s", text)
	}
	resizeObservedScreen(t, screen, 80, 46)
	if text := screenText(screen); strings.Contains(text, "Terminal too small") || !strings.ContainsRune(text, '\u2580') {
		t.Fatalf("resized pairing frame:\n%s", text)
	}
	updates <- ConnectionUpdate{State: ConnectionConnected}
	<-screen.shown
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestPairingQRHalfBlockGeometryPreservesEveryModule(t *testing.T) {
	for _, test := range []struct {
		name string
		size int
	}{{name: "even", size: 12}, {name: "odd", size: 13}} {
		t.Run(test.name, func(t *testing.T) {
			size := test.size
			cells := make([]bool, size*size)
			for y := 4; y < size-4; y++ {
				for x := 4; x < size-4; x++ {
					cells[y*size+x] = (x+2*y)%3 == 0
				}
			}
			frame, err := NewPairingQRFrame(size, cells)
			if err != nil {
				t.Fatal(err)
			}
			const left, top = 3, 1
			renderedHeight := (size + 1) / 2
			screenWidth := size + 2*left
			screenHeight := top + renderedHeight + 1
			screen := initializedSimulationScreen(t, screenWidth, screenHeight)
			if !drawPairingQR(screen, frame, top, top+renderedHeight) {
				t.Fatal("aspect-correct QR did not fit")
			}
			renderedCells := 0
			for y := 0; y < screenHeight; y++ {
				for x := 0; x < screenWidth; x++ {
					main, _, _, _ := screen.GetContent(x, y)
					if main != '\u2580' {
						continue
					}
					renderedCells++
					if x < left || x >= left+size || y < top || y >= top+renderedHeight {
						t.Fatalf("half block outside %dx%d QR bounds at %d,%d", size, renderedHeight, x, y)
					}
				}
			}
			if want := size * renderedHeight; renderedCells != want {
				t.Fatalf("rendered cells=%d want %d (%dx%d)", renderedCells, want, size, renderedHeight)
			}

			mapped := 0
			for cellY := 0; cellY < renderedHeight; cellY++ {
				for x := 0; x < size; x++ {
					main, _, style, cellWidth := screen.GetContent(left+x, top+cellY)
					if main != '\u2580' || cellWidth != 1 {
						t.Fatalf("cell %d,%d rune=%q width=%d", x, cellY, main, cellWidth)
					}
					foreground, background, _ := style.Decompose()
					topY := 2 * cellY
					if got := foreground == tcell.ColorBlack; got != cells[topY*size+x] {
						t.Fatalf("top module %d,%d=%t want %t", x, topY, got, cells[topY*size+x])
					}
					mapped++
					bottomY := topY + 1
					if bottomY < size {
						if got := background == tcell.ColorBlack; got != cells[bottomY*size+x] {
							t.Fatalf("bottom module %d,%d=%t want %t", x, bottomY, got, cells[bottomY*size+x])
						}
						mapped++
					} else if background != tcell.ColorWhite {
						t.Fatalf("missing lower module background=%v want white", background)
					}
				}
			}
			if mapped != size*size {
				t.Fatalf("mapped modules=%d want %d", mapped, size*size)
			}

			for offset := 0; offset < size; offset++ {
				for quiet := 0; quiet < 4; quiet++ {
					if renderedQRModuleDark(screen, left, top, offset, quiet) ||
						renderedQRModuleDark(screen, left, top, offset, size-1-quiet) ||
						renderedQRModuleDark(screen, left, top, quiet, offset) ||
						renderedQRModuleDark(screen, left, top, size-1-quiet, offset) {
						t.Fatalf("rendered four-module quiet zone is not white at offset %d", offset)
					}
				}
			}
		})
	}
}

func TestHalfBlockCellCoversAllModuleCombinations(t *testing.T) {
	for _, test := range []struct {
		name           string
		topDark        bool
		bottomDark     bool
		wantForeground tcell.Color
		wantBackground tcell.Color
	}{
		{name: "white white", wantForeground: tcell.ColorWhite, wantBackground: tcell.ColorWhite},
		{name: "black black", topDark: true, bottomDark: true, wantForeground: tcell.ColorBlack, wantBackground: tcell.ColorBlack},
		{name: "black white", topDark: true, wantForeground: tcell.ColorBlack, wantBackground: tcell.ColorWhite},
		{name: "white black", bottomDark: true, wantForeground: tcell.ColorWhite, wantBackground: tcell.ColorBlack},
	} {
		t.Run(test.name, func(t *testing.T) {
			main, style := halfBlockCell(test.topDark, test.bottomDark)
			foreground, background, _ := style.Decompose()
			if main != '\u2580' || foreground != test.wantForeground || background != test.wantBackground {
				t.Fatalf("cell=%q foreground=%v background=%v", main, foreground, background)
			}
		})
	}
}

func TestPairingQROddFinalRowUsesWhiteMissingLowerModule(t *testing.T) {
	frame, err := NewPairingQRFrame(1, []bool{true})
	if err != nil {
		t.Fatal(err)
	}
	screen := initializedSimulationScreen(t, 1, 1)
	if !drawPairingQR(screen, frame, 0, 1) {
		t.Fatal("one-module QR did not fit")
	}
	main, _, style, _ := screen.GetContent(0, 0)
	foreground, background, _ := style.Decompose()
	if main != '\u2580' || foreground != tcell.ColorBlack || background != tcell.ColorWhite {
		t.Fatalf("cell=%q foreground=%v background=%v", main, foreground, background)
	}
}

func TestConnectionViewFailureAndEscapeRestoreTerminal(t *testing.T) {
	t.Run("failure", func(t *testing.T) {
		updates := make(chan ConnectionUpdate, 1)
		screen := newObservedScreen()
		result := make(chan error, 1)
		go func() { result <- RunConnection(context.Background(), screen, ConnectionInput{Updates: updates}) }()
		<-screen.shown
		updates <- ConnectionUpdate{State: ConnectionFailed}
		<-screen.shown
		if err := <-result; !errors.Is(err, ErrConnectionViewFailed) {
			t.Fatalf("RunConnection=%v", err)
		}
		assertRunRestoredTerminal(t, screen)
	})
	t.Run("escape", func(t *testing.T) {
		updates := make(chan ConnectionUpdate)
		screen := newObservedScreen()
		result := make(chan error, 1)
		go func() { result <- RunConnection(context.Background(), screen, ConnectionInput{Updates: updates}) }()
		<-screen.shown
		if err := screen.PostEvent(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone)); err != nil {
			t.Fatal(err)
		}
		if err := <-result; !errors.Is(err, ErrConnectionViewExit) {
			t.Fatalf("RunConnection=%v", err)
		}
		assertRunRestoredTerminal(t, screen)
	})
}

func testPairingQR(t *testing.T, alternate bool) PairingQRFrame {
	t.Helper()
	const size = 21
	cells := make([]bool, size*size)
	for y := 4; y < size-4; y++ {
		for x := 4; x < size-4; x++ {
			cells[y*size+x] = (x+y)%2 == 0
			if alternate {
				cells[y*size+x] = x%3 == 0
			}
		}
	}
	frame, err := NewPairingQRFrame(size, cells)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func testPairingQRSize(t *testing.T, size int) PairingQRFrame {
	t.Helper()
	cells := make([]bool, size*size)
	for y := 4; y < size-4; y++ {
		for x := 4; x < size-4; x++ {
			cells[y*size+x] = (x+y)%2 == 0
		}
	}
	frame, err := NewPairingQRFrame(size, cells)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func screenStyleFingerprint(screen tcell.SimulationScreen) string {
	cells, _, _ := screen.GetContents()
	var fingerprint strings.Builder
	fingerprint.Grow(2 * len(cells))
	for _, cell := range cells {
		foreground, background, _ := cell.Style.Decompose()
		if foreground == tcell.ColorBlack {
			fingerprint.WriteByte('B')
		} else {
			fingerprint.WriteByte('W')
		}
		if background == tcell.ColorBlack {
			fingerprint.WriteByte('B')
		} else {
			fingerprint.WriteByte('W')
		}
	}
	return fingerprint.String()
}

func renderedQRModuleDark(screen tcell.Screen, left, top, x, y int) bool {
	_, _, style, _ := screen.GetContent(left+x, top+y/2)
	foreground, background, _ := style.Decompose()
	if y%2 == 0 {
		return foreground == tcell.ColorBlack
	}
	return background == tcell.ColorBlack
}
