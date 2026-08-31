package wa

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync/atomic"
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

type fakeTextClient struct {
	connected, loggedIn bool
	calls               atomic.Int32
	readinessCalls      atomic.Int32
	send                func(context.Context, types.JID, *waE2E.Message) (whatsmeow.SendResponse, error)
}

func (client *fakeTextClient) IsConnected() bool {
	client.readinessCalls.Add(1)
	return client.connected
}
func (client *fakeTextClient) IsLoggedIn() bool { return client.loggedIn }
func (client *fakeTextClient) SendMessage(ctx context.Context, jid types.JID, message *waE2E.Message, extra ...whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error) {
	client.calls.Add(1)
	if len(extra) != 0 {
		return whatsmeow.SendResponse{}, errors.New("unexpected send extras")
	}
	return client.send(ctx, jid, message)
}

func TestRealTextSenderPreservesPlainTextJIDAndAuthoritativeResult(t *testing.T) {
	for _, id := range []string{"12345@lid", "15551234567@s.whatsapp.net", "12345-67890@g.us"} {
		t.Run(id, func(t *testing.T) {
			chatID, _ := model.NewChatID(id)
			text := "é äöü 日本語 🐧 https://example.invalid/"
			at := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
			client := &fakeTextClient{connected: true, loggedIn: true, send: func(_ context.Context, jid types.JID, message *waE2E.Message) (whatsmeow.SendResponse, error) {
				if jid.String() != id || message.GetConversation() != text {
					t.Error("request changed")
				}
				if !reflect.DeepEqual(message, &waE2E.Message{Conversation: &text}) {
					t.Error("non-plain protobuf fields")
				}
				return whatsmeow.SendResponse{ID: "authoritative-id", Timestamp: at}, nil
			}}
			event, err := (&realTextSender{client: client}).SendText(context.Background(), chatID, text)
			if err != nil || client.calls.Load() != 1 || event.Message().ChatID() != chatID || event.Message().MessageID().String() != "authoritative-id" || event.Message().SentAt() != at || !event.Message().FromMe() || event.Message().Text() != text || !event.Message().BodyRetained() {
				t.Fatalf("result=%+v err=%v calls=%d", event, err, client.calls.Load())
			}
		})
	}
}

func TestRealTextSenderRejectsInvalidDisconnectedAndCancelledBeforeTransport(t *testing.T) {
	client := &fakeTextClient{connected: true, loggedIn: true}
	sender := &realTextSender{client: client}
	for _, id := range []string{"not-a-jid", "@lid", "123@lid@extra", "123:2@s.whatsapp.net", "123.0@s.whatsapp.net", "bad user@lid", "123@newsletter", "status@broadcast"} {
		chatID, _ := model.NewChatID(id)
		if _, err := sender.SendText(context.Background(), chatID, "text"); !errors.Is(err, ErrTextRejected) {
			t.Errorf("id=%q err=%v", id, err)
		}
	}
	chatID, _ := model.NewChatID("123@lid")
	client.connected = false
	if _, err := sender.SendText(context.Background(), chatID, "text"); !errors.Is(err, ErrTextUnavailable) {
		t.Fatal(err)
	}
	client.connected = true
	client.loggedIn = false
	if _, err := sender.SendText(context.Background(), chatID, "text"); !errors.Is(err, ErrTextUnavailable) {
		t.Fatal(err)
	}
	client.loggedIn = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sender.SendText(ctx, chatID, "text"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if client.calls.Load() != 0 {
		t.Fatal("rejected request reached transport")
	}
}

func TestRealTextSenderCancellationAndSuccessRace(t *testing.T) {
	for _, success := range []bool{false, true} {
		t.Run(map[bool]string{false: "blocked cancellation", true: "success wins"}[success], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started := make(chan struct{})
			client := &fakeTextClient{connected: true, loggedIn: true, send: func(ctx context.Context, _ types.JID, _ *waE2E.Message) (whatsmeow.SendResponse, error) {
				close(started)
				<-ctx.Done()
				if success {
					return whatsmeow.SendResponse{ID: "success", Timestamp: time.Now().UTC()}, nil
				}
				return whatsmeow.SendResponse{}, ctx.Err()
			}}
			chatID, _ := model.NewChatID("123@lid")
			done := make(chan error, 1)
			go func() { _, err := (&realTextSender{client: client}).SendText(ctx, chatID, "text"); done <- err }()
			<-started
			cancel()
			err := <-done
			if success && err != nil || !success && !errors.Is(err, ErrTextUncertain) || client.calls.Load() != 1 {
				t.Fatalf("err=%v calls=%d", err, client.calls.Load())
			}
		})
	}
}

