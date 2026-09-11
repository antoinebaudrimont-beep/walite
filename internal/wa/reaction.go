package wa

import (
	"context"
	"errors"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

var (
	ErrReactionRejected    = errors.New("WhatsApp reaction request rejected")
	ErrReactionUnavailable = errors.New("WhatsApp reaction sending unavailable")
	ErrReactionUncertain   = errors.New("WhatsApp reaction delivery unknown; check the message before trying again")
)

// ReactionSender is the narrow reaction capability of the connection-owned
// whatsmeow client. Empty Emoji removes the linked account's reaction.
type ReactionSender interface {
	ValidateReaction(model.SendReactionRequest) error
	SendReaction(context.Context, model.SendReactionRequest) (model.Reaction, error)
}

type reactionClient interface {
	textClient
	BuildReaction(types.JID, types.JID, types.MessageID, string) *waE2E.Message
}

func (sender *realTextSender) ValidateReaction(request model.SendReactionRequest) error {
	if sender == nil || sender.client == nil {
		return ErrReactionRejected
	}
	chat, err := textRecipient(request.ChatID())
	client, available := sender.client.(reactionClient)
	if err != nil || !available || client == nil {
		return ErrReactionRejected
	}
	if request.IsGroup() != (chat.Server == types.GroupServer) {
		return ErrReactionRejected
	}
	if request.IsGroup() && !request.TargetFromMe() {
		targetID, idErr := model.NewChatID(request.TargetSenderID().String())
		target, err := textRecipient(targetID)
		if idErr != nil || err != nil || target.Server == types.GroupServer {
			return ErrReactionRejected
		}
	}
	if sender.ready != nil && !sender.ready.Load() {
		return ErrReactionUnavailable
	}
	return nil
}

func (sender *realTextSender) SendReaction(ctx context.Context, request model.SendReactionRequest) (model.Reaction, error) {
	if ctx == nil {
		return model.Reaction{}, ErrReactionRejected
	}
	if err := ctx.Err(); err != nil {
		return model.Reaction{}, err
	}
	if err := sender.ValidateReaction(request); err != nil {
		return model.Reaction{}, err
	}
	client, ok := sender.client.(reactionClient)
	if !ok || !client.IsConnected() || !client.IsLoggedIn() {
		return model.Reaction{}, ErrReactionUnavailable
	}
	chat, _ := textRecipient(request.ChatID())
	alternate := sender.aliases.alternateFor(request.ChatID())
	alternate, err := lookupAlternate(ctx, request.ChatID(), alternate, sender.lookup)
	if err != nil {
		return model.Reaction{}, err
	}
	canonical, err := sender.aliases.resolveForSend(request.ChatID(), alternate)
	if err != nil || canonical != request.ChatID() {
		return model.Reaction{}, ErrReactionRejected
	}
	targetSender := types.EmptyJID
	if !request.TargetFromMe() {
		if request.IsGroup() {
			targetID, _ := model.NewChatID(request.TargetSenderID().String())
			targetSender, _ = textRecipient(targetID)
		} else {
			targetSender = chat
		}
	}
	payload := client.BuildReaction(chat, targetSender, types.MessageID(request.TargetMessageID().String()), request.Emoji())
	if payload == nil || payload.GetReactionMessage() == nil {
		return model.Reaction{}, ErrReactionRejected
	}
	if err := ctx.Err(); err != nil {
		return model.Reaction{}, err
	}
	response, err := client.SendMessage(ctx, chat, payload)
	if err != nil {
		return model.Reaction{}, ErrReactionUncertain
	}
	reaction, err := model.NewReaction(model.ReactionInput{
		ChatID: request.ChatID().String(), TargetMessageID: request.TargetMessageID().String(),
		ReactorID: model.SelfReactorID, Emoji: request.Emoji(), UpdatedAt: response.Timestamp,
	})
	if err != nil {
		return model.Reaction{}, ErrReactionUncertain
	}
	return reaction, nil
}
