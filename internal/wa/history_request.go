package wa

import (
	"context"
	"errors"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

const MaxOlderHistoryMessages = 50

var (
	ErrHistoryRequestRejected    = errors.New("WhatsApp history request rejected")
	ErrHistoryRequestUnavailable = errors.New("WhatsApp history unavailable")
)

// HistoryRequester is the narrow capability used to ask the linked primary
// device for one bounded page immediately before a known message.
type HistoryRequester interface {
	RequestOlderHistory(context.Context, model.ChatID, model.MessageID, time.Time, bool, int) error
}

type historyRequestClient interface {
	IsConnected() bool
	IsLoggedIn() bool
	BuildHistorySyncRequest(*types.MessageInfo, int) *waE2E.Message
	SendPeerMessage(context.Context, *waE2E.Message) (whatsmeow.SendResponse, error)
}

type realHistoryRequester struct {
	client historyRequestClient
	ready  atomicBool
}

// atomicBool is the minimal load-only view shared with the connection-owned
// readiness flag. It avoids any additional connection or goroutine.
type atomicBool interface {
	Load() bool
}

// HistoryRequester uses the same long-lived client and session as realtime
// traffic. The ON_DEMAND response returns through the existing HistorySync
// event path.
func (connection *Connection) HistoryRequester() HistoryRequester {
	if connection == nil {
		return nil
	}
	client, ok := connection.client.(*whatsmeowConnectionClient)
	if !ok || client.client == nil {
		return nil
	}
	return &realHistoryRequester{client: client.client, ready: &client.textReady}
}

func (requester *realHistoryRequester) RequestOlderHistory(
	ctx context.Context,
	chatID model.ChatID,
	oldestID model.MessageID,
	oldestAt time.Time,
	oldestFromMe bool,
	count int,
) error {
	if ctx == nil || oldestAt.IsZero() || oldestID.String() == "" || count < 1 || count > MaxOlderHistoryMessages {
		return ErrHistoryRequestRejected
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	chat, err := textRecipient(chatID)
	if err != nil {
		return ErrHistoryRequestRejected
	}
	if requester == nil || requester.client == nil || requester.ready == nil || !requester.ready.Load() ||
		!requester.client.IsConnected() || !requester.client.IsLoggedIn() {
		return ErrHistoryRequestUnavailable
	}
	frontier := &types.MessageInfo{
		MessageSource: types.MessageSource{Chat: chat, IsFromMe: oldestFromMe, IsGroup: chat.Server == types.GroupServer},
		ID:            types.MessageID(oldestID.String()),
		Timestamp:     oldestAt,
	}
	payload := requester.client.BuildHistorySyncRequest(frontier, count)
	if payload == nil {
		return ErrHistoryRequestUnavailable
	}
	if _, err := requester.client.SendPeerMessage(ctx, payload); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrHistoryRequestUnavailable
	}
	return nil
}
