package wa

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
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
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
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

func TestHistorySyncKnownAliasSendsWithoutRedundantLookup(t *testing.T) {
	for _, chat := range []string{"12345@s.whatsapp.net", "98765@lid"} {
		t.Run(chat, func(t *testing.T) {
			primary := aliasID(t, chat)
			alternate := aliasID(t, map[string]string{
				"12345@s.whatsapp.net": "98765@lid",
				"98765@lid":            "12345@s.whatsapp.net",
			}[chat])
			aliases := &chatAliases{}
			if _, err := aliases.resolve(primary, alternate); err != nil {
				t.Fatal(err)
			}
			var lookups atomic.Int32
			client := &fakeTextClient{connected: true, loggedIn: true, send: func(context.Context, types.JID, *waE2E.Message) (whatsmeow.SendResponse, error) {
				return whatsmeow.SendResponse{ID: "history-send", Timestamp: time.Now().UTC()}, nil
			}}
			sender := &realTextSender{client: client, aliases: aliases, lookup: func(context.Context, types.JID) (types.JID, error) {
				lookups.Add(1)
				return types.EmptyJID, errors.New("historical mapping absent from session lookup")
			}}
			if _, err := sender.SendText(context.Background(), aliasID(t, chat), "send from history"); err != nil {
				t.Fatal(err)
			}
			quote, _ := model.NewTextQuote("history-original", "quoted Café 👋", false)
			if _, err := sender.SendText(context.Background(), aliasID(t, chat), "reply from history", quote); err != nil {
				t.Fatal(err)
			}
			if lookups.Load() != 0 || client.calls.Load() != 2 {
				t.Fatalf("lookups=%d transport calls=%d", lookups.Load(), client.calls.Load())
			}
		})
	}
}

func TestCacheSeededPNAndLIDRowsRemainSendable(t *testing.T) {
	for _, chat := range []string{"12345@s.whatsapp.net", "98765@lid"} {
		t.Run(chat, func(t *testing.T) {
			pn := aliasID(t, "12345@s.whatsapp.net")
			lid := aliasID(t, "98765@lid")
			aliases := &chatAliases{}
			// Production cache seeding knows stable rows, but not their relation.
			for _, id := range []model.ChatID{pn, lid} {
				if _, err := aliases.resolve(id, model.ChatID{}); err != nil {
					t.Fatal(err)
				}
			}
			client := &fakeTextClient{connected: true, loggedIn: true, send: func(context.Context, types.JID, *waE2E.Message) (whatsmeow.SendResponse, error) {
				return whatsmeow.SendResponse{ID: "cache-seeded-send", Timestamp: time.Now().UTC()}, nil
			}}
			sender := &realTextSender{client: client, aliases: aliases, lookup: func(_ context.Context, jid types.JID) (types.JID, error) {
				if jid.Server == types.DefaultUserServer {
					return types.NewJID("98765", types.HiddenUserServer), nil
				}
				return types.NewJID("12345", types.DefaultUserServer), nil
			}}
			if _, err := sender.SendText(context.Background(), aliasID(t, chat), "cache-seeded send"); err != nil {
				t.Fatal(err)
			}
			quote, _ := model.NewTextQuote("cache-original", "quoted from cache", false)
			if _, err := sender.SendText(context.Background(), aliasID(t, chat), "cache-seeded reply", quote); err != nil {
				t.Fatal(err)
			}
			if aliases.count != 1 || aliases.alternateFor(aliasID(t, chat)).String() == "" || client.calls.Load() != 2 {
				t.Fatalf("aliases=%d alternate=%q transport calls=%d", aliases.count, aliases.alternateFor(aliasID(t, chat)).String(), client.calls.Load())
			}
		})
	}
}

