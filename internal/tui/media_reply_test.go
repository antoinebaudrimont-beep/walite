package tui

import (
	"strings"
	"testing"
	"time"
)

func TestEveryVisibleMediaKindCanBecomeReplyTarget(t *testing.T) {
	tests := []struct {
		kind, name, mime, caption, summary string
	}{
		{mediaImage, "", "image/jpeg", "", "[Image]"},
		{mediaImage, "", "image/jpeg", "Holiday Café 👋", "[Image] Holiday Café 👋"},
		{mediaVideo, "", "video/mp4", "", "[Video]"},
		{mediaDocument, "report 日本語.pdf", "application/pdf", "", "[Document: report 日本語.pdf]"},
		{mediaAudio, "", "audio/ogg", "", "[Audio]"},
		{mediaSticker, "", "image/webp", "", "[Sticker]"},
	}
	for _, test := range tests {
		t.Run(test.summary, func(t *testing.T) {
			at := time.Unix(1, 0).UTC()
			state, err := chatStateFromInitial(InitialState{Chats: []InitialChat{{ID: "123@lid", Title: "Media", ActivityTime: at, Messages: []InitialMessage{{
				ID: "media-original", SentAt: at, Text: test.caption, BodyRetained: true,
				MediaKind: test.kind, MediaName: test.name, MediaMIME: test.mime,
			}}}}})
			if err != nil {
				t.Fatal(err)
			}
			var sent SendRequest
			view := viewModel{chats: state, options: DefaultOptions(), send: func(request SendRequest) error { sent = request; return nil }}
			if !focusNewestVisibleMessage(&view, 100, 24) || !chooseReplyTarget(&view) || !view.composer.insertText("reply 👋") || !submitOutgoingMessage(&view) {
				t.Fatal("media reply was not selectable/sendable")
			}
			if sent.ReplyToID != "media-original" || sent.ReplyToText != test.caption || sent.ReplyToFromMe ||
				sent.ReplyMediaKind != test.kind || sent.ReplyMediaName != test.name || sent.ReplyMediaMIME != test.mime {
				t.Fatalf("request=%+v", sent)
			}
		})
	}
}

func TestCommittedMediaQuoteRendersSummaryWithoutOriginal(t *testing.T) {
	for _, test := range []struct{ caption, summary string }{{"", "[Image]"}, {"Holiday Café 👋", "[Image] Holiday Café 👋"}} {
		at := time.Unix(1, 0).UTC()
		event := LiveMessage{ChatID: "chat", MessageID: "reply", SentAt: at, FromMe: true, Text: "reply body", BodyRetained: true,
			ReplyToID: "outside-working-set", ReplyToText: test.caption, ReplyMediaKind: mediaImage, ReplyMediaMIME: "image/jpeg",
			ActivityTime: at}
		state, _ := chatStateFromInitial(InitialState{})
		view := viewModel{chats: state, options: DefaultOptions(), terminalWidth: 100, terminalHeight: 24}
		if !applyLiveMessage(&view, event) {
			t.Fatal("committed media quote rejected")
		}
		screen := initializedSimulationScreen(t, 100, 24)
		draw(screen, &view)
		screen.Show()
		text := screenText(screen)
		if !strings.Contains(text, "↪ "+test.summary) || strings.Count(text, event.Text) != 1 {
			t.Fatalf("media quote summary missing:\n%s", text)
		}
	}
}
