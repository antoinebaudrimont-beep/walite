package wa

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"go.mau.fi/whatsmeow/types"
)

type capturedReadCall struct {
	ids          []types.MessageID
	readAt       time.Time
	chat, sender types.JID
}

type fakeReadReceiptClient struct {
	connected, loggedIn bool
	err                 error
	mu                  sync.Mutex
	calls               []capturedReadCall
}

func (client *fakeReadReceiptClient) IsConnected() bool { return client.connected }
func (client *fakeReadReceiptClient) IsLoggedIn() bool  { return client.loggedIn }
func (client *fakeReadReceiptClient) MarkRead(_ context.Context, ids []types.MessageID, readAt time.Time, chat, sender types.JID, extras ...types.ReceiptType) error {
	if len(extras) != 0 {
		return errors.New("unexpected receipt type")
	}
	client.mu.Lock()
	client.calls = append(client.calls, capturedReadCall{ids: append([]types.MessageID(nil), ids...), readAt: readAt, chat: chat, sender: sender})
	client.mu.Unlock()
	return client.err
}

func receiptRequest(t *testing.T, chat string, group bool, inputs ...model.ReadReceiptMessageInput) model.ReadReceiptRequest {
	t.Helper()
	request, err := model.NewReadReceiptRequest(chat, group, inputs)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func TestDirectReadReceiptUsesStableIDsTimestampAndExistingAlias(t *testing.T) {
	base := time.Date(2026, 9, 1, 14, 0, 0, 0, time.UTC)
	pn := aliasID(t, "15551234567@s.whatsapp.net")
	lid := aliasID(t, "987654@lid")
	aliases := &chatAliases{}
	if _, err := aliases.resolve(pn, lid); err != nil {
		t.Fatal(err)
	}
	client := &fakeReadReceiptClient{connected: true, loggedIn: true}
	sender := &realReadReceiptSender{client: client, aliases: aliases, now: func() time.Time { return base }}
	request := receiptRequest(t, pn.String(), false,
		model.ReadReceiptMessageInput{MessageID: "stable-1", SentAt: base.Add(-time.Minute)},
		model.ReadReceiptMessageInput{MessageID: "stable-2", SentAt: base.Add(-time.Second)},
	)
	if err := sender.MarkRead(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(client.calls) != 1 || client.calls[0].chat.String() != pn.String() || !client.calls[0].sender.IsEmpty() ||
		client.calls[0].readAt != base || len(client.calls[0].ids) != 2 || client.calls[0].ids[0] != "stable-1" || client.calls[0].ids[1] != "stable-2" ||
		aliases.alternateFor(pn) != lid {
		t.Fatalf("calls=%+v alternate=%s", client.calls, aliases.alternateFor(pn).String())
	}
}

func TestGroupReadReceiptSplitsBoundedFrontierByParticipant(t *testing.T) {
	base := time.Date(2026, 9, 1, 15, 0, 0, 0, time.UTC)
	client := &fakeReadReceiptClient{connected: true, loggedIn: true}
	sender := &realReadReceiptSender{client: client, now: func() time.Time { return base }}
	request := receiptRequest(t, "12345-67890@g.us", true,
		model.ReadReceiptMessageInput{MessageID: "a1", SentAt: base.Add(-3 * time.Minute), SenderID: "111@s.whatsapp.net"},
		model.ReadReceiptMessageInput{MessageID: "b1", SentAt: base.Add(-2 * time.Minute), SenderID: "222@lid"},
		model.ReadReceiptMessageInput{MessageID: "a2", SentAt: base.Add(-time.Minute), SenderID: "111@s.whatsapp.net"},
	)
	if err := sender.MarkRead(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(client.calls) != 2 || client.calls[0].chat.String() != "12345-67890@g.us" || client.calls[1].chat.String() != "12345-67890@g.us" ||
		client.calls[0].sender.String() != "111@s.whatsapp.net" || len(client.calls[0].ids) != 2 || client.calls[0].ids[0] != "a1" || client.calls[0].ids[1] != "a2" ||
		client.calls[1].sender.String() != "222@lid" || len(client.calls[1].ids) != 1 || client.calls[1].ids[0] != "b1" {
		t.Fatalf("calls=%+v", client.calls)
	}
}

func TestReadReceiptUnavailableAndFailureAreControlledWithoutRetry(t *testing.T) {
	base := time.Unix(10, 0).UTC()
	request := receiptRequest(t, "123@lid", false, model.ReadReceiptMessageInput{MessageID: "id", SentAt: base})
	client := &fakeReadReceiptClient{connected: false, loggedIn: true}
	sender := &realReadReceiptSender{client: client, now: func() time.Time { return base }}
	if err := sender.MarkRead(context.Background(), request); !errors.Is(err, ErrReadReceiptUnavailable) || len(client.calls) != 0 {
		t.Fatalf("disconnected err=%v calls=%d", err, len(client.calls))
	}
	client.connected = true
	client.err = errors.New("private upstream failure")
	if err := sender.MarkRead(context.Background(), request); !errors.Is(err, ErrReadReceiptUnavailable) || len(client.calls) != 1 {
		t.Fatalf("failure err=%v calls=%d", err, len(client.calls))
	}
}

func TestConnectionReadReceiptSenderSharesExistingClientAndAliases(t *testing.T) {
	connection, err := NewConnection(context.Background(), filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	sender, ok := connection.ReadReceiptSender().(*realReadReceiptSender)
	client := connection.client.(*whatsmeowConnectionClient)
	if !ok || sender.client != client.client || sender.aliases != connection.RealtimeSource().aliases || sender.lookup == nil {
		t.Fatal("read sender does not share connection client and routing")
	}
}
