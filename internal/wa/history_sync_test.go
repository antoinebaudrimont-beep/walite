package wa

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"go.mau.fi/whatsmeow"
	waCommon "go.mau.fi/whatsmeow/proto/waCommon"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	waWeb "go.mau.fi/whatsmeow/proto/waWeb"
	waStore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

func TestHistorySyncCategoriesMapToTransportNeutralBootstrap(t *testing.T) {
	for upstream, want := range map[waHistorySync.HistorySync_HistorySyncType]model.BootstrapCategory{
		waHistorySync.HistorySync_INITIAL_BOOTSTRAP: model.BootstrapInitial,
		waHistorySync.HistorySync_RECENT:            model.BootstrapRecent,
		waHistorySync.HistorySync_FULL:              model.BootstrapFull,
		waHistorySync.HistorySync_ON_DEMAND:         model.BootstrapOnDemand,
	} {
		if got, ok := bootstrapCategory(upstream); !ok || got != want {
			t.Fatalf("category %v mapped to %v,%t", upstream, got, ok)
		}
	}
}

func TestHistorySyncConversationMetadataAndNewestFiftyTextMessages(t *testing.T) {
	now := time.Date(2100, 9, 1, 12, 0, 0, 0, time.UTC)
	client := bootstrapTestClient(now)
	conversation := &waHistorySync.Conversation{
		ID: stringPointer("family@g.us"), DisplayName: stringPointer("Family 家族"),
		LastMsgTimestamp: uint64Pointer(uint64(now.Unix())), UnreadCount: uint32Pointer(7),
		Archived: boolPointer(true), MuteEndTime: uint64Pointer(1),
	}
	for index := 0; index < 60; index++ {
		body := fmt.Sprintf("history-%02d Café 👋", index)
		participant := "22222@s.whatsapp.net"
		conversation.Messages = append(conversation.Messages, &waHistorySync.HistorySyncMsg{Message: bootstrapWebMessage(
			"family@g.us", fmt.Sprintf("m-%02d", index), now.Add(time.Duration(index-60)*time.Minute), index%2 == 0,
			participant, &waE2E.Message{Conversation: &body},
		)})
	}
	conversation.Messages = append(conversation.Messages, &waHistorySync.HistorySyncMsg{Message: bootstrapWebMessage(
		"family@g.us", "unsupported", now, false, "22222@s.whatsapp.net", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{}},
	)})
	record, ok := client.adaptBootstrapConversation(model.BootstrapInitial, conversation, now)
	if !ok || record.Len() != model.MaxBootstrapMessagesPerChat {
		t.Fatalf("record accepted=%t messages=%d", ok, record.Len())
	}
	chat := record.Chat()
	if chat.ID().String() != "family@g.us" || chat.DisplayName() != "Family 家族" || !chat.IsGroup() ||
		chat.UnreadCount() != 7 || !chat.Archived() || !chat.Muted() || !chat.LastMessageAt().Equal(now) {
		t.Fatalf("chat metadata=%+v", chat)
	}
	first, _ := record.At(0)
	second, _ := record.At(1)
	last, _ := record.At(record.Len() - 1)
	if first.MessageID().String() != "m-10" || last.MessageID().String() != "m-59" ||
		first.Text() != "history-10 Café 👋" || last.Text() != "history-59 Café 👋" ||
		!first.FromMe() || second.FromMe() || second.SenderID().String() != "22222@s.whatsapp.net" || !first.IsGroup() {
		t.Fatalf("bounded history first=%+v last=%+v", first, last)
	}
}

func TestHistorySyncGroupSenderRequestsLocalContactName(t *testing.T) {
	now := time.Date(2100, 9, 1, 12, 0, 0, 0, time.UTC)
	client := bootstrapTestClient(now)
	pn := types.NewJID("22222", types.DefaultUserServer)
	lid := types.NewJID("98765", types.HiddenUserServer)
	client.realtime.display = newDisplayResolver(client.realtime.aliases, func(_ context.Context, jid types.JID) (types.JID, error) {
		if jid != lid {
			t.Fatalf("alternate lookup=%s", jid)
		}
		return pn, nil
	}, func(_ context.Context, jid types.JID) (types.ContactInfo, error) {
		if jid == pn {
			return types.ContactInfo{Found: true, FirstName: "Elena 日本 👋", BusinessName: "lower business", PushName: "lower push"}, nil
		}
		return types.ContactInfo{}, nil
	}, nil)
	body := "cached group history"
	record, ok := client.adaptBootstrapConversation(model.BootstrapFull, &waHistorySync.Conversation{
		ID: stringPointer("family@g.us"), Messages: []*waHistorySync.HistorySyncMsg{{Message: bootstrapWebMessage(
			"family@g.us", "group-history", now, false, lid.String(), &waE2E.Message{Conversation: &body},
		)}},
	}, now)
	if !ok || record.Len() != 1 || client.realtime.display.count != 1 {
		t.Fatalf("record=%t messages=%d lookups=%d", ok, record.Len(), client.realtime.display.count)
	}
	message, _ := record.At(0)
	if message.SenderID().String() != lid.String() {
		t.Fatalf("sender=%q", message.SenderID().String())
	}
	request := client.realtime.display.requests[client.realtime.display.head]
	client.realtime.display.lookup(context.Background(), request)
	for _, id := range []model.ChatID{aliasID(t, lid.String()), aliasID(t, pn.String())} {
		if got := client.realtime.display.cached(id); got.Name() != "Elena 日本 👋" || got.Quality() != model.DisplaySaved {
			t.Fatalf("group sender metadata=%+v", got)
		}
	}
}

