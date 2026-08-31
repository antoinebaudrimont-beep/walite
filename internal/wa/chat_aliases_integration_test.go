package wa

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/config"
	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"github.com/antoinebaudrimont-beep/walite/internal/service"
	"github.com/antoinebaudrimont-beep/walite/internal/store"
	"github.com/antoinebaudrimont-beep/walite/internal/syncpolicy"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func TestChatAliasSendEchoCommitOrderingAndUnrelatedIDs(t *testing.T) {
	for _, lidPrimary := range []bool{false, true} {
		for _, echoFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("lid=%t/echoFirst=%t", lidPrimary, echoFirst), func(t *testing.T) {
				primary := types.NewJID("12345", types.DefaultUserServer)
				alternate := types.NewJID("987654", types.HiddenUserServer)
				if lidPrimary {
					primary, alternate = alternate, primary
				}
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
				base := time.Now().UTC().Truncate(time.Second)
				started, release := make(chan struct{}), make(chan struct{})
				text := "exact é 日本語 🐧"
				transport := &fakeTextClient{connected: true, loggedIn: true, send: func(ctx context.Context, jid types.JID, body *waE2E.Message) (whatsmeow.SendResponse, error) {
					if jid != primary || body.GetConversation() != text {
						t.Error("transport request changed")
					}
					close(started)
					if echoFirst {
						select {
						case <-release:
						case <-ctx.Done():
							return whatsmeow.SendResponse{}, ctx.Err()
						}
					}
					return whatsmeow.SendResponse{ID: "X", Timestamp: base}, nil
				}}
				core, err := service.NewWithTextSender(realtimeCoreOptions(values), source, memory, policy, service.NewSystemClock(), &realTextSender{client: transport, aliases: source.aliases})
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan error, 1)
				go func() { done <- core.Run(ctx) }()
				t.Cleanup(func() {
					cancel()
					if err := <-done; !errors.Is(err, context.Canceled) {
						t.Error(err)
					}
				})
				waitForCoreReady(t, core.Updates())
				wrapped := &whatsmeowConnectionClient{realtime: source, now: func() time.Time { return base.Add(time.Minute) }}
				// The selected/presented identity exists before the pair is learned.
				wrapped.handleEvent(aliasUpstreamMessage(primary, types.JID{}, "seed", base.Add(-time.Second), false, "seed"))
				if seed := <-core.LiveEvents(); seed.Message().ChatID().String() != primary.String() {
					t.Fatal("seed identity changed")
				}
				request, _ := service.NewSendTextRequest(primary.String(), text)
				sent := make(chan error, 1)
				go func() { sent <- core.SendText(ctx, request) }()
				<-started
				if !echoFirst {
					if err := <-sent; err != nil {
						t.Fatal(err)
					}
				}
				echo := aliasUpstreamMessage(alternate, primary, "X", base, true, text)
				if echoFirst {
					wrapped.handleEvent(echo)
				}
				first := <-core.LiveEvents()
				if first.Message().ChatID().String() != primary.String() || first.Message().MessageID().String() != "X" || first.Message().Text() != text || !first.Message().FromMe() {
					t.Fatalf("first=%+v", first)
				}
				if echoFirst {
					close(release)
					if err := <-sent; err != nil {
						t.Fatal(err)
					}
				} else {
					wrapped.handleEvent(echo)
				}
				// Different alias message must survive; unrelated same-ID message too.
				wrapped.handleEvent(aliasUpstreamMessage(alternate, primary, "Y", base.Add(time.Second), false, "different"))
				unrelated := types.NewJID("55555", types.DefaultUserServer)
				wrapped.handleEvent(aliasUpstreamMessage(unrelated, types.JID{}, "X", base.Add(2*time.Second), true, "unrelated"))
				second, third := <-core.LiveEvents(), <-core.LiveEvents()
				if second.Message().MessageID().String() != "Y" || second.Message().ChatID().String() != primary.String() || second.UnreadCount() != 2 || !second.ActivityTime().Equal(base.Add(time.Second)) {
					t.Fatalf("second/duplicate=%+v", second)
				}
				if third.Message().MessageID().String() != "X" || third.Message().ChatID().String() != unrelated.String() {
					t.Fatalf("unrelated collapsed=%+v", third)
				}
				primaryID := aliasID(t, primary.String())
				messages, _, err := memory.Page(ctx, primaryID, model.NoCursor(), 10)
				if err != nil || len(messages) != 3 {
					t.Fatalf("messages=%d err=%v", len(messages), err)
				}
				usage, err := memory.Usage(ctx)
				if err != nil || usage.Chats() != 2 || usage.Messages() != 4 || transport.calls.Load() != 1 {
					t.Fatalf("usage=%+v err=%v calls=%d", usage, err, transport.calls.Load())
				}
			})
		}
	}
}

func aliasUpstreamMessage(chat, alternate types.JID, id string, at time.Time, fromMe bool, text string) *events.Message {
	info := types.MessageInfo{MessageSource: types.MessageSource{Chat: chat, Sender: chat, IsFromMe: fromMe}, ID: id, Timestamp: at}
	if fromMe {
		info.RecipientAlt = alternate
	} else {
		info.SenderAlt = alternate
	}
	return &events.Message{Info: info, Message: &waE2E.Message{Conversation: &text}}
}

func TestChatAliasEchoWithoutMetadataUsesSharedSendTimeLookup(t *testing.T) {
	source := newRealtimeSource()
	pn := types.NewJID("12345", types.DefaultUserServer)
	lid := types.NewJID("987654", types.HiddenUserServer)
	source.lookup = func(_ context.Context, jid types.JID) (types.JID, error) {
		if jid == pn {
			return lid, nil
		}
		if jid == lid {
			return pn, nil
		}
		return types.JID{}, nil
	}
	text := "unchanged 日本語 🐧"
	client := &fakeTextClient{connected: true, loggedIn: true, send: func(context.Context, types.JID, *waE2E.Message) (whatsmeow.SendResponse, error) {
		return whatsmeow.SendResponse{ID: "X", Timestamp: time.Now().UTC()}, nil
	}}
	sender := &realTextSender{client: client, aliases: source.aliases, lookup: source.lookup}
	first, err := sender.SendText(context.Background(), aliasID(t, pn.String()), text)
	if err != nil {
		t.Fatal(err)
	}
	echo, err := first.Message().WithChatID(aliasID(t, lid.String()))
	if err != nil {
		t.Fatal(err)
	}
	event, _ := model.NewEvent(echo, first.ReceivedAt())
	resolved, err := source.resolveEntry(context.Background(), realtimeEntry{event: event})
	if err != nil || resolved != first {
		t.Fatalf("resolved=%+v first=%+v err=%v", resolved, first, err)
	}
}
