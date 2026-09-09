package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestCommittedReplyUsesOfflinePresentation(t *testing.T) {
	for _, showTime := range []bool{false, true} {
		view := defaultDemoView()
		view.options.ShowTimestamps = showTime
		view.chats.chats[0].messages[0].text = "synthetic Café 日本語 👋"
		original := view.chats.chats[0].messages[0]
		chat, _ := view.chats.selectedChat()
		event := liveTestMessage(chat.id, "remote-reply-ID", 70, 70, true, "reply presentation 👋", 0)
		event.ReplyToID, event.ReplyToText, event.ReplyToFromMe = string(original.id), original.text, original.fromMe
		if !applyLiveMessage(&view, event) {
			t.Fatal("committed reply rejected")
		}
		reply, ok := view.chats.findMessageByID(view.chats.selectedIndex(), "remote-reply-ID")
		if !ok || !reply.hasReply || reply.replyToID != original.id || reply.replyText != original.text || reply.replyFromMe != original.fromMe {
			t.Fatal("live presentation lost quote")
		}
		screen := initializedSimulationScreen(t, 100, 8)
		drawMessage(screen, &view, view.chats.selectedIndex(), reply, 0, 0, 100, 8, false)
		screen.Show()
		got := replyScreenText(screen)
		if !strings.Contains(got, "↪ ") || !strings.Contains(got, original.text) || !strings.Contains(got, event.Text) {
			t.Fatalf("reply not visible:\n%s", got)
		}
		// Historical offline replies use the same renderer with only hasReply /
		// replyToID. The complete rendered frame must match that presentation.
		offline := reply
		offline.replyText, offline.replyFromMe = "", false
		screen.Clear()
		drawMessage(screen, &view, view.chats.selectedIndex(), offline, 0, 0, 100, 8, false)
		screen.Show()
		if got != replyScreenText(screen) {
			t.Fatal("live reply diverged from offline rendering")
		}
		before := *view.chats
		event.ReplyToID, event.ReplyToText, event.ReplyToFromMe = "", "", false
		if applyLiveMessage(&view, event) || *view.chats != before {
			t.Fatal("quote-less echo downgraded reply or duplicated it")
		}
	}
}

func TestCommittedReplyExcerptRendersWithoutOriginalInLiveAndSnapshot(t *testing.T) {
	for _, quotedFromMe := range []bool{false, true} {
		event := liveTestMessage("chat", "reply", 70, 70, true, "reply body 👋", 0)
		event.ReplyToID, event.ReplyToText, event.ReplyToFromMe = "outside-working-set", "synthetic Café é 日本語 👋", quotedFromMe
		state, _ := chatStateFromInitial(InitialState{})
		view := viewModel{chats: state, options: DefaultOptions(), terminalWidth: 100, terminalHeight: 24}
		if !applyLiveMessage(&view, event) {
			t.Fatal("live reply rejected")
		}
		screen := initializedSimulationScreen(t, 100, 24)
		draw(screen, &view)
		screen.Show()
		got := replyScreenText(screen)
		if !strings.Contains(got, "↪ "+event.ReplyToText) || strings.Count(got, event.Text) != 1 || strings.Contains(got, "original message unavailable") {
			t.Fatalf("bounded excerpt missing:\n%s", got)
		}
		initial := InitialState{Chats: []InitialChat{{ID: event.ChatID, Title: event.ChatID, ActivityTime: event.ActivityTime, Messages: []InitialMessage{{ID: event.MessageID, SentAt: event.SentAt, FromMe: true, Text: event.Text, BodyRetained: true, ReplyToID: event.ReplyToID, ReplyToText: event.ReplyToText, ReplyToFromMe: event.ReplyToFromMe}}}}}
		snapshot, err := chatStateFromInitial(initial)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.chats[0].messages[0] != state.chats[0].messages[0] {
			t.Fatal("snapshot lost committed quote")
		}
		view.chats = snapshot
		draw(screen, &view)
		screen.Show()
		if got != replyScreenText(screen) {
			t.Fatal("snapshot reply rendering differs from live reply")
		}
	}
}

func TestReplyPresentationMetadataStaysBounded(t *testing.T) {
	for _, test := range []struct {
		id, text, mediaKind, mediaName, mediaMIME string
		fromMe                                    bool
		valid                                     bool
	}{
		{valid: true},
		{id: strings.Repeat("i", maxReplyIDBytes), text: strings.Repeat("q", maxReplyTextBytes-4) + "👋", fromMe: true, valid: true},
		{id: "media", mediaKind: mediaImage, mediaMIME: "image/jpeg", valid: true},
		{id: "media", mediaKind: mediaDocument, mediaName: "report.pdf", mediaMIME: "application/pdf", valid: true},
		{id: "media", mediaKind: "unknown"},
		{id: "media", mediaKind: mediaImage, mediaMIME: strings.Repeat("m", maxMediaMIMEBytes+1)},
		{id: "original", text: strings.Repeat("q", maxReplyTextBytes+1)},
		{id: strings.Repeat("i", maxReplyIDBytes+1), text: "quote"},
		{id: "original", text: "\xff"},
		{id: "\xff", text: "quote"},
		{id: "original"},
		{text: "quote without ID"},
		{fromMe: true},
	} {
		event := liveTestMessage("chat", "reply", 70, 70, true, "reply", 0)
		event.ReplyToID, event.ReplyToText, event.ReplyToFromMe = test.id, test.text, test.fromMe
		event.ReplyMediaKind, event.ReplyMediaName, event.ReplyMediaMIME = test.mediaKind, test.mediaName, test.mediaMIME
		state, _ := chatStateFromInitial(InitialState{})
		view := viewModel{chats: state, options: DefaultOptions()}
		before := *state
		if applyLiveMessage(&view, event) != test.valid {
			t.Fatal("live quote validation changed")
		}
		if !test.valid && *state != before {
			t.Fatal("invalid quote mutated working set")
		}
		_, err := chatStateFromInitial(InitialState{Chats: []InitialChat{{ID: "chat", Messages: []InitialMessage{{ID: "reply", SentAt: event.SentAt, ReplyToID: test.id, ReplyToText: test.text, ReplyToFromMe: test.fromMe,
			ReplyMediaKind: test.mediaKind, ReplyMediaName: test.mediaName, ReplyMediaMIME: test.mediaMIME}}}}})
		if (err == nil) != test.valid {
			t.Fatal("snapshot quote validation differs")
		}
	}
}

// Read logical terminal graphemes, skipping continuation cells of wide glyphs.
func replyScreenText(screen tcell.Screen) string {
	var text strings.Builder
	width, height := screen.Size()
	for y := 0; y < height; y++ {
		for x := 0; x < width; {
			r, combining, _, cells := screen.GetContent(x, y)
			text.WriteRune(r)
			for _, c := range combining {
				text.WriteRune(c)
			}
			if cells < 1 {
				cells = 1
			}
			x += cells
		}
		text.WriteByte('\n')
	}
	return text.String()
}
