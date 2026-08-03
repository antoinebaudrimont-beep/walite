package model

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxIdentifierBytes is the hard byte limit for chat and message IDs.
	MaxIdentifierBytes = 512
	// MaxRetainedTextBytes is the hard byte limit for normalized message text.
	MaxRetainedTextBytes = 16 * 1024
	// MaxNormalizedEventBytes is the hard conservative charge for one event.
	MaxNormalizedEventBytes = 32 * 1024

	normalizedEventEnvelopeBytes = 256
)

// ValidationCode identifies a content-free model validation failure.
type ValidationCode string

const (
	Required         ValidationCode = "required"
	InvalidUTF8      ValidationCode = "invalid_utf8"
	ControlCharacter ValidationCode = "control_character"
	TooLong          ValidationCode = "too_long"
	SizeExceeded     ValidationCode = "size_exceeded"
)

// ValidationError describes a model construction failure without retaining or
// rendering the rejected value.
type ValidationError struct {
	Code  ValidationCode
	Field string
	Limit int
}

// Error implements error without disclosing rejected identifiers or text.
func (err *ValidationError) Error() string {
	if err == nil {
		return "model validation failed"
	}
	if err.Limit > 0 {
		return fmt.Sprintf(
			"model validation failed: field %s: %s (byte limit %d)",
			err.Field,
			err.Code,
			err.Limit,
		)
	}
	return fmt.Sprintf("model validation failed: field %s: %s", err.Field, err.Code)
}

// NewChatID validates and takes bounded ownership of a chat identifier.
func NewChatID(value string) (ChatID, error) {
	if err := validateIdentifier(value, "chat_id"); err != nil {
		return ChatID{}, err
	}
	return ChatID{value: strings.Clone(value)}, nil
}

// NewMessageID validates and takes bounded ownership of a message identifier.
func NewMessageID(value string) (MessageID, error) {
	if err := validateIdentifier(value, "message_id"); err != nil {
		return MessageID{}, err
	}
	return MessageID{value: strings.Clone(value)}, nil
}

// NewMessage validates identity and time, then retains only normalized bounded
// text owned by the returned value.
func NewMessage(input MessageInput) (Message, error) {
	chatID, err := NewChatID(input.ChatID)
	if err != nil {
		return Message{}, err
	}
	messageID, err := NewMessageID(input.MessageID)
	if err != nil {
		return Message{}, err
	}
	if input.SentAt.IsZero() {
		return Message{}, newValidationError(Required, "sent_at", 0)
	}

	text, truncated := NormalizeText(input.Text)
	byteSize, ok := messageByteSize(chatID.value, messageID.value, text)
	if !ok {
		return Message{}, newValidationError(
			SizeExceeded,
			"message",
			MaxNormalizedEventBytes-normalizedEventEnvelopeBytes,
		)
	}

	return Message{
		chatID:        chatID,
		messageID:     messageID,
		sentAt:        input.SentAt,
		fromMe:        input.FromMe,
		text:          text,
		bodyTruncated: truncated,
		bodyRetained:  true,
		byteSize:      byteSize,
	}, nil
}

// NewEvent validates a normalized message and receipt timestamp, then applies
// the conservative event-envelope charge.
func NewEvent(message Message, receivedAt time.Time) (Event, error) {
	if receivedAt.IsZero() {
		return Event{}, newValidationError(Required, "received_at", 0)
	}

	messageBytes, err := validateMessage(message)
	if err != nil {
		return Event{}, err
	}
	eventBytes, err := normalizedEventByteSize(messageBytes)
	if err != nil {
		return Event{}, err
	}

	message.byteSize = messageBytes
	return Event{
		message:    message,
		receivedAt: receivedAt,
		byteSize:   eventBytes,
	}, nil
}

// NormalizeText replaces malformed UTF-8 and retains at most
// MaxRetainedTextBytes. It never scans or allocates in proportion to input
// beyond the bounded prefix needed for the result.
func NormalizeText(value string) (text string, truncated bool) {
	if value == "" {
		return "", false
	}

	prefixBytes := len(value)
	if prefixBytes > MaxRetainedTextBytes {
		prefixBytes = MaxRetainedTextBytes
	}
	prefix := value[:prefixBytes]
	if utf8.ValidString(prefix) {
		return strings.Clone(prefix), prefixBytes < len(value)
	}

	var normalized strings.Builder
	normalized.Grow(prefixBytes)
	for offset := 0; offset < len(value); {
		r, sourceBytes := utf8.DecodeRuneInString(value[offset:])
		outputBytes := sourceBytes
		if r == utf8.RuneError && sourceBytes == 1 {
			outputBytes = utf8.RuneLen(utf8.RuneError)
		}
		if normalized.Len() > MaxRetainedTextBytes-outputBytes {
			return normalized.String(), true
		}

		if r == utf8.RuneError && sourceBytes == 1 {
			normalized.WriteRune(utf8.RuneError)
		} else {
			normalized.WriteString(value[offset : offset+sourceBytes])
		}
		offset += sourceBytes
	}

	return normalized.String(), false
}

func validateIdentifier(value, field string) *ValidationError {
	if value == "" {
		return newValidationError(Required, field, 0)
	}
	if len(value) > MaxIdentifierBytes {
		return newValidationError(TooLong, field, MaxIdentifierBytes)
	}
	if !utf8.ValidString(value) {
		return newValidationError(InvalidUTF8, field, MaxIdentifierBytes)
	}
	for _, r := range value {
		if r == 0 || unicode.IsControl(r) {
			return newValidationError(ControlCharacter, field, MaxIdentifierBytes)
		}
	}
	return nil
}

func validateMessage(message Message) (int, error) {
	if err := validateIdentifier(message.chatID.value, "chat_id"); err != nil {
		return 0, err
	}
	if err := validateIdentifier(message.messageID.value, "message_id"); err != nil {
		return 0, err
	}
	if message.sentAt.IsZero() {
		return 0, newValidationError(Required, "sent_at", 0)
	}
	if len(message.text) > MaxRetainedTextBytes {
		return 0, newValidationError(TooLong, "text", MaxRetainedTextBytes)
	}
	if !utf8.ValidString(message.text) {
		return 0, newValidationError(InvalidUTF8, "text", MaxRetainedTextBytes)
	}

	byteSize, ok := messageByteSize(
		message.chatID.value,
		message.messageID.value,
		message.text,
	)
	if !ok {
		return 0, newValidationError(
			SizeExceeded,
			"message",
			MaxNormalizedEventBytes-normalizedEventEnvelopeBytes,
		)
	}
	return byteSize, nil
}

func messageByteSize(chatID, messageID, text string) (int, bool) {
	return checkedByteSum(len(chatID), len(messageID), len(text))
}

func normalizedEventByteSize(messageBytes int) (int, error) {
	byteSize, ok := checkedByteSum(normalizedEventEnvelopeBytes, messageBytes)
	if !ok || byteSize > MaxNormalizedEventBytes {
		return 0, newValidationError(
			SizeExceeded,
			"event",
			MaxNormalizedEventBytes,
		)
	}
	return byteSize, nil
}

func checkedByteSum(parts ...int) (int, bool) {
	const maxInt = int(^uint(0) >> 1)

	total := 0
	for _, part := range parts {
		if part < 0 || total > maxInt-part {
			return 0, false
		}
		total += part
	}
	return total, true
}

func newValidationError(code ValidationCode, field string, limit int) *ValidationError {
	return &ValidationError{Code: code, Field: field, Limit: limit}
}
