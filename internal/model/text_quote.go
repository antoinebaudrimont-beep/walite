package model

import (
	"strings"
	"unicode/utf8"
)

// MaxQuoteTextBytes bounds the quoted excerpt, not a message snapshot.
const MaxQuoteTextBytes = 1024

// TextQuote is an immutable same-chat reference shared by requests and messages.
// FromMe identifies which side of a direct conversation authored the quote.
// Media contains only a bounded presentation descriptor for a quoted media
// message; it never retains download metadata, bytes, or protocol types.
type TextQuote struct {
	id     MessageID
	text   string
	fromMe bool
	media  Media
}

func NewTextQuote(id, text string, fromMe bool) (TextQuote, error) {
	return newQuote(id, text, fromMe, Media{}, true)
}

// NewMediaQuote constructs a bounded same-chat media reference. Caption may be
// empty; the media kind/name are sufficient for a deterministic quote summary.
func NewMediaQuote(id, caption string, fromMe bool, media Media) (TextQuote, error) {
	if media.Kind() == 0 {
		return TextQuote{}, newValidationError(Required, "quote_media", 0)
	}
	owned, err := NewMedia(media.Kind(), media.Name(), media.MIMEType())
	if err != nil {
		return TextQuote{}, err
	}
	return newQuote(id, caption, fromMe, owned, false)
}

func newQuote(id, text string, fromMe bool, media Media, requireText bool) (TextQuote, error) {
	messageID, err := NewMessageID(id)
	if err != nil {
		return TextQuote{}, err
	}
	if requireText && text == "" {
		return TextQuote{}, newValidationError(Required, "quote_text", 0)
	}
	if len(text) > MaxRetainedTextBytes {
		return TextQuote{}, newValidationError(TooLong, "quote_text", MaxRetainedTextBytes)
	}
	if !utf8.ValidString(text) {
		return TextQuote{}, newValidationError(InvalidUTF8, "quote_text", MaxQuoteTextBytes)
	}
	end := len(text)
	if end > MaxQuoteTextBytes {
		end = MaxQuoteTextBytes
		for !utf8.RuneStart(text[end]) {
			end--
		}
	}
	return TextQuote{id: messageID, text: strings.Clone(text[:end]), fromMe: fromMe, media: media}, nil
}

func (quote TextQuote) MessageID() MessageID { return quote.id }
func (quote TextQuote) Text() string         { return quote.text }
func (quote TextQuote) FromMe() bool         { return quote.fromMe }
func (quote TextQuote) Media() Media         { return quote.media }

func cloneTextQuote(quote TextQuote) (TextQuote, error) {
	if quote == (TextQuote{}) {
		return TextQuote{}, nil
	}
	if len(quote.text) > MaxQuoteTextBytes {
		return TextQuote{}, newValidationError(TooLong, "quote_text", MaxQuoteTextBytes)
	}
	if quote.media.Kind() != 0 {
		return NewMediaQuote(quote.id.value, quote.text, quote.fromMe, quote.media)
	}
	return NewTextQuote(quote.id.value, quote.text, quote.fromMe)
}
