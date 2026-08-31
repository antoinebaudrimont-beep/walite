package wa

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/antoinebaudrimont-beep/walite/internal/config"
	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/service"
	"github.com/antoinebaudrimont-beep/walite/internal/store"
	"github.com/antoinebaudrimont-beep/walite/internal/syncpolicy"
	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waStore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func incomingReplyFixture(participant, text string, extended bool) *events.Message {
	body, stanza := "synthetic incoming reply 👋", "quoted-original"
	quoted := &waE2E.Message{Conversation: &text}
	if extended {
		quoted = &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: &text}}
	}
	return upstreamTextMessage("12345", "incoming-reply", time.Date(2100, 1, 1, 15, 57, 0, 0, time.UTC), false,
		&waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: &body, ContextInfo: &waE2E.ContextInfo{
			StanzaID: &stanza, Participant: &participant, QuotedMessage: quoted,
		}}})
}

func TestIncomingTextQuoteIdentityTextAndBounds(t *testing.T) {
	ownPN := types.NewJID("10000", types.DefaultUserServer)
	ownPN.Device = 7 // linked devices identify the same author
	ownLID := types.NewJID("20000", types.HiddenUserServer)
	for _, identity := range []struct {
		name, participant string
		fromMe, group     bool
	}{
		{"own PN", "10000@s.whatsapp.net", true, false},
		{"own LID", "20000@lid", true, false},
		{"peer PN", "12345@s.whatsapp.net", false, false},
		{"peer LID", "54321@lid", false, false},
		{"missing participant", "", false, false},
		{"unknown participant", "30000@s.whatsapp.net", false, false},
		{"not a JID", "Synthetic Name", false, false},
		{"own group quote", "20000@lid", true, true},
		{"peer group quote", "54321@lid", false, true},
	} {
		for _, extended := range []bool{false, true} {
			t.Run(identity.name+map[bool]string{false: "/conversation", true: "/extended"}[extended], func(t *testing.T) {
				text := "Café é 日本語 ❤️ 👍🏽 👨‍👩‍👧‍👦"
				incoming := incomingReplyFixture(identity.participant, text, extended)
				incoming.Info.Sender = types.NewJID("12345", types.DefaultUserServer)
				incoming.Info.SenderAlt = types.NewJID("54321", types.HiddenUserServer)
				if identity.group {
					incoming.Info.Chat = types.NewJID("test-group", types.GroupServer)
					incoming.Info.IsGroup = true
				}
				event, ok := adaptTextMessage(incoming, incoming.Info.Timestamp, ownPN, ownLID)
				quote := event.Message().Quote()
				if !ok || quote.MessageID().String() != "quoted-original" || quote.Text() != text || quote.FromMe() != identity.fromMe {
					t.Fatalf("quote=%+v recognized=%t", quote, ok)
				}
				if event.Message().Text() != incoming.Message.GetExtendedTextMessage().GetText() || event.Message().FromMe() || event.Message().IsGroup() != identity.group {
					t.Fatal("quote extraction changed parent message")
				}
			})
		}
	}
	for _, text := range []string{strings.Repeat("a", 1023) + "👋 tail", strings.Repeat("é", 600)} {
		incoming := incomingReplyFixture(ownPN.ToNonAD().String(), text, false)
		event, ok := adaptTextMessage(incoming, incoming.Info.Timestamp, ownPN, ownLID)
		want, err := model.NewTextQuote("quoted-original", text, true)
		if err != nil || !ok || event.Message().Quote() != want || !utf8.ValidString(event.Message().Quote().Text()) || len(want.Text()) > model.MaxQuoteTextBytes {
			t.Fatal("quote did not retain model's exact UTF-8-safe 1 KiB bound")
		}
	}
}

