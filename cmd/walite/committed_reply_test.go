package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/tui"
	"github.com/antoinebaudrimont-beep/walite/internal/wa"
	"github.com/gdamore/tcell/v2"
)

// Capture immutable frames on the sole drawing goroutine. SimulationScreen's
// GetContents exposes its live cells, so reading them after a Show notification
// could race the next committed frame.
type committedReplyScreen struct {
	*startupObservedScreen
	frames chan string
}

func (screen *committedReplyScreen) Show() {
	screen.SimulationScreen.Show()
	screen.frames <- startupScreenText(screen)
}

func TestConnectedCommittedPlainAndReplyRenderThroughApplication(t *testing.T) {
	for _, reply := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "reply"}[reply], func(t *testing.T) {
			isolateApplicationFiles(t)
			connection, err := wa.NewConnection(context.Background(), filepath.Join(t.TempDir(), "session.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close() // no Connect/Run: transport is a deterministic fake
			sender := &connectedTestSender{at: time.Date(2100, 8, 1, 12, 0, 0, 0, time.UTC)}
			application, err := newConnectedApplicationService(connection.RealtimeSource(), sender, connection.ReadReceiptSender(), openConnectedTestCache(t))
			if err != nil {
				t.Fatal(err)
			}
			request := tui.SendRequest{ChatID: "123@lid", Text: "reply presentation test 👋"}
			var quote model.TextQuote
			if reply {
				request.ReplyToID, request.ReplyToText, request.ReplyToFromMe = "synthetic-original", "synthetic Café original 👋", true
				quote, err = model.NewTextQuote(request.ReplyToID, request.ReplyToText, request.ReplyToFromMe)
				if err != nil {
					t.Fatal(err)
				}
			}
			screen := &committedReplyScreen{startupObservedScreen: newStartupObservedScreen(), frames: make(chan string, 4)}
			renderer := func(ctx context.Context, _ tcell.Screen, input tui.Input) error {
				if err := input.Send(ctx, request); err != nil {
					return err
				}
				if result := <-input.SendResults; result.Failed {
					return errors.New("transport fixture failed")
				}
				// Keep the real application live adapter and TUI consumer in place:
				// no synthetic local append or manually injected presentation event.
				done := make(chan error, 1)
				go func() { done <- tui.Run(ctx, screen, input) }()
				var rendered string
			waitingForCommit:
				for {
					select {
					case rendered = <-screen.frames:
						if strings.Contains(rendered, request.Text) {
							break waitingForCommit
						}
					case err := <-done:
						if err != nil {
							return err
						}
						return errors.New("TUI exited before commit became visible")
					}
				}
				screen.InjectKey(tcell.KeyCtrlC, 0, tcell.ModNone)
				if err := <-done; err != nil {
					return err
				}
				if strings.Count(rendered, request.Text) != 1 || strings.Contains(rendered, "↪ ") != reply {
					return errors.New("plain/reply presentation or local count incorrect")
				}
				if reply && !strings.Contains(rendered, "↪ "+request.ReplyToText) {
					return errors.New("committed quoted excerpt not visible")
				}
				return nil
			}
			if err := runStartedApplication(context.Background(), screen, tui.DefaultOptions(), application, renderer); err != nil {
				t.Fatal(err)
			}
			id, _ := model.NewChatID(request.ChatID)
			messages, _, err := application.(*connectedApplicationService).store.Page(context.Background(), id, model.NoCursor(), 10)
			if err != nil || len(messages) != 1 || messages[0].Quote() != quote || sender.calls.Load() != 1 {
				t.Fatal("committed quote/count lost")
			}
		})
	}
}

func TestCommittedQuoteSurvivesLiveAndSnapshotAdapters(t *testing.T) {
	now := time.Date(2100, 8, 1, 12, 0, 0, 0, time.UTC)
	for _, fromMe := range []bool{false, true} {
		for _, text := range []string{"synthetic Café é 日本語 👋", strings.Repeat("x", model.MaxQuoteTextBytes-1) + "👋 tail"} {
			quote, err := model.NewTextQuote("quoted-ID", text, fromMe)
			if err != nil {
				t.Fatal(err)
			}
			message := mustSnapshotMessage(t, "chat", "reply", now, true, "body 👋").WithQuote(quote)
			committed, err := model.NewLiveMessageCommitted(message, 7, now)
			if err != nil {
				t.Fatal(err)
			}
			live, ok := adaptLiveMessage(committed)
			if !ok || live.ReplyToID != quote.MessageID().String() || live.ReplyToText != quote.Text() || live.ReplyToFromMe != quote.FromMe() || live.Text != message.Text() {
				t.Fatal("live adapter lost exact bounded quote")
			}
			chat := mustSnapshotChat(t, "chat", "Synthetic", false, 7, now)
			stub := &snapshotStub{chats: []model.Chat{chat}, messages: map[string][]model.Message{"chat": {message}}}
			initial, err := buildInitialTUIState(context.Background(), stub)
			if err != nil {
				t.Fatal(err)
			}
			if len(initial.Chats[0].Messages) != 0 {
				t.Fatal("startup summary unexpectedly preloaded messages")
			}
			loaded, err := buildChatLoadResult(context.Background(), stub, tui.ChatLoadRequest{ChatID: "chat"})
			if err != nil {
				t.Fatal(err)
			}
			got := loaded.Messages[0]
			if got.ReplyToID != quote.MessageID().String() || got.ReplyToText != quote.Text() || got.ReplyToFromMe != quote.FromMe() {
				t.Fatal("snapshot adapter lost bounded quote")
			}
		}
	}
}

func TestCommittedMediaQuoteSurvivesLiveAndSnapshotAdapters(t *testing.T) {
	now := time.Date(2100, 8, 1, 12, 0, 0, 0, time.UTC)
	media, _ := model.NewMedia(model.MediaImage, "", "image/jpeg")
	quote, _ := model.NewMediaQuote("media-original", "Holiday Café 👋", false, media)
	message := mustSnapshotMessage(t, "chat", "reply", now, true, "body 👋").WithQuote(quote)
	committed, err := model.NewLiveMessageCommitted(message, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	live, ok := adaptLiveMessage(committed)
	if !ok || live.ReplyToID != "media-original" || live.ReplyToText != quote.Text() || live.ReplyMediaKind != "image" || live.ReplyMediaMIME != "image/jpeg" {
		t.Fatalf("live=%+v", live)
	}
	chat := mustSnapshotChat(t, "chat", "Synthetic", false, 1, now)
	stub := &snapshotStub{chats: []model.Chat{chat}, messages: map[string][]model.Message{"chat": {message}}}
	loaded, err := buildChatLoadResult(context.Background(), stub, tui.ChatLoadRequest{ChatID: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	got := loaded.Messages[0]
	if got.ReplyToID != "media-original" || got.ReplyToText != quote.Text() || got.ReplyMediaKind != "image" || got.ReplyMediaMIME != "image/jpeg" {
		t.Fatalf("snapshot=%+v", got)
	}
}
