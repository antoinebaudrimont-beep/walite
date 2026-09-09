package wa

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

func TestRealQuotedTextBuildsExactContextAndParticipant(t *testing.T) {
	pn, lid := "12345@s.whatsapp.net", "987654@lid"
	for _, tc := range []struct {
		name, chat, alternate, participant string
		fromMe                             bool
	}{
		{"peer PN", pn, "", pn, false},
		{"peer mapped LID", pn, lid, lid, false},
		{"peer LID", lid, pn, lid, false},
		{"own message", pn, lid, "55555@lid", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, quoted, stanza := "walite reply é 日本語 🐧", "original ❤️ café", "exact-quoted-ID"
			quote, err := model.NewTextQuote(stanza, quoted, tc.fromMe)
			if err != nil {
				t.Fatal(err)
			}
			at := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
			client := &fakeTextClient{connected: true, loggedIn: true, send: func(_ context.Context, jid types.JID, payload *waE2E.Message) (whatsmeow.SendResponse, error) {
				want := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: &body, ContextInfo: &waE2E.ContextInfo{StanzaID: &stanza, Participant: &tc.participant, QuotedMessage: &waE2E.Message{Conversation: &quoted}}}}
				if jid.String() != tc.chat || !reflect.DeepEqual(payload, want) {
					t.Errorf("destination=%s payload=%v want=%v", jid, payload, want)
				}
				return whatsmeow.SendResponse{ID: "reply-result", Timestamp: at}, nil
			}}
			sender := &realTextSender{client: client, aliases: &chatAliases{}, lookup: func(context.Context, types.JID) (types.JID, error) {
				if tc.alternate == "" {
					return types.JID{}, nil
				}
				return types.ParseJID(tc.alternate)
			}, ownJID: func() types.JID { id := types.NewJID("55555", types.HiddenUserServer); id.Device = 2; return id }}
			event, err := sender.SendText(context.Background(), aliasID(t, tc.chat), body, quote)
			if err != nil || client.calls.Load() != 1 || event.Message().ChatID().String() != tc.chat || event.Message().Text() != body || event.Message().MessageID().String() != "reply-result" || !event.Message().FromMe() || event.Message().SentAt() != at {
				t.Fatalf("event=%+v err=%v calls=%d", event, err, client.calls.Load())
			}
		})
	}
}