func TestIncomingTextQuoteUnavailableMetadataKeepsParent(t *testing.T) {
	for _, kind := range []string{"ordinary", "missing context", "missing stanza", "missing quoted message", "media", "invalid UTF8", "no account identity"} {
		t.Run(kind, func(t *testing.T) {
			incoming := incomingReplyFixture("10000@s.whatsapp.net", "quoted", false)
			body := incoming.Message.GetExtendedTextMessage().GetText()
			info := incoming.Message.ExtendedTextMessage.ContextInfo
			switch kind {
			case "ordinary":
				incoming.Message = &waE2E.Message{Conversation: &body}
			case "missing context":
				incoming.Message.ExtendedTextMessage.ContextInfo = nil
			case "missing stanza":
				info.StanzaID = nil
			case "missing quoted message":
				info.QuotedMessage = nil
			case "media":
				info.QuotedMessage = &waE2E.Message{ImageMessage: &waE2E.ImageMessage{}}
			case "invalid UTF8":
				invalid := string([]byte{0xff})
				info.QuotedMessage.Conversation = &invalid
			}
			event, ok := adaptTextMessage(incoming, incoming.Info.Timestamp)
			if !ok || event.Message().Text() != body || event.Message().FromMe() {
				t.Fatal("unavailable quote discarded/changed the incoming body")
			}
			quote := event.Message().Quote()
			if kind == "no account identity" {
				if quote.MessageID().String() != "quoted-original" || quote.Text() != "quoted" || quote.FromMe() {
					t.Fatal("missing account identity must retain quote without guessing author")
				}
			} else if quote != (model.TextQuote{}) {
				t.Fatal("unsupported/malformed quote invented presentation metadata")
			}
		})
	}
}

func TestIncomingReplyCallbackThroughCoreAndDuplicateAliases(t *testing.T) {
	for _, lateQuote := range []bool{false, true} {
		t.Run(map[bool]string{false: "retain", true: "enrich"}[lateQuote], func(t *testing.T) {
			source := newRealtimeSource()
			values := config.DefaultValues()
			memory, err := store.NewMemory(values.Retention.MaxChats)
			if err != nil {
				t.Fatal(err)
			}
			policy, err := syncpolicy.New(values.Retention)
			if err != nil {
				t.Fatal(err)
			}
			core, err := service.New(realtimeCoreOptions(values), source, memory, policy, service.NewSystemClock())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			done := make(chan error, 1)
			go func() { done <- core.Run(ctx) }()
			t.Cleanup(func() {
				cancel()
				if err := <-done; !errors.Is(err, context.Canceled) {
					t.Error(err)
				}
			})
			waitForCoreReady(t, core.Updates())
			incoming := incomingReplyFixture("20000@lid", "original Café é 👋", false)
			incoming.Info.Sender = incoming.Info.Chat
			incoming.Info.SenderAlt = types.NewJID("54321", types.HiddenUserServer)
			ownPN := types.NewJID("10000", types.DefaultUserServer)
			client := &whatsmeowConnectionClient{
				client:   &whatsmeow.Client{Store: &waStore.Device{ID: &ownPN, LID: types.NewJID("20000", types.HiddenUserServer)}},
				realtime: source, now: func() time.Time { return incoming.Info.Timestamp },
			}
			quoteContext := incoming.Message.ExtendedTextMessage.ContextInfo
			if lateQuote {
				incoming.Message.ExtendedTextMessage.ContextInfo = nil
			}
			client.handleEvent(incoming)
			first := <-core.LiveEvents()
			want, _ := model.NewTextQuote("quoted-original", "original Café é 👋", true)
			if first.Message().MessageID().String() != "incoming-reply" || first.UnreadCount() != 1 || (!lateQuote && first.Message().Quote() != want) {
				t.Fatal("callback -> Event -> store -> LiveEvent lost incoming quote")
			}
			// The same logical message arrives under the authoritative LID alias.
			incoming.Info.Chat = types.NewJID("54321", types.HiddenUserServer)
			incoming.Info.Sender, incoming.Info.SenderAlt = incoming.Info.Chat, types.NewJID("12345", types.DefaultUserServer)
			incoming.Message.ExtendedTextMessage.ContextInfo = quoteContext
			client.handleEvent(incoming)
			incoming.Message.ExtendedTextMessage.ContextInfo = nil
			client.handleEvent(incoming) // poorer duplicate must not downgrade it
			body := "marker after duplicate"
			marker := upstreamTextMessage("12345", "marker", incoming.Info.Timestamp.Add(time.Second), false, &waE2E.Message{Conversation: &body})
			client.handleEvent(marker)
			if next := <-core.LiveEvents(); next.Message().MessageID().String() != "marker" || next.UnreadCount() != 2 {
				t.Fatal("duplicate published another message or incremented unread")
			}
			id, _ := model.NewChatID("12345@s.whatsapp.net")
			messages, _, err := memory.Page(ctx, id, model.NoCursor(), 10)
			if err != nil || len(messages) != 2 || messages[1].Quote() != want {
				t.Fatal("store lost richer quote or duplicated aliased message")
			}
		})
	}
}
