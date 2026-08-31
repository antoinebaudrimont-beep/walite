package service

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

// SendTextRequest is an immutable, bounded outgoing plain-text request.
// Transport-owned message identity and time are deliberately absent.
type SendTextRequest struct {
	chatID model.ChatID
	text   string
	reply  model.TextQuote
}

// NewSendTextRequest validates and owns one outgoing plain-text request.
func NewSendTextRequest(chatID, text string, quotes ...model.TextQuote) (SendTextRequest, error) {
	ownedChatID, err := model.NewChatID(chatID)
	if err != nil || text == "" || !utf8.ValidString(text) || len(text) > model.MaxRetainedTextBytes {
		return SendTextRequest{}, &CoreError{Kind: CoreMalformed, Operation: CoreOperationSend}
	}
	request := SendTextRequest{chatID: ownedChatID, text: strings.Clone(text)}
	if len(quotes) > 1 {
		return SendTextRequest{}, &CoreError{Kind: CoreMalformed, Operation: CoreOperationSend}
	}
	if len(quotes) == 1 {
		request.reply, err = model.NewTextQuote(quotes[0].MessageID().String(), quotes[0].Text(), quotes[0].FromMe())
		if err != nil {
			return SendTextRequest{}, &CoreError{Kind: CoreMalformed, Operation: CoreOperationSend}
		}
	}
	return request, nil
}

// ChatID returns the stable selected-chat identity.
func (request SendTextRequest) ChatID() model.ChatID { return request.chatID }

// Text returns the exact validated outgoing text.
func (request SendTextRequest) Text() string { return request.text }

// Reply returns the optional same-chat quote; the zero value means plain text.
func (request SendTextRequest) Reply() model.TextQuote { return request.reply }

func (request SendTextRequest) quotes() []model.TextQuote {
	if request.reply.MessageID().String() == "" {
		return nil
	}
	return []model.TextQuote{request.reply}
}

// SendText reserves bounded realtime capacity before obtaining a
// transport-owned outgoing event. Successful return means admission, not
// persistence; commit visibility remains Core.LiveEvents.
func (core *Core) SendText(ctx context.Context, request SendTextRequest) error {
	if core == nil || ctx == nil || core.sender == nil || !core.acceptingSends.Load() {
		return &CoreError{Kind: CoreClosed, Operation: CoreOperationSend}
	}
	owned, err := NewSendTextRequest(request.ChatID().String(), request.Text(), request.quotes()...)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return &CoreError{Kind: CoreCancelled, Operation: CoreOperationSend, Cause: err}
	}
	select {
	case core.sendSlot <- struct{}{}:
		defer func() { <-core.sendSlot }()
	default:
		return &CoreError{Kind: CoreBusy, Operation: CoreOperationSend}
	}
	reserved, err := core.realtimeQ.tryReserveCapacity(model.MaxNormalizedEventBytes)
	if err != nil {
		if errors.Is(err, errQueueFull) {
			return &CoreError{Kind: CoreBusy, Operation: CoreOperationSend}
		}
		if errors.Is(err, errQueueStopped) {
			return &CoreError{Kind: CoreClosed, Operation: CoreOperationSend}
		}
		return &CoreError{Kind: CoreInvariant, Operation: CoreOperationSend, Cause: err}
	}
	committed := false
	defer func() {
		if !committed {
			_ = reserved.release()
		}
	}()
	if err := ctx.Err(); err != nil {
		return &CoreError{Kind: CoreCancelled, Operation: CoreOperationSend, Cause: err}
	}

	event, err := core.sender.SendText(ctx, owned.ChatID(), owned.Text(), owned.quotes()...)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			cause := ctx.Err()
			if cause == nil {
				cause = err
			}
			return &CoreError{Kind: CoreCancelled, Operation: CoreOperationSend, Cause: cause}
		}
		return &CoreError{Kind: CoreDegraded, Operation: CoreOperationSend, Cause: err}
	}
	// The transport owns the new identity/time; the validated request owns the
	// same-chat quote. Retain it only after success, through the usual committed
	// path. No optimistic insertion or transport-specific metadata is needed.
	normalized, err := model.NewEvent(event.Message().WithQuote(owned.Reply()), event.ReceivedAt())
	if err != nil || normalized.Message().ChatID() != owned.ChatID() || !normalized.Message().FromMe() ||
		!normalized.Message().BodyRetained() || normalized.Message().Text() != owned.Text() {
		return &CoreError{Kind: CoreMalformed, Operation: CoreOperationSend}
	}
	if err := reserved.commit(normalized); err != nil {
		return &CoreError{Kind: CoreInvariant, Operation: CoreOperationSend, Cause: err}
	}
	committed = true
	return nil
}
