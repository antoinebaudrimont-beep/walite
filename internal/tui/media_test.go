package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

func TestMediaPlaceholderLabelsAndCaption(t *testing.T) {
	tests := []struct {
		kind, name, caption, want string
	}{
		{kind: mediaImage, caption: "Holiday photo 👋", want: "[Image] Holiday photo 👋"},
		{kind: mediaVideo, want: "[Video]"},
		{kind: mediaDocument, name: "report 日本語.pdf", want: "[Document: report 日本語.pdf]"},
		{kind: mediaDocument, want: "[Document]"},
		{kind: mediaAudio, want: "[Audio]"},
		{kind: mediaSticker, want: "[Sticker]"},
	}
	for _, test := range tests {
		message := messageView{mediaKind: test.kind, mediaName: test.name, text: test.caption}
		if got := messageDisplayText(message); got != test.want {
			t.Fatalf("kind=%q display=%q want=%q", test.kind, got, test.want)
		}
		if rows := wrappedMessageLines(message, 32, true); rows < 1 {
			t.Fatalf("kind=%q rows=%d", test.kind, rows)
		}
	}
	if got := mediaPlaceholder("future-unknown", "ignored"); got != "[Media]" {
		t.Fatalf("unknown placeholder=%q", got)
	}
}

func TestMediaRenderingPreservesDirectionAndGroupSender(t *testing.T) {
	view := defaultDemoView()
	const left, right = 3, 73
	screen := directionalScreen(t, left, right, 8)
	incoming := messageView{
		time: "18:42", mediaKind: mediaImage, text: "Holiday Café 👋",
		isGroup: true, senderID: "person", senderName: "Elena 日本",
	}
	outgoing := messageView{time: "18:43", mediaKind: mediaDocument, mediaName: "report.pdf", fromMe: true}
	if rows := drawMessage(screen, &view, 0, incoming, left, 0, right, 8, false); rows != 2 {
		t.Fatalf("incoming rows=%d", rows)
	}
	if rows := drawMessage(screen, &view, 0, outgoing, left, 2, right, 8, false); rows != 1 {
		t.Fatalf("outgoing rows=%d", rows)
	}
	screen.Show()
	assertDirectionalTextAt(t, screen, left, 0, "18:42  Elena 日本")
	assertDirectionalTextAt(t, screen, left+prefixWidth, 1, "[Image] Holiday Café 👋")
	outgoingText := "18:43  [Document: report.pdf]"
	assertDirectionalTextAt(t, screen, right-uniseg.StringWidth(outgoingText), 2, outgoingText)
	if attributesAt(screen, left+prefixWidth, 0)&tcell.AttrBold == 0 {
		t.Fatal("incoming group sender styling was lost")
	}
	assertDirectionalBorders(t, screen, left, right, 8)
}

func TestInitialAndLiveMediaRenderWithDateSeparators(t *testing.T) {
	firstDay := time.Date(2026, 9, 7, 18, 42, 0, 0, time.Local)
	secondDay := firstDay.Add(24 * time.Hour)
	state, err := chatStateFromInitial(InitialState{Chats: []InitialChat{{
		ID: "media-chat", Title: "Media", ActivityTime: firstDay,
		Messages: []InitialMessage{{ID: "history-image", SentAt: firstDay, BodyRetained: true, MediaKind: mediaImage, Text: "from history 👋"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	view := viewModel{chats: state, options: DefaultOptions()}
	if !applyLiveMessage(&view, LiveMessage{
		ChatID: "media-chat", MessageID: "live-audio", SentAt: secondDay, BodyRetained: true,
		MediaKind: mediaAudio, UnreadCount: 1, ActivityTime: secondDay,
	}) {
		t.Fatal("live media message rejected")
	}
	screen := initializedSimulationScreen(t, 90, 24)
	draw(screen, &view)
	screen.Show()
	got := screenText(screen)
	for _, want := range []string{"[Image] from history 👋", "[Audio]", messageDateLabel(firstDay), messageDateLabel(secondDay)} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, " 2026 ") != 2 {
		t.Fatalf("date separators changed:\n%s", got)
	}
}

func TestMediaPresentationValidationRemainsBounded(t *testing.T) {
	now := time.Unix(1, 0).UTC()
	if _, err := chatStateFromInitial(InitialState{Chats: []InitialChat{{
		ID: "chat", Messages: []InitialMessage{{ID: "bad", SentAt: now, BodyRetained: true, MediaKind: "unknown"}},
	}}}); err == nil {
		t.Fatal("unknown media kind accepted into initial state")
	}
	state, _ := chatStateFromInitial(InitialState{})
	view := viewModel{chats: state}
	if applyLiveMessage(&view, LiveMessage{
		ChatID: "chat", MessageID: "bad", SentAt: now, BodyRetained: true,
		MediaKind: mediaDocument, MediaName: strings.Repeat("x", maxMediaNameBytes+1), ActivityTime: now,
	}) {
		t.Fatal("oversized media presentation entered live working set")
	}
	if state.count() != 0 {
		t.Fatal("rejected event allocated presentation storage")
	}
}
