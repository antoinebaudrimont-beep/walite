package wa

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

var (
	ErrTextRejected          = errors.New("WhatsApp text request rejected")
	ErrTextUnavailable       = errors.New("WhatsApp text sending unavailable")
	ErrGroupReplyUnavailable = errors.New("group replies not available yet")
	// ErrTextUncertain means transport was invoked but no usable successful
	// result exists. The remote side may have accepted it: never auto-retry.
	ErrTextUncertain = errors.New("WhatsApp delivery unknown; check recipient before composing another message")
)

// TextSender is the walite-owned capability of the existing connection.
// ValidateText is local only. It cannot guarantee the connection stays online.
type TextSender interface {
	ValidateText(model.ChatID, string, ...model.TextQuote) error
	SendText(context.Context, model.ChatID, string, ...model.TextQuote) (model.Event, error)
}

type textClient interface {
	IsConnected() bool
	IsLoggedIn() bool
	SendMessage(context.Context, types.JID, *waE2E.Message, ...whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error)
}

type realTextSender struct {
	client  textClient
	ready   *atomic.Bool
	aliases *chatAliases
	lookup  alternateJIDLookup
	ownJID  func() types.JID
}

// TextSender uses the same client and session as authentication and incoming
// traffic. No client, store, goroutine, or network connection is created here.
func (connection *Connection) TextSender() TextSender {
	if connection == nil {
		return nil
	}
	client, ok := connection.client.(*whatsmeowConnectionClient)
	if !ok || client.client == nil || client.realtime == nil {
		return nil
	}
	return &realTextSender{client: client.client, ready: &client.textReady, aliases: client.realtime.aliases, lookup: client.realtime.lookup, ownJID: func() types.JID {
		device := client.client.Store
		if lid := device.GetLID(); !lid.IsEmpty() {
			return lid.ToNonAD()
		}
		return device.GetJID().ToNonAD()
	}}
}

func textRecipient(chatID model.ChatID) (types.JID, error) {
	raw := chatID.String()
	jid, err := types.ParseJID(raw)
	// ParseJID accepts server-only strings and surplus @ components. Require a
	// canonical non-device conversation JID, not a derived phone number.
	if err != nil || jid.User == "" || jid.Device != 0 || jid.RawAgent != 0 || jid.String() != raw ||
		strings.ContainsAny(jid.User, "@.:") || strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return types.JID{}, ErrTextRejected
	}
	switch jid.Server {
	case types.DefaultUserServer, types.HiddenUserServer, types.GroupServer:
		return jid, nil
	default:
		return types.JID{}, ErrTextRejected
	}
}

func (sender *realTextSender) ValidateText(chatID model.ChatID, text string, quotes ...model.TextQuote) error {
	jid, err := textRecipient(chatID)
	if err != nil {
		return err
	}
	if text == "" || len(text) > model.MaxRetainedTextBytes || !utf8.ValidString(text) {
		return ErrTextRejected
	}
	if len(quotes) > 1 {
		return ErrTextRejected
	}
	if len(quotes) == 1 {
		if jid.Server == types.GroupServer {
			return ErrGroupReplyUnavailable
		}
		if _, err := model.NewTextQuote(quotes[0].MessageID().String(), quotes[0].Text(), quotes[0].FromMe()); err != nil {
			return ErrTextRejected
		}
	}
	// UI preflight must not take whatsmeow's socket lock: ConnectContext holds
	// it across network work. Connection events provide this atomic hint.
	if sender == nil || sender.client == nil || sender.ready != nil && !sender.ready.Load() {
		return ErrTextUnavailable
	}
	return nil
}

func (sender *realTextSender) SendText(ctx context.Context, chatID model.ChatID, text string, quotes ...model.TextQuote) (model.Event, error) {
	if ctx == nil {
		return model.Event{}, ErrTextRejected
	}
	if err := ctx.Err(); err != nil {
		return model.Event{}, err
	}
	if err := sender.ValidateText(chatID, text, quotes...); err != nil {
		return model.Event{}, err
	}
	// Recheck the actual client only on the sender worker, never the UI loop.
	if !sender.client.IsConnected() || !sender.client.IsLoggedIn() {
		return model.Event{}, ErrTextUnavailable
	}
	jid, _ := textRecipient(chatID)
	// Pin the requested presentation identity before any remote operation so
	// even an echo delivered before SendMessage returns routes to this chat.
	canonical, err := sender.aliases.resolve(chatID, model.ChatID{})
	if err != nil {
		return model.Event{}, err
	}
	if canonical != chatID {
		return model.Event{}, ErrTextRejected
	}
	alternate, err := lookupAlternate(ctx, chatID, model.ChatID{}, sender.lookup)
	if err != nil {
		return model.Event{}, err
	}
	canonical, err = sender.aliases.resolve(chatID, alternate)
	if err != nil {
		return model.Event{}, err
	}
	if canonical != chatID {
		return model.Event{}, ErrTextRejected
	}
	payload := &waE2E.Message{Conversation: &text}
	if len(quotes) == 1 {
		payload, err = sender.quotedText(jid, alternate, text, quotes[0])
		if err != nil {
			return model.Event{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return model.Event{}, err
	}
	response, err := sender.client.SendMessage(ctx, jid, payload)
	if err != nil {
		// Do not expose upstream errors (which can contain private identifiers).
		return model.Event{}, ErrTextUncertain
	}
	// Success wins over concurrent cancellation. Never fabricate identity/time.
	message, err := model.NewMessage(model.MessageInput{
		ChatID: chatID.String(), MessageID: string(response.ID), SentAt: response.Timestamp,
		FromMe: true, Text: text,
	})
	if err != nil {
		return model.Event{}, ErrTextUncertain
	}
	event, err := model.NewEvent(message, response.Timestamp)
	if err != nil {
		return model.Event{}, ErrTextUncertain
	}
	return event, nil
}

// quotedText builds only a same-chat text quote. RemoteJID is deliberately
// absent: cross-chat quotes are unsupported. No original protobuf is retained.
func (sender *realTextSender) quotedText(peer types.JID, alternate model.ChatID, text string, quote model.TextQuote) (*waE2E.Message, error) {
	participant := peer
	if other, ok := directChatJID(alternate); ok && other.Server == types.HiddenUserServer {
		participant = other
	}
	if quote.FromMe() {
		if sender.ownJID == nil {
			return nil, ErrTextUnavailable
		}
		participant = sender.ownJID().ToNonAD()
	}
	id, err := model.NewChatID(participant.String())
	if err != nil {
		return nil, ErrTextUnavailable
	}
	if _, ok := directChatJID(id); !ok {
		return nil, ErrTextUnavailable
	}
	stanza, author, quoted := quote.MessageID().String(), participant.String(), quote.Text()
	return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
		Text: &text,
		ContextInfo: &waE2E.ContextInfo{
			StanzaID: &stanza, Participant: &author,
			QuotedMessage: &waE2E.Message{Conversation: &quoted},
		},
	}}, nil
}
