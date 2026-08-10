package model

import (
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// MaxChatDisplayNameBytes is the hard byte limit for a normalized chat name.
	MaxChatDisplayNameBytes = 1024
	// MaxWriteBatchMessages is the hard message limit for one store write batch.
	MaxWriteBatchMessages = 50

	writeBatchRecordBytes = 32
	maxWriteBatchBytes    = MaxWriteBatchMessages *
		(2*MaxIdentifierBytes + MaxRetainedTextBytes + writeBatchRecordBytes)
)

// ChatInput is the unvalidated input accepted by NewChat.
type ChatInput struct {
	ID          string
	DisplayName string
	Placeholder bool
}

// Chat is immutable bounded chat metadata.
type Chat struct {
	id            ChatID
	displayName   string
	nameTruncated bool
	placeholder   bool
}

// NewChat validates the chat identity and takes bounded ownership of its
// normalized display name.
func NewChat(input ChatInput) (Chat, error) {
	id, err := NewChatID(input.ID)
	if err != nil {
		return Chat{}, err
	}
	displayName, truncated := normalizeBoundedUTF8(
		input.DisplayName,
		MaxChatDisplayNameBytes,
	)
	return Chat{
		id:            id,
		displayName:   displayName,
		nameTruncated: truncated,
		placeholder:   input.Placeholder,
	}, nil
}

// ID returns the chat identifier.
func (chat Chat) ID() ChatID {
	return chat.id
}

// DisplayName returns the normalized bounded display name.
func (chat Chat) DisplayName() string {
	return chat.displayName
}

// NameTruncated reports whether normalization omitted source bytes.
func (chat Chat) NameTruncated() bool {
	return chat.nameTruncated
}

// Placeholder reports whether this chat was created only to satisfy message
// identity and ordering requirements.
func (chat Chat) Placeholder() bool {
	return chat.placeholder
}

// WithoutBody returns a copy of the message with its text removed. Identity,
// timestamp, direction, and the source truncation marker are preserved.
func (message Message) WithoutBody() Message {
	withoutBody := message
	withoutBody.text = ""
	withoutBody.bodyRetained = false
	byteSize, ok := messageByteSize(
		withoutBody.chatID.value,
		withoutBody.messageID.value,
		withoutBody.text,
	)
	if !ok {
		// Valid Messages cannot reach this branch because both identifiers are
		// individually bounded. Keep a malformed zero value conservatively
		// uncharged rather than manufacturing an overflowing size.
		byteSize = 0
	}
	withoutBody.byteSize = byteSize
	return withoutBody
}

// Cursor is an exclusive deterministic (sent_at, message_id) page boundary.
// Its zero value is the explicit no-boundary sentinel returned by NoCursor.
type Cursor struct {
	sentAt    time.Time
	messageID MessageID
}

// NewCursor validates and takes ownership of a complete paging boundary.
func NewCursor(sentAt time.Time, messageID MessageID) (Cursor, error) {
	if sentAt.IsZero() {
		return Cursor{}, newValidationError(Required, "sent_at", 0)
	}
	ownedID, err := NewMessageID(messageID.String())
	if err != nil {
		return Cursor{}, err
	}
	return Cursor{sentAt: sentAt, messageID: ownedID}, nil
}

// NoCursor returns the only valid no-boundary cursor.
func NoCursor() Cursor {
	return Cursor{}
}

// IsZero reports whether this is the explicit no-boundary cursor.
func (cursor Cursor) IsZero() bool {
	return cursor.sentAt.IsZero() && cursor.messageID.String() == ""
}

// SentAt returns the cursor timestamp.
func (cursor Cursor) SentAt() time.Time {
	return cursor.sentAt
}

// MessageID returns the cursor message identifier.
func (cursor Cursor) MessageID() MessageID {
	return cursor.messageID
}

// MessageAnchor is immutable body-free paging metadata for one chat.
type MessageAnchor struct {
	chatID    ChatID
	messageID MessageID
	sentAt    time.Time
	fromMe    bool
}

// NewMessageAnchor validates and takes ownership of body-free paging metadata.
func NewMessageAnchor(
	chatID ChatID,
	messageID MessageID,
	sentAt time.Time,
	fromMe bool,
) (MessageAnchor, error) {
	ownedChatID, err := NewChatID(chatID.String())
	if err != nil {
		return MessageAnchor{}, err
	}
	ownedMessageID, err := NewMessageID(messageID.String())
	if err != nil {
		return MessageAnchor{}, err
	}
	if sentAt.IsZero() {
		return MessageAnchor{}, newValidationError(Required, "sent_at", 0)
	}
	return MessageAnchor{
		chatID:    ownedChatID,
		messageID: ownedMessageID,
		sentAt:    sentAt,
		fromMe:    fromMe,
	}, nil
}

// ChatID returns the anchor chat identifier.
func (anchor MessageAnchor) ChatID() ChatID {
	return anchor.chatID
}

