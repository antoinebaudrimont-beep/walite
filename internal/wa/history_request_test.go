package wa

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

type fakeHistoryRequestClient struct {
	connected, loggedIn bool
	buildCalls          atomic.Int32
	sendCalls           atomic.Int32
	frontier            *types.MessageInfo
	count               int
	payload             *waE2E.Message
	sendErr             error
}

func (client *fakeHistoryRequestClient) IsConnected() bool { return client.connected }
func (client *fakeHistoryRequestClient) IsLoggedIn() bool  { return client.loggedIn }
func (client *fakeHistoryRequestClient) BuildHistorySyncRequest(frontier *types.MessageInfo, count int) *waE2E.Message {
	client.buildCalls.Add(1)
	client.frontier, client.count = frontier, count
	client.payload = (&whatsmeow.Client{}).BuildHistorySyncRequest(frontier, count)
	return client.payload
}
func (client *fakeHistoryRequestClient) SendPeerMessage(_ context.Context, payload *waE2E.Message) (whatsmeow.SendResponse, error) {
	client.sendCalls.Add(1)
	if payload != client.payload {
		return whatsmeow.SendResponse{}, errors.New("payload identity changed")
	}
	return whatsmeow.SendResponse{}, client.sendErr
}

func TestOlderHistoryRequestBuildsExactBoundedFrontierAndSendsOnce(t *testing.T) {
	for _, rawChat := range []string{"15551234567@s.whatsapp.net", "123456789@lid", "12345-67890@g.us"} {
		t.Run(rawChat, func(t *testing.T) {
			chatID, _ := model.NewChatID(rawChat)
			messageID, _ := model.NewMessageID("oldest-message")
			at := time.Date(2026, 9, 9, 12, 34, 56, 0, time.UTC)
			client := &fakeHistoryRequestClient{connected: true, loggedIn: true}
			var ready atomic.Bool
			ready.Store(true)
			requester := &realHistoryRequester{client: client, ready: &ready}
			if err := requester.RequestOlderHistory(context.Background(), chatID, messageID, at, true, MaxOlderHistoryMessages); err != nil {
				t.Fatal(err)
			}
			if client.buildCalls.Load() != 1 || client.sendCalls.Load() != 1 || client.count != 50 {
				t.Fatalf("build=%d send=%d count=%d", client.buildCalls.Load(), client.sendCalls.Load(), client.count)
			}
			if client.frontier.Chat.String() != rawChat || client.frontier.ID != "oldest-message" || !client.frontier.Timestamp.Equal(at) || !client.frontier.IsFromMe || client.frontier.IsGroup != (client.frontier.Chat.Server == types.GroupServer) {
				t.Fatalf("frontier=%+v", client.frontier)
			}
			request := client.payload.GetProtocolMessage().GetPeerDataOperationRequestMessage().GetHistorySyncOnDemandRequest()
			if request.GetChatJID() != rawChat || request.GetOldestMsgID() != "oldest-message" || !request.GetOldestMsgFromMe() || request.GetOnDemandMsgCount() != 50 || request.GetOldestMsgTimestampMS() != at.Unix() {
				t.Fatalf("protobuf request=%+v", request)
			}
		})
	}
}

func TestOlderHistoryRequestRejectsBeforeTransportAndReportsUnavailable(t *testing.T) {
	chatID, _ := model.NewChatID("123@lid")
	messageID, _ := model.NewMessageID("oldest")
	at := time.Now().UTC()
	client := &fakeHistoryRequestClient{connected: true, loggedIn: true}
	var ready atomic.Bool
	requester := &realHistoryRequester{client: client, ready: &ready}
	if err := requester.RequestOlderHistory(context.Background(), chatID, messageID, at, false, 50); !errors.Is(err, ErrHistoryRequestUnavailable) {
		t.Fatal(err)
	}
	ready.Store(true)
	for _, count := range []int{0, 51} {
		if err := requester.RequestOlderHistory(context.Background(), chatID, messageID, at, false, count); !errors.Is(err, ErrHistoryRequestRejected) {
			t.Fatalf("count=%d err=%v", count, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := requester.RequestOlderHistory(ctx, chatID, messageID, at, false, 50); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	client.connected = false
	if err := requester.RequestOlderHistory(context.Background(), chatID, messageID, at, false, 50); !errors.Is(err, ErrHistoryRequestUnavailable) {
		t.Fatal(err)
	}
	if client.sendCalls.Load() != 0 {
		t.Fatalf("rejected requests reached transport: %d", client.sendCalls.Load())
	}
}
