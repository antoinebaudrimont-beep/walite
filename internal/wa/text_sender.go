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
		if _, err := ownedTextQuote(quotes[0]); err != nil {
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
	// HistorySync and live traffic may already have supplied the authoritative
	// PN/LID pair. Reuse it: a broad historical chat is still sendable even when
	// the session store cannot independently answer a redundant lookup.
	alternate := sender.aliases.alternateFor(chatID)
	alternate, err := lookupAlternate(ctx, chatID, alternate, sender.lookup)
	if err != nil {
		return model.Event{}, err
	}
	// Install the selected route before any remote operation so an echo that
	// arrives before SendMessage returns is normalized to this visible chat.
	canonical, err := sender.aliases.resolveForSend(chatID, alternate)
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

// quotedText builds a same-chat quote. RemoteJID is deliberately absent:
// cross-chat quotes are unsupported. Media quotes reconstruct only the bounded
// content family/metadata WhatsApp needs for quote presentation; download
// credentials, bytes, and original protobufs are not retained.
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
	stanza, author := quote.MessageID().String(), participant.String()
	quoted := quotedMessage(quote)
	if quoted == nil {
		return nil, ErrTextRejected
	}
	return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
		Text: &text,
		ContextInfo: &waE2E.ContextInfo{
			StanzaID: &stanza, Participant: &author,
			QuotedMessage: quoted,
		},
	}}, nil
}

func ownedTextQuote(quote model.TextQuote) (model.TextQuote, error) {
	if quote.Media().Kind() != 0 {
		return model.NewMediaQuote(quote.MessageID().String(), quote.Text(), quote.FromMe(), quote.Media())
	}
	return model.NewTextQuote(quote.MessageID().String(), quote.Text(), quote.FromMe())
}

func quotedMessage(quote model.TextQuote) *waE2E.Message {
	text, media := quote.Text(), quote.Media()
	if media.Kind() == 0 {
		return &waE2E.Message{Conversation: &text}
	}
	mime, name := media.MIMEType(), media.Name()
	switch media.Kind() {
	case model.MediaImage:
		return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: optionalString(mime), Caption: optionalString(text)}}
	case model.MediaVideo:
		return &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Mimetype: optionalString(mime), Caption: optionalString(text)}}
	case model.MediaDocument:
		return &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{Mimetype: optionalString(mime), FileName: optionalString(name), Caption: optionalString(text)}}
	case model.MediaAudio:
		return &waE2E.Message{AudioMessage: &waE2E.AudioMessage{Mimetype: optionalString(mime)}}
	case model.MediaSticker:
		return &waE2E.Message{StickerMessage: &waE2E.StickerMessage{Mimetype: optionalString(mime)}}
	default:
		return nil
	}
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
