package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/store"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/rivo/uniseg"
)

// Copy full graphemes on the drawing goroutine, not SimulationScreen's mutable
// backing cells. The test can then inspect exact terminal columns race-free.
type incomingReplyFrame struct {
	cells         []string
	width, height int
}

type incomingReplyScreen struct {
	*startupObservedScreen
	frames chan incomingReplyFrame
}

func (screen *incomingReplyScreen) Show() {
	screen.SimulationScreen.Show()
	cells, width, height := screen.GetContents()
	frame := incomingReplyFrame{cells: make([]string, len(cells)), width: width, height: height}
	for i, cell := range cells {
		frame.cells[i] = string(cell.Runes)
	}
	screen.frames <- frame
}

func (frame incomingReplyFrame) containsAt(x, y int, text string) bool {
	clusters := uniseg.NewGraphemes(text)
	for clusters.Next() {
		if x < 0 || x >= frame.width || y < 0 || y >= frame.height || frame.cells[y*frame.width+x] != clusters.Str() {
			return false
		}
		x += clusters.Width()
	}
	return true
}

func (frame incomingReplyFrame) findText(t *testing.T, text string) (int, int) {
	t.Helper()
	foundX, foundY, count := 0, 0, 0
	for y := 0; y < frame.height; y++ {
		for x := 0; x < frame.width; x++ {
			if frame.containsAt(x, y, text) {
				foundX, foundY, count = x, y, count+1
			}
		}
	}
	if count != 1 {
		t.Fatalf("visible occurrences of %q=%d, want exactly one", text, count)
	}
	return foundX, foundY
}

func TestIncomingCommittedReplyUsesDirectionalQuoteRenderer(t *testing.T) {
	for _, group := range []bool{false, true} {
		for _, timestamps := range []bool{false, true} {
			t.Run(map[bool]string{false: "direct", true: "group"}[group]+map[bool]string{false: "/no-time", true: "/time"}[timestamps], func(t *testing.T) {
				isolateApplicationFiles(t)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				memory, err := store.NewMemory(2)
				if err != nil {
					t.Fatal(err)
				}
				at := time.Date(2100, 1, 1, 15, 56, 0, 0, time.UTC)
				excerpt := "Café é 日本語 👋"
				original := mustSnapshotMessage(t, "chat", "original", at, true, excerpt)
				batch, _ := model.NewWriteBatch(model.WriteRealtime, []model.Message{original})
				if _, err := memory.WriteRealtime(ctx, batch); err != nil {
					t.Fatal(err)
				}
				chat := mustSnapshotChat(t, "chat", "Synthetic chat", group, 0, at)
				application := newLiveApplicationService([]model.Chat{chat}, map[string][]model.Message{"chat": {original}})
				screen := &incomingReplyScreen{startupObservedScreen: newStartupObservedScreen(), frames: make(chan incomingReplyFrame, 8)}
				options := tui.DefaultOptions()
				options.ShowTimestamps = timestamps
				done := make(chan error, 1)
				go func() { done <- runStartedApplication(ctx, screen, options, application, tui.Run) }()
				t.Cleanup(func() {
					cancel()
					if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
						t.Error(err)
					}
				})
				nextFrame := func() incomingReplyFrame {
					t.Helper()
					select {
					case frame := <-screen.frames:
						return frame
					case <-ctx.Done():
						t.Fatal("no committed frame before deadline")
						return incomingReplyFrame{}
					}
				}
				nextFrame()
				quote, err := model.NewTextQuote("original", excerpt, true)
				if err != nil {
					t.Fatal(err)
				}
				quoteLine := "↪ " + excerpt
				prefix := 0
				if timestamps {
					quoteLine = "↪ 15:56 " + excerpt
					prefix = 7
				}
				var final incomingReplyFrame
				for index, id := range []string{"incoming-reply", "outgoing-reply", "ordinary"} {
					fromMe := id == "outgoing-reply"
					body := id + " 👋"
					parent, err := model.NewMessage(model.MessageInput{
						ChatID: "chat", MessageID: id, SentAt: at.Add(time.Duration(index+1) * time.Minute),
						FromMe: fromMe, Text: body, IsGroup: group, SenderID: "synthetic-peer",
					})
					if err != nil {
						t.Fatal(err)
					}
					if id != "ordinary" {
						parent = parent.WithQuote(quote)
					}
					event, err := model.NewEvent(parent, parent.SentAt())
					if err != nil {
						t.Fatal(err)
					}
					batch, err := model.NewWriteBatch(model.WriteRealtime, []model.Message{event.Message()})
					if err != nil {
						t.Fatal(err)
					}
					committed, err := memory.WriteRealtime(ctx, batch)
					if err != nil || committed.Len() != 1 {
						t.Fatal("not one committed message", err)
					}
					live, _ := committed.At(0)
					presentation, ok := adaptLiveMessage(live)
					if !ok || presentation.FromMe != fromMe || presentation.ReplyToID != parent.Quote().MessageID().String() || presentation.ReplyToText != parent.Quote().Text() || presentation.ReplyToFromMe != parent.Quote().FromMe() {
						t.Fatal("Event -> store -> LiveEvent -> TUI adapter lost incoming quote")
					}
					select {
					case application.emit <- live:
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
					final = nextFrame()
					x, y := final.findText(t, body)
					wantX := 35 + prefix // unchanged 100-column layout: separator at 33
					if fromMe {
						wantX = 98 - max(uniseg.StringWidth(quoteLine), uniseg.StringWidth(body))
					}
					if x != wantX {
						t.Fatalf("%s body column=%d want=%d", id, x, wantX)
					}
					if id != "ordinary" && !final.containsAt(x, y-1, quoteLine) {
						t.Fatal("existing quote marker/excerpt not visible immediately above aligned body")
					}
					// Rich and quote-less duplicates cannot publish a second message.
					for _, duplicate := range []model.Message{parent, parent.WithQuote(model.TextQuote{})} {
						batch, _ := model.NewWriteBatch(model.WriteRealtime, []model.Message{duplicate})
						if extra, err := memory.WriteRealtime(ctx, batch); err != nil || extra.Len() != 0 {
							t.Fatal("duplicate published", err)
						}
					}
				}
				for _, body := range []string{"incoming-reply 👋", "outgoing-reply 👋", "ordinary 👋"} {
					final.findText(t, body)
				}
				markers := 0
				for _, cell := range final.cells {
					if cell == "↪" {
						markers++
					}
				}
				if markers != 2 {
					t.Fatalf("quote markers=%d; plain message changed or replies duplicated", markers)
				}
				messages, _, err := memory.Page(ctx, original.ChatID(), model.NoCursor(), 10)
				if err != nil || len(messages) != 4 || messages[1].Quote() != quote || messages[2].Quote() != quote {
					t.Fatal("stored quotes/count changed", err)
				}
			})
		}
	}
}