func TestConnectionTextSenderSharesExistingClientWithoutConnecting(t *testing.T) {
	connection, err := NewConnection(context.Background(), filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	sender, ok := connection.TextSender().(*realTextSender)
	if !ok || sender.client != connection.client.(*whatsmeowConnectionClient).client {
		t.Fatal("sender does not share existing client")
	}
	if sender.aliases != connection.RealtimeSource().aliases || sender.lookup == nil {
		t.Fatal("sender does not share the connection's alias routing")
	}
}

func TestTextPreflightUsesAtomicConnectionStateWithoutTakingSocketLock(t *testing.T) {
	client := &fakeTextClient{connected: true, loggedIn: true}
	connectionClient := &whatsmeowConnectionClient{events: make(chan protocolEvent, 1)}
	sender := &realTextSender{client: client, ready: &connectionClient.textReady}
	chatID, _ := model.NewChatID("123@lid")
	if err := sender.ValidateText(chatID, "text"); !errors.Is(err, ErrTextUnavailable) {
		t.Fatal(err)
	}
	connectionClient.handleEvent(&events.Connected{})
	if err := sender.ValidateText(chatID, "text"); err != nil {
		t.Fatal(err)
	}
	connectionClient.handleEvent(&events.Disconnected{})
	if err := sender.ValidateText(chatID, "text"); !errors.Is(err, ErrTextUnavailable) {
		t.Fatal(err)
	}
	if client.readinessCalls.Load() != 0 || client.calls.Load() != 0 {
		t.Fatal("UI preflight touched upstream socket/transport")
	}
}

func TestRealTextSenderFailureAndMalformedResultDoNotInventSuccess(t *testing.T) {
	chatID, _ := model.NewChatID("123@lid")
	for _, upstreamFailure := range []bool{false, true} {
		client := &fakeTextClient{connected: true, loggedIn: true, send: func(context.Context, types.JID, *waE2E.Message) (whatsmeow.SendResponse, error) {
			if upstreamFailure {
				return whatsmeow.SendResponse{}, errors.New("private upstream detail")
			}
			return whatsmeow.SendResponse{}, nil
		}}
		event, err := (&realTextSender{client: client}).SendText(context.Background(), chatID, "text")
		if !errors.Is(err, ErrTextUncertain) || event.Message().MessageID().String() != "" || client.calls.Load() != 1 {
			t.Fatalf("event=%+v err=%v", event, err)
		}
	}
}

func TestRealTextSendSuccessAndIncomingEchoCommitOnce(t *testing.T) {
	testRealTextSendEchoCommitOnce(t, false)
}

// The pinned SendMessage converts PN destinations to LIDs. A valid echo can
// consequently carry the same message ID under its LID recipient identity.
// This regression intentionally requires one committed outcome across aliases.
func TestRealTextSendPNAndLIDEchoCommitOnce(t *testing.T) {
	testRealTextSendEchoCommitOnce(t, true)
}

func testRealTextSendEchoCommitOnce(t *testing.T, lidEcho bool, withReply ...bool) {
	t.Helper()
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
	text := "outgoing é 日本語 🐧"
	var sentPayload *waE2E.Message
	client := &fakeTextClient{connected: true, loggedIn: true, send: func(_ context.Context, _ types.JID, payload *waE2E.Message) (whatsmeow.SendResponse, error) {
		sentPayload = payload
		return whatsmeow.SendResponse{ID: "echo-id", Timestamp: base}, nil
	}}
	core, err := service.NewWithTextSender(realtimeCoreOptions(values), source, memory, policy, service.NewSystemClock(), &realTextSender{client: client, aliases: source.aliases})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- core.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	})
	waitForCoreReady(t, core.Updates())
	request, _ := service.NewSendTextRequest("12345@s.whatsapp.net", text)
	if len(withReply) == 1 && withReply[0] {
		quote, _ := model.NewTextQuote("quoted-original", "synthetic original é 日本語 👋", false)
		request, _ = service.NewSendTextRequest("12345@s.whatsapp.net", text, quote)
	}
	if err := core.SendText(ctx, request); err != nil {
		t.Fatal(err)
	}
	first := <-core.LiveEvents()
	if first.Message().MessageID().String() != "echo-id" || !first.Message().FromMe() || first.Message().Text() != text {
		t.Fatalf("live=%+v", first)
	}
	if first.Message().Quote() != request.Reply() {
		t.Fatal("committed live event lost outgoing quote (or added one to plain text)")
	}
	upstreamEcho := upstreamTextMessage("12345", "echo-id", base, true, &waE2E.Message{Conversation: &text})
	if len(withReply) == 1 && withReply[0] {
		if sentPayload.GetExtendedTextMessage().GetContextInfo().GetStanzaID() != "quoted-original" {
			t.Fatal("reply lost before transport")
		}
		upstreamEcho.Message = sentPayload
	}
	if lidEcho {
		upstreamEcho.Info.Chat = types.NewJID("987654", types.HiddenUserServer)
		upstreamEcho.Info.RecipientAlt = types.NewJID("12345", types.DefaultUserServer)
	}
	echo, ok := adaptTextMessage(upstreamEcho, base.Add(time.Second))
	if !ok {
		t.Fatal("echo rejected")
	}
	if echo.Message().Quote() != (model.TextQuote{}) {
		t.Fatal("this regression must exercise a quote-less incoming echo")
	}
	marker, ok := adaptTextMessage(upstreamTextMessage("12345", "marker", base.Add(time.Second), false, &waE2E.Message{Conversation: &text}), base.Add(time.Second))
	if !ok {
		t.Fatal("marker rejected")
	}
	if !source.admitWithAlternate(echo, messageChatAlternate(upstreamEcho.Info)) || !source.admit(marker) {
		t.Fatal("source admission")
	}
	if next := <-core.LiveEvents(); next.Message().MessageID().String() != "marker" {
		t.Fatalf("duplicate live event=%+v", next)
	}
	chatID, _ := model.NewChatID("12345@s.whatsapp.net")
	messages, _, err := memory.Page(ctx, chatID, model.NoCursor(), 10)
	if err != nil || len(messages) != 2 || client.calls.Load() != 1 {
		t.Fatalf("messages=%d calls=%d err=%v", len(messages), client.calls.Load(), err)
	}
	if messages[1].MessageID().String() != "echo-id" || messages[1].Quote() != request.Reply() {
		t.Fatal("duplicate PN/LID echo downgraded stored outgoing reply")
	}
}