func TestHistorySyncMetadataOnlyAliasDirectionAndQuote(t *testing.T) {
	now := time.Date(2100, 9, 1, 12, 0, 0, 0, time.UTC)
	client := bootstrapTestClient(now)
	metadataOnly := &waHistorySync.Conversation{ID: stringPointer("12345@s.whatsapp.net"), LidJID: stringPointer("98765@lid"), Name: stringPointer("Café Contact")}
	first, ok := client.adaptBootstrapConversation(model.BootstrapRecent, metadataOnly, now)
	if !ok || first.Len() != 0 || first.Chat().DisplayName() != "Café Contact" {
		t.Fatalf("metadata-only record=%+v accepted=%t", first, ok)
	}

	quotedID, quotedText, body, participant := "quoted", "original 日本語 👋", "reply ❤️", "10000@s.whatsapp.net"
	message := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: &body, ContextInfo: &waE2E.ContextInfo{
		StanzaID: &quotedID, Participant: &participant, QuotedMessage: &waE2E.Message{Conversation: &quotedText},
	}}}
	lidConversation := &waHistorySync.Conversation{
		ID: stringPointer("98765@lid"), PnJID: stringPointer("12345@s.whatsapp.net"),
		Messages: []*waHistorySync.HistorySyncMsg{{Message: bootstrapWebMessage("98765@lid", "reply", now, true, "", message)}},
	}
	second, ok := client.adaptBootstrapConversation(model.BootstrapRecent, lidConversation, now)
	if !ok || second.Chat().ID() != first.Chat().ID() || second.Len() != 1 {
		t.Fatalf("alias records first=%q second=%q messages=%d", first.Chat().ID().String(), second.Chat().ID().String(), second.Len())
	}
	got, _ := second.At(0)
	quote, _ := model.NewTextQuote(quotedID, quotedText, true)
	if !got.FromMe() || got.Text() != body || got.Quote() != quote {
		t.Fatalf("historical reply=%+v", got)
	}
}

func TestHistorySyncUsesNewestCompleteActivityTimestamp(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	client := bootstrapTestClient(now)
	lastMessage := now.Add(-48 * time.Hour)
	conversationActivity := now.Add(-2 * time.Hour)
	record, ok := client.adaptBootstrapConversation(model.BootstrapFull, &waHistorySync.Conversation{
		ID:                    stringPointer("12345@s.whatsapp.net"),
		LastMsgTimestamp:      uint64Pointer(uint64(lastMessage.Unix())),
		ConversationTimestamp: uint64Pointer(uint64(conversationActivity.Unix())),
	}, now)
	if !ok || !record.Chat().LastMessageAt().Equal(conversationActivity) {
		t.Fatalf("activity=%v accepted=%t", record.Chat().LastMessageAt(), ok)
	}
}

func TestHistorySyncUsesReadablePhoneFallbackWithoutInventingLIDPhone(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	client := bootstrapTestClient(now)
	for _, test := range []struct {
		conversation *waHistorySync.Conversation
		want         string
	}{
		{conversation: &waHistorySync.Conversation{ID: stringPointer("12086708856@s.whatsapp.net")}, want: "+12086708856"},
		{conversation: &waHistorySync.Conversation{ID: stringPointer("218699835404531@lid")}, want: ""},
		{conversation: &waHistorySync.Conversation{ID: stringPointer("218699835404531@lid"), PnJID: stringPointer("33621344453@s.whatsapp.net")}, want: "+33621344453"},
	} {
		record, ok := client.adaptBootstrapConversation(model.BootstrapFull, test.conversation, now)
		if !ok || record.Chat().DisplayName() != test.want {
			t.Fatalf("id=%q name=%q accepted=%t", test.conversation.GetID(), record.Chat().DisplayName(), ok)
		}
	}
}

func bootstrapTestClient(now time.Time) *whatsmeowConnectionClient {
	own := types.NewJID("10000", types.DefaultUserServer)
	return &whatsmeowConnectionClient{
		client:   &whatsmeow.Client{Store: &waStore.Device{ID: &own, LID: types.NewJID("20000", types.HiddenUserServer)}},
		realtime: newRealtimeSource(), now: func() time.Time { return now },
	}
}

func bootstrapWebMessage(chat, id string, at time.Time, fromMe bool, participant string, message *waE2E.Message) *waWebMessageInfo {
	return newBootstrapWebMessage(chat, id, at, fromMe, participant, message)
}

// Alias keeps the fixture construction readable while using the pinned type.
type waWebMessageInfo = waWeb.WebMessageInfo

func newBootstrapWebMessage(chat, id string, at time.Time, fromMe bool, participant string, message *waE2E.Message) *waWeb.WebMessageInfo {
	return &waWeb.WebMessageInfo{
		Key: &waCommon.MessageKey{RemoteJID: &chat, ID: &id, FromMe: &fromMe}, Message: message,
		MessageTimestamp: uint64Pointer(uint64(at.Unix())), Participant: &participant,
	}
}

func stringPointer(value string) *string { return &value }
func uint64Pointer(value uint64) *uint64 { return &value }
func uint32Pointer(value uint32) *uint32 { return &value }
func boolPointer(value bool) *bool       { return &value }