func TestRealMediaRepliesBuildMatchingQuotedMessageShape(t *testing.T) {
	pn, lid := "12345@s.whatsapp.net", "987654@lid"
	tests := []struct {
		kind                model.MediaKind
		name, mime, caption string
		assert              func(*testing.T, *waE2E.Message)
	}{
		{model.MediaImage, "", "image/jpeg", "", func(t *testing.T, message *waE2E.Message) {
			if value := message.GetImageMessage(); value == nil || value.GetMimetype() != "image/jpeg" || value.GetCaption() != "" {
				t.Fatal("image quote shape lost")
			}
		}},
		{model.MediaImage, "", "image/jpeg", "Holiday Café 👋", func(t *testing.T, message *waE2E.Message) {
			if value := message.GetImageMessage(); value == nil || value.GetCaption() != "Holiday Café 👋" {
				t.Fatal("image caption lost")
			}
		}},
		{model.MediaVideo, "", "video/mp4", "clip", func(t *testing.T, message *waE2E.Message) {
			if value := message.GetVideoMessage(); value == nil || value.GetMimetype() != "video/mp4" || value.GetCaption() != "clip" || value.GetGifPlayback() {
				t.Fatal("video quote shape lost")
			}
		}},
		{model.MediaDocument, "report 日本語.pdf", "application/pdf", "", func(t *testing.T, message *waE2E.Message) {
			if value := message.GetDocumentMessage(); value == nil || value.GetFileName() != "report 日本語.pdf" || value.GetMimetype() != "application/pdf" {
				t.Fatal("document quote shape lost")
			}
		}},
		{model.MediaAudio, "", "audio/ogg", "", func(t *testing.T, message *waE2E.Message) {
			if value := message.GetAudioMessage(); value == nil || value.GetMimetype() != "audio/ogg" {
				t.Fatal("audio quote shape lost")
			}
		}},
		{model.MediaSticker, "", "image/webp", "", func(t *testing.T, message *waE2E.Message) {
			if value := message.GetStickerMessage(); value == nil || value.GetMimetype() != "image/webp" {
				t.Fatal("sticker quote shape lost")
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.kind.String()+test.caption, func(t *testing.T) {
			media, _ := model.NewMedia(test.kind, test.name, test.mime)
			quote, err := model.NewMediaQuote("media-original", test.caption, false, media)
			if err != nil {
				t.Fatal(err)
			}
			client := &fakeTextClient{connected: true, loggedIn: true, send: func(_ context.Context, jid types.JID, payload *waE2E.Message) (whatsmeow.SendResponse, error) {
				if jid.String() != pn {
					t.Fatalf("destination=%s", jid)
				}
				info := payload.GetExtendedTextMessage().GetContextInfo()
				if info.GetStanzaID() != "media-original" || info.GetParticipant() != lid {
					t.Fatalf("context=%+v", info)
				}
				test.assert(t, info.GetQuotedMessage())
				return whatsmeow.SendResponse{ID: "outgoing", Timestamp: time.Unix(2, 0).UTC()}, nil
			}}
			sender := &realTextSender{client: client, aliases: &chatAliases{}, lookup: func(context.Context, types.JID) (types.JID, error) { return types.ParseJID(lid) }}
			event, err := sender.SendText(context.Background(), aliasID(t, pn), "reply", quote)
			if err != nil || client.calls.Load() != 1 || event.Message().MessageID().String() != "outgoing" {
				t.Fatalf("event=%+v err=%v calls=%d", event, err, client.calls.Load())
			}
		})
	}
}

func TestRealQuotedTextRejectsGroupsAndMissingParticipantBeforeTransport(t *testing.T) {
	quote, _ := model.NewTextQuote("id", "quoted", false)
	media, _ := model.NewMedia(model.MediaImage, "", "image/jpeg")
	mediaQuote, _ := model.NewMediaQuote("media-id", "", false, media)
	own, _ := model.NewTextQuote("id", "quoted", true)
	client := &fakeTextClient{connected: true, loggedIn: true}
	sender := &realTextSender{client: client}
	group := aliasID(t, "12345-67890@g.us")
	if err := sender.ValidateText(group, "reply", quote); !errors.Is(err, ErrGroupReplyUnavailable) {
		t.Fatal(err)
	}
	if _, err := sender.SendText(context.Background(), group, "reply", quote); !errors.Is(err, ErrGroupReplyUnavailable) {
		t.Fatal(err)
	}
	if err := sender.ValidateText(group, "reply", mediaQuote); !errors.Is(err, ErrGroupReplyUnavailable) {
		t.Fatal(err)
	}
	if _, err := sender.SendText(context.Background(), aliasID(t, "12345@lid"), "reply", own); !errors.Is(err, ErrTextUnavailable) {
		t.Fatal(err)
	}
	for _, quotes := range [][]model.TextQuote{{{}}, {quote, quote}} {
		if _, err := sender.SendText(context.Background(), aliasID(t, "12345@lid"), "reply", quotes...); err == nil {
			t.Fatal("invalid quote accepted")
		}
	}
	if client.calls.Load() != 0 {
		t.Fatal("rejected reply called transport")
	}
}

func TestRealQuotedTextFailureOrCancellationDoesNotRetry(t *testing.T) {
	quote, _ := model.NewTextQuote("id", "quoted", false)
	client := &fakeTextClient{connected: true, loggedIn: true, send: func(context.Context, types.JID, *waE2E.Message) (whatsmeow.SendResponse, error) {
		return whatsmeow.SendResponse{}, errors.New("transport failed")
	}}
	sender := &realTextSender{client: client}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sender.SendText(ctx, aliasID(t, "12345@lid"), "reply", quote); !errors.Is(err, context.Canceled) || client.calls.Load() != 0 {
		t.Fatal("cancelled reply reached transport")
	}
	event, err := sender.SendText(context.Background(), aliasID(t, "12345@lid"), "reply", quote)
	if !errors.Is(err, ErrTextUncertain) || client.calls.Load() != 1 || event.Message().MessageID().String() != "" {
		t.Fatalf("event=%+v err=%v calls=%d", event, err, client.calls.Load())
	}
}

func TestRealQuotedTextPNAndLIDEchoCommitOnce(t *testing.T) {
	quote, _ := model.NewTextQuote("quoted-original", "synthetic original é 日本語 👋", false)
	testRealTextSendEchoCommitOnce(t, true, quote)
}

func TestRealMediaQuotePNAndLIDEchoCommitOnceWithoutDowngrade(t *testing.T) {
	media, _ := model.NewMedia(model.MediaImage, "", "image/jpeg")
	quote, _ := model.NewMediaQuote("quoted-original", "Holiday Café 👋", false, media)
	testRealTextSendEchoCommitOnce(t, true, quote)
}

func TestReplyParticipantUsesExistingDeviceIdentity(t *testing.T) {
	connection, err := NewConnection(context.Background(), filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close() // No pairing, Connect, or SendMessage.
	sender := connection.TextSender().(*realTextSender)
	device := connection.client.(*whatsmeowConnectionClient).client.Store
	if !sender.ownJID().IsEmpty() {
		t.Fatal("unlinked device fabricated identity")
	}
	pn := types.NewJID("11111", types.DefaultUserServer)
	pn.Device = 2
	device.ID = &pn
	if sender.ownJID() != pn.ToNonAD() {
		t.Fatal("own PN fallback changed")
	}
	lid := types.NewJID("22222", types.HiddenUserServer)
	device.LID = lid
	if sender.ownJID() != lid {
		t.Fatal("existing own LID not used")
	}
}