func TestHistorySyncChatSendsCommitOnceThroughSQLite(t *testing.T) {
	for _, cacheSplit := range []bool{false, true} {
		for _, chat := range []string{"12345@s.whatsapp.net", "98765@lid"} {
			for _, reply := range []bool{false, true} {
				name := map[bool]string{false: "history/", true: "cache-split/"}[cacheSplit] + chat + map[bool]string{false: "/plain", true: "/reply"}[reply]
				t.Run(name, func(t *testing.T) {
					source := newRealtimeSource()
					at := time.Date(2026, 9, 1, 12, 30, 0, 0, time.UTC)
					var record model.BootstrapRecord
					if cacheSplit {
						pn, lid := aliasID(t, "12345@s.whatsapp.net"), aliasID(t, "98765@lid")
						if err := source.SeedChatIDs([]model.ChatID{pn, lid}); err != nil {
							t.Fatal(err)
						}
						selected, err := model.NewChat(model.ChatInput{ID: chat, LastMessageAt: at.Add(-time.Minute), UpdatedAt: at})
						if err != nil {
							t.Fatal(err)
						}
						record, err = model.NewBootstrapRecord(model.BootstrapFull, selected, nil)
						if err != nil {
							t.Fatal(err)
						}
					} else {
						bootstrap := bootstrapTestClient(at)
						bootstrap.realtime = source
						conversation := &waHistorySync.Conversation{ID: stringPointer(chat), LastMsgTimestamp: uint64Pointer(uint64(at.Add(-time.Minute).Unix()))}
						if strings.HasSuffix(chat, types.DefaultUserServer) {
							conversation.LidJID = stringPointer("98765@lid")
						} else {
							conversation.PnJID = stringPointer("12345@s.whatsapp.net")
						}
						var ok bool
						record, ok = bootstrap.adaptBootstrapConversation(model.BootstrapFull, conversation, at)
						if !ok || record.Chat().ID().String() != chat {
							t.Fatalf("history chat=%q accepted=%t", record.Chat().ID().String(), ok)
						}
					}

					cache, err := store.OpenSQLite(context.Background(), store.SQLiteOptions{Path: filepath.Join(t.TempDir(), "cache.db")})
					if err != nil {
						t.Fatal(err)
					}
					defer cache.Close()
					if err := cache.EnsureChat(context.Background(), record.Chat()); err != nil {
						t.Fatal(err)
					}
					if cacheSplit {
						other := "98765@lid"
						if chat == other {
							other = "12345@s.whatsapp.net"
						}
						otherChat, _ := model.NewChat(model.ChatInput{ID: other, LastMessageAt: at.Add(-2 * time.Minute), UpdatedAt: at})
						if err := cache.EnsureChat(context.Background(), otherChat); err != nil {
							t.Fatal(err)
						}
					}
					var sentPayload *waE2E.Message
					transport := &fakeTextClient{connected: true, loggedIn: true, send: func(_ context.Context, jid types.JID, payload *waE2E.Message) (whatsmeow.SendResponse, error) {
						if jid.String() != chat {
							t.Errorf("destination=%q", jid.String())
						}
						sentPayload = payload
						return whatsmeow.SendResponse{ID: "sqlite-send", Timestamp: at}, nil
					}}
					sender := &realTextSender{client: transport, aliases: source.aliases, lookup: func(_ context.Context, jid types.JID) (types.JID, error) {
						if cacheSplit {
							if jid.Server == types.DefaultUserServer {
								return types.NewJID("98765", types.HiddenUserServer), nil
							}
							return types.NewJID("12345", types.DefaultUserServer), nil
						}
						return types.EmptyJID, errors.New("redundant lookup must not run")
					}}
					values := config.DefaultValues()
					policy, err := syncpolicy.New(values.Retention)
					if err != nil {
						t.Fatal(err)
					}
					core, err := service.NewWithTextSender(realtimeCoreOptions(values), source, cache, policy, service.NewSystemClock(), sender)
					if err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithCancel(context.Background())
					done := make(chan error, 1)
					go func() { done <- core.Run(ctx) }()
					waitForCoreReady(t, core.Updates())
					request, _ := service.NewSendTextRequest(chat, "history sqlite send 👋")
					var quote model.TextQuote
					if reply {
						quote, _ = model.NewTextQuote("history-original", "quoted Café 👋", false)
						request, _ = service.NewSendTextRequest(chat, "history sqlite send 👋", quote)
					}
					if err := core.SendText(ctx, request); err != nil {
						cancel()
						t.Fatal(err)
					}
					committed := <-core.LiveEvents()
					cancel()
					if err := <-done; !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
					messages, _, err := cache.Page(context.Background(), record.Chat().ID(), model.NoCursor(), 10)
					if err != nil || len(messages) != 1 || committed.Message().MessageID().String() != "sqlite-send" || messages[0].Quote() != quote || transport.calls.Load() != 1 {
						t.Fatalf("messages=%d committed=%+v calls=%d err=%v", len(messages), committed, transport.calls.Load(), err)
					}
					if reply != (sentPayload.GetExtendedTextMessage().GetContextInfo().GetStanzaID() == "history-original") {
						t.Fatal("plain/reply payload changed")
					}
				})
			}
		}
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
		// Deliberately quote-less: this regression protects the authoritative
		// outgoing quote even when an incoming echo omits ContextInfo.
		upstreamEcho.Message = &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: &text}}
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
