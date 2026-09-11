package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestExtractMessageLinksIsBoundedAndUsesLogicalText(t *testing.T) {
	text := "wrapped-looking https://example.test/é/日本語?x=1, unsupported ftp://bad.test and javascript:alert(1) "
	text += strings.Repeat("https://bounded.test/x ", MaxMessageLinks+4)
	links := extractMessageLinks(text)
	if len(links) != MaxMessageLinks || links[0] != "https://example.test/é/日本語?x=1" {
		t.Fatalf("links=%q", links)
	}
	for _, invalid := range []string{"not a link", "xhttps://bad.test", "http://", "HTTPS://example.test"} {
		if got := extractMessageLinks(invalid); len(got) != 0 {
			t.Fatalf("%q produced %q", invalid, got)
		}
	}
	exact := "https://x/" + strings.Repeat("a", MaxLinkBytes-len("https://x/"))
	if got := extractMessageLinks(exact); len(got) != 1 || got[0] != exact {
		t.Fatalf("maximum link=%d/%q", len(got), got)
	}
	if got := extractMessageLinks(exact + "a"); len(got) != 0 {
		t.Fatalf("oversized link accepted: %d", len(got[0]))
	}
}

func TestFocusedLinkOneOpensAndMultipleUseChooser(t *testing.T) {
	model := defaultDemoView()
	model.replySelect = replySelectionState{valid: true, index: 0}
	model.chats.chats[0].messages[0].text = "see https://one.test/a"
	var got LinkRequest
	model.links = func(request LinkRequest) bool { got = request; return true }
	if !openFocusedLinks(&model) || got.Action != LinkOpen || got.URL != "https://one.test/a" || model.linkPicker.open {
		t.Fatalf("request=%+v picker=%+v", got, model.linkPicker)
	}

	model.chats.chats[0].messages[0].text = "https://one.test/a and https://two.test/b"
	if !openFocusedLinks(&model) || !model.linkPicker.open || model.linkPicker.count != 2 {
		t.Fatalf("picker=%+v", model.linkPicker)
	}
	if !handleLinkPickerKey(&model, tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)) || !handleLinkPickerKey(&model, tcell.NewEventKey(tcell.KeyRune, 'C', tcell.ModNone)) {
		t.Fatal("chooser input not handled")
	}
	if got.Action != LinkCopy || got.URL != "https://two.test/b" || model.linkPicker.open {
		t.Fatalf("request=%+v picker=%+v", got, model.linkPicker)
	}
}

func TestHostileLinkIsPassedAsOneExactValue(t *testing.T) {
	want := "https://example.test/$(touch-pwned)?x=;&y=日本語"
	links := extractMessageLinks("open " + want)
	if len(links) != 1 || links[0] != want {
		t.Fatalf("links=%q", links)
	}
}

func TestReadOnlyChatRejectsAllSendEntryPoints(t *testing.T) {
	model := defaultDemoView()
	model.chats.chats[0].readOnly = true
	screen := initializedSimulationScreen(t, 100, 24)
	draw(screen, &model)
	screen.Show()
	if !strings.Contains(screenText(screen), "[read-only]") {
		t.Fatal("read-only state not visible")
	}
	for _, event := range []*tcell.EventKey{
		tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone),
		tcell.NewEventKey(tcell.KeyRune, 'F', tcell.ModNone),
	} {
		changed, exit := handleKey(&model, event, 100, 24)
		if !changed || exit || model.mode != modeNavigate || model.sendStatus != "Sending is not supported for this chat type" {
			t.Fatalf("changed=%t exit=%t mode=%v status=%q", changed, exit, model.mode, model.sendStatus)
		}
	}
	model.replySelect = replySelectionState{valid: true, index: 0}
	if !handleReplySelectionKey(&model, tcell.NewEventKey(tcell.KeyRune, 'R', tcell.ModNone), 100, 24) || model.emojiPicker.open {
		t.Fatal("read-only reaction was not rejected")
	}
	if !handleReplySelectionKey(&model, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), 100, 24) || model.mode != modeNavigate {
		t.Fatal("read-only reply was not rejected")
	}
}

func TestGroupReplyRequestCarriesTargetParticipant(t *testing.T) {
	model := defaultDemoView()
	chat := &model.chats.chats[0]
	chat.isGroup = true
	target := &chat.messages[0]
	target.isGroup, target.senderID, target.bodyRetained = true, "55555@lid", true
	model.mode = modeCompose
	model.replyTarget = replyTarget{valid: true, id: target.id}
	model.composer.insertText("reply")
	var got SendRequest
	model.send = func(request SendRequest) error { got = request; return nil }
	if !submitOutgoingMessage(&model) || !got.ReplyIsGroup || got.ReplyTargetSenderID != "55555@lid" || got.ReplyToID == "" {
		t.Fatalf("request=%+v", got)
	}
}

func TestCommittedGroupReplyUsesExistingQuoteRendering(t *testing.T) {
	state, err := chatStateFromInitial(InitialState{Chats: []InitialChat{
		{
			ID: "123-456@g.us", Title: "Group", IsGroup: true,
			Messages: []InitialMessage{
				{ID: "reply", SentAt: liveTestBase, FromMe: true, Text: "response", BodyRetained: true,
					ReplyToID: "original", ReplyToText: "original Café 👋", ReplyToFromMe: false},
			},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	model := viewModel{chats: state, options: DefaultOptions()}
	screen := initializedSimulationScreen(t, 100, 24)
	draw(screen, &model)
	screen.Show()
	if text := screenText(screen); !strings.Contains(text, "original Café 👋") || !strings.Contains(text, "response") {
		t.Fatal(text)
	}
}