// MessageID returns the anchor message identifier.
func (anchor MessageAnchor) MessageID() MessageID {
	return anchor.messageID
}

// SentAt returns the anchor timestamp.
func (anchor MessageAnchor) SentAt() time.Time {
	return anchor.sentAt
}

// FromMe returns the anchor direction.
func (anchor MessageAnchor) FromMe() bool {
	return anchor.fromMe
}

// WriteOrigin is a closed application write-lane classification.
type WriteOrigin uint8

const (
	WriteRealtime WriteOrigin = iota + 1
	WriteHistory
)

// WriteBatch is an immutable bounded collection passed to a store operation.
type WriteBatch struct {
	origin   WriteOrigin
	messages []Message
	byteSize int
}

// NewWriteBatch validates, charges, and defensively copies at most 50
// messages. Its conservative charge includes 32 fixed bytes per record.
func NewWriteBatch(origin WriteOrigin, messages []Message) (WriteBatch, error) {
	if err := validateWriteOrigin(origin); err != nil {
		return WriteBatch{}, err
	}
	if len(messages) > MaxWriteBatchMessages {
		return WriteBatch{}, newValidationError(
			SizeExceeded,
			"write_batch_messages",
			MaxWriteBatchMessages,
		)
	}
	if len(messages) == 0 {
		return WriteBatch{origin: origin}, nil
	}

	var messageBytes [MaxWriteBatchMessages]int
	owned := make([]Message, len(messages))
	for index, message := range messages {
		normalized, byteSize, err := cloneNormalizedMessage(message)
		if err != nil {
			return WriteBatch{}, err
		}
		owned[index] = normalized
		messageBytes[index] = byteSize
	}
	byteSize, err := writeBatchByteSize(messageBytes[:len(messages)])
	if err != nil {
		return WriteBatch{}, err
	}
	return WriteBatch{origin: origin, messages: owned, byteSize: byteSize}, nil
}

// Origin returns the batch write lane.
func (batch WriteBatch) Origin() WriteOrigin {
	return batch.origin
}

// Len returns the batch message count.
func (batch WriteBatch) Len() int {
	return len(batch.messages)
}

// At returns the message at index and whether index was in bounds.
func (batch WriteBatch) At(index int) (Message, bool) {
	if index < 0 || index >= len(batch.messages) {
		return Message{}, false
	}
	return batch.messages[index], true
}

// ByteSize returns the conservative bounded batch charge.
func (batch WriteBatch) ByteSize() int {
	return batch.byteSize
}

// MetadataLimitKind is a closed, content-free metadata limit classification.
type MetadataLimitKind uint8

const (
	MetadataChatLimit MetadataLimitKind = iota + 1
)

func validateMetadataLimitKind(kind MetadataLimitKind) *ValidationError {
	if kind == 0 {
		return newValidationError(Required, "metadata_limit_kind", 0)
	}
	if kind != MetadataChatLimit {
		return newValidationError(InvalidValue, "metadata_limit_kind", 0)
	}
	return nil
}

func validateWriteOrigin(origin WriteOrigin) *ValidationError {
	if origin == 0 {
		return newValidationError(Required, "write_origin", 0)
	}
	switch origin {
	case WriteRealtime, WriteHistory:
		return nil
	default:
		return newValidationError(InvalidValue, "write_origin", 0)
	}
}

func cloneNormalizedMessage(message Message) (Message, int, error) {
	byteSize, err := validateMessage(message)
	if err != nil {
		return Message{}, 0, err
	}
	chatID, err := NewChatID(message.ChatID().String())
	if err != nil {
		return Message{}, 0, err
	}
	messageID, err := NewMessageID(message.MessageID().String())
	if err != nil {
		return Message{}, 0, err
	}
	return Message{
		chatID:        chatID,
		messageID:     messageID,
		sentAt:        message.sentAt,
		fromMe:        message.fromMe,
		text:          strings.Clone(message.text),
		bodyTruncated: message.bodyTruncated,
		bodyRetained:  message.bodyRetained,
		byteSize:      byteSize,
	}, byteSize, nil
}

func writeBatchByteSize(messageBytes []int) (int, error) {
	total := 0
	for _, byteSize := range messageBytes {
		next, ok := checkedByteSum(total, byteSize, writeBatchRecordBytes)
		if !ok || next > maxWriteBatchBytes {
			return 0, newValidationError(
				SizeExceeded,
				"write_batch",
				maxWriteBatchBytes,
			)
		}
		total = next
	}
	return total, nil
}

func normalizeBoundedUTF8(value string, maxBytes int) (string, bool) {
	if value == "" {
		return "", false
	}
	if maxBytes <= 0 {
		return "", true
	}

	prefixBytes := len(value)
	if prefixBytes > maxBytes {
		prefixBytes = maxBytes
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
		if normalized.Len() > maxBytes-outputBytes {
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
