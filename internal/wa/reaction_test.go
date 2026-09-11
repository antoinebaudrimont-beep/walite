package wa

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"go.mau.fi/whatsmeow"
	waCommon "go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

type fakeReactionClient struct {
	*fakeTextClient
	builtChat, builtSender types.JID
	builtTarget            types.MessageID
	builtEmoji             string
}

func (client *fakeReactionClient) BuildReaction(chat, sender types.JID, target types.MessageID, emoji string) *waE2E.Message {
	client.builtChat, client.builtSender, client.builtTarget, client.builtEmoji = chat, sender, target, emoji
	return &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{
		Key: &waCommon.MessageKey{RemoteJID: proto.String(chat.String()), ID: proto.String(string(target))}, Text: proto.String(emoji),
	}}
}

func TestAdaptIncomingReactionIdentityChangeRemoval(t *testing.T) {
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	for _, emoji := range []string{"👍🏽", ""} {
		key := &waCommon.MessageKey{ID: proto.String("target-id")}
		value := &waE2E.ReactionMessage{Key: key, Text: proto.String(emoji), SenderTimestampMS: proto.Int64(base.UnixMilli())}
		incoming := upstreamTextMessage("12345", "reaction-event", base, false, &waE2E.Message{ReactionMessage: value})
		reaction, ok := adaptReaction(incoming, base.Add(time.Second))
		if !ok || reaction.ChatID().String() != "12345@s.whatsapp.net" || reaction.TargetMessageID().String() != "target-id" ||
			reaction.ReactorID().String() != "12345@s.whatsapp.net" || reaction.Emoji() != emoji || reaction.Removed() != (emoji == "") || !reaction.UpdatedAt().Equal(base) {
			t.Fatalf("reaction=%+v accepted=%t", reaction, ok)
		}
	}
}

func TestWhatsmeowHandleEventRoutesReactionToLiveMutationStream(t *testing.T) {
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	text := "👍🏽"
	timestamp := base.UnixMilli()
	target := "target-id"
	incoming := upstreamTextMessage("12345", "reaction-event", base, false, &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{
		Key: &waCommon.MessageKey{ID: &target}, Text: &text, SenderTimestampMS: &timestamp,
	}})
	source := newRealtimeSource()
	client := &whatsmeowConnectionClient{realtime: source, now: func() time.Time { return base }}
	client.handleEvent(incoming)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- source.Run(ctx) }()
	got := <-source.ReactionEvents()
	if got.ChatID().String() != "12345@s.whatsapp.net" || got.TargetMessageID().String() != target || got.Emoji() != text {
		t.Fatalf("reaction=%+v", got)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run=%v", err)
	}
}

func TestRealReactionSenderUsesPinnedDirectAndGroupTargetIdentityOnce(t *testing.T) {
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name, chat, sender string
		group              bool
	}{
		{name: "direct", chat: "12345@s.whatsapp.net"},
		{name: "group", chat: "12345-67890@g.us", sender: "77777@lid", group: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := &fakeReactionClient{fakeTextClient: &fakeTextClient{connected: true, loggedIn: true}}
			transport.send = func(_ context.Context, jid types.JID, payload *waE2E.Message) (whatsmeow.SendResponse, error) {
				if jid.String() != test.chat || payload.GetReactionMessage() == nil {
					t.Fatal("wrong reaction payload")
				}
				return whatsmeow.SendResponse{ID: "reaction-send", Timestamp: base}, nil
			}
			request, err := model.NewSendReactionRequest(test.chat, "target", test.sender, false, test.group, "❤️")
			if err != nil {
				t.Fatal(err)
			}
			sender := &realTextSender{client: transport, aliases: &chatAliases{}}
			reaction, err := sender.SendReaction(context.Background(), request)
			if err != nil || transport.calls.Load() != 1 || transport.builtChat.String() != test.chat || transport.builtTarget != "target" || transport.builtEmoji != "❤️" ||
				reaction.ReactorID().String() != model.SelfReactorID || reaction.TargetMessageID().String() != "target" {
				t.Fatalf("reaction=%+v err=%v calls=%d sender=%s", reaction, err, transport.calls.Load(), transport.builtSender)
			}
			wantSender := test.chat
			if test.group {
				wantSender = test.sender
			}
			if transport.builtSender.String() != wantSender {
				t.Fatalf("target sender=%q want=%q", transport.builtSender.String(), wantSender)
			}
		})
	}
}

func TestRealReactionSenderFailureDoesNotInventMutationOrRetry(t *testing.T) {
	transport := &fakeReactionClient{fakeTextClient: &fakeTextClient{connected: true, loggedIn: true}}
	transport.send = func(context.Context, types.JID, *waE2E.Message) (whatsmeow.SendResponse, error) {
		return whatsmeow.SendResponse{}, errors.New("private transport failure")
	}
	request, _ := model.NewSendReactionRequest("12345@lid", "target", "", false, false, "")
	reaction, err := (&realTextSender{client: transport, aliases: &chatAliases{}}).SendReaction(context.Background(), request)
	if !errors.Is(err, ErrReactionUncertain) || reaction.ChatID().String() != "" || transport.calls.Load() != 1 {
		t.Fatalf("reaction=%+v err=%v calls=%d", reaction, err, transport.calls.Load())
	}
}
