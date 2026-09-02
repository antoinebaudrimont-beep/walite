package wa

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"go.mau.fi/whatsmeow/types"
)

var (
	ErrReadReceiptRejected    = errors.New("WhatsApp read receipt rejected")
	ErrReadReceiptUnavailable = errors.New("WhatsApp read receipt unavailable")
)

// ReadReceiptSender is the transport side-effect capability of the existing
// connection. It creates no client, store, worker, or network connection.
type ReadReceiptSender interface {
	MarkRead(context.Context, model.ReadReceiptRequest) error
}

type readReceiptClient interface {
	IsConnected() bool
	IsLoggedIn() bool
	MarkRead(context.Context, []types.MessageID, time.Time, types.JID, types.JID, ...types.ReceiptType) error
}

type realReadReceiptSender struct {
	client  readReceiptClient
	ready   *atomic.Bool
	aliases *chatAliases
	lookup  alternateJIDLookup
	now     func() time.Time
}

// ReadReceiptSender shares the same long-lived client and PN/LID registry as
// incoming and outgoing text traffic.
func (connection *Connection) ReadReceiptSender() ReadReceiptSender {
	if connection == nil {
		return nil
	}
	client, ok := connection.client.(*whatsmeowConnectionClient)
	if !ok || client.client == nil || client.realtime == nil {
		return nil
	}
	return &realReadReceiptSender{
		client: client.client, ready: &client.textReady, aliases: client.realtime.aliases,
		lookup: client.realtime.lookup, now: time.Now,
	}
}

func (sender *realReadReceiptSender) MarkRead(ctx context.Context, request model.ReadReceiptRequest) error {
	if ctx == nil || request.Len() == 0 || request.Len() > model.MaxReadReceiptMessages {
		return ErrReadReceiptRejected
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if sender == nil || sender.client == nil || sender.ready != nil && !sender.ready.Load() ||
		!sender.client.IsConnected() || !sender.client.IsLoggedIn() {
		return ErrReadReceiptUnavailable
	}
	chat, err := textRecipient(request.ChatID())
	if err != nil || request.IsGroup() != (chat.Server == types.GroupServer) {
		return ErrReadReceiptRejected
	}
	if chat.Server != types.GroupServer {
		alternate := sender.aliases.alternateFor(request.ChatID())
		alternate, err = lookupAlternate(ctx, request.ChatID(), alternate, sender.lookup)
		if err != nil {
			return err
		}
		canonical, err := sender.aliases.resolveForSend(request.ChatID(), alternate)
		if err != nil {
			return err
		}
		if canonical != request.ChatID() {
			return ErrReadReceiptRejected
		}
	}
	readAt := time.Now().UTC()
	if sender.now != nil {
		readAt = sender.now().UTC()
	}
	if readAt.IsZero() {
		return ErrReadReceiptRejected
	}
	if chat.Server != types.GroupServer {
		var ids [model.MaxReadReceiptMessages]types.MessageID
		for index := 0; index < request.Len(); index++ {
			message, _ := request.At(index)
			ids[index] = types.MessageID(message.MessageID().String())
		}
		return sender.markBatch(ctx, ids[:request.Len()], readAt, chat, types.EmptyJID)
	}

	// Upstream allows one sender per call. Fixed arrays split a mixed-participant
	// group frontier without maps or storage beyond the 32-message request bound.
	type groupBatch struct {
		sender types.JID
		ids    [model.MaxReadReceiptMessages]types.MessageID
		count  int
	}
	var batches [model.MaxReadReceiptMessages]groupBatch
	batchCount := 0
	for index := 0; index < request.Len(); index++ {
		message, _ := request.At(index)
		senderID, err := model.NewChatID(message.SenderID().String())
		if err != nil {
			return ErrReadReceiptRejected
		}
		participant, direct := directChatJID(senderID)
		if !direct {
			return ErrReadReceiptRejected
		}
		batchIndex := -1
		for candidate := 0; candidate < batchCount; candidate++ {
			if batches[candidate].sender.ToNonAD() == participant.ToNonAD() {
				batchIndex = candidate
				break
			}
		}
		if batchIndex < 0 {
			batchIndex = batchCount
			batchCount++
			batches[batchIndex].sender = participant.ToNonAD()
		}
		batch := &batches[batchIndex]
		batch.ids[batch.count] = types.MessageID(message.MessageID().String())
		batch.count++
	}
	for index := 0; index < batchCount; index++ {
		if err := sender.markBatch(ctx, batches[index].ids[:batches[index].count], readAt, chat, batches[index].sender); err != nil {
			return err
		}
	}
	return nil
}

func (sender *realReadReceiptSender) markBatch(ctx context.Context, ids []types.MessageID, readAt time.Time, chat, participant types.JID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := sender.client.MarkRead(ctx, ids, readAt, chat, participant); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrReadReceiptUnavailable
	}
	return nil
}
