package model

import (
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// MaxChatDisplayNameBytes is the hard byte limit for a normalized chat name.
	MaxChatDisplayNameBytes = 1024
	// MaxContactDisplayNameBytes is the hard byte limit for a normalized contact name.
	MaxContactDisplayNameBytes = 1024
	// MaxWriteBatchMessages is the hard message limit for one store write batch.
	MaxWriteBatchMessages = 50

	writeBatchRecordBytes = 32
	maxWriteBatchBytes    = MaxWriteBatchMessages *
		(4*MaxIdentifierBytes + MaxRetainedTextBytes + MaxQuoteTextBytes + MaxMediaNameBytes + MaxMediaMIMEBytes + writeBatchRecordBytes)
)

// ContactInput is the unvalidated input accepted by NewContact.
type ContactInput struct {
	ID          string
	DisplayName string
	UpdatedAt   time.Time
	IngestSeq   uint64
}

// Contact is immutable bounded contact metadata. Its identifier is opaque and
// carries no phone-number or transport-specific semantics.
type Contact struct {
	id            ContactID
	displayName   string
	nameTruncated bool
	updatedAt     time.Time
	ingestSeq     uint64
}

// NewContact validates the contact identity and takes bounded ownership of its
// normalized display name.
func NewContact(input ContactInput) (Contact, error) {
	id, err := NewContactID(input.ID)
	if err != nil {
		return Contact{}, err
	}
	if err := validateIngestSeq(input.IngestSeq); err != nil {
		return Contact{}, err
	}
	displayName, truncated := normalizeBoundedUTF8(
		input.DisplayName,
		MaxContactDisplayNameBytes,
	)
	return Contact{
		id:            id,
		displayName:   displayName,
		nameTruncated: truncated,
		updatedAt:     input.UpdatedAt,
		ingestSeq:     input.IngestSeq,
	}, nil
}

// ID returns the contact identifier.
func (contact Contact) ID() ContactID {
	return contact.id
}

// DisplayName returns the normalized bounded display name.
func (contact Contact) DisplayName() string {
	return contact.displayName
}

// NameTruncated reports whether normalization omitted source bytes.
func (contact Contact) NameTruncated() bool {
	return contact.nameTruncated
}

// UpdatedAt returns the source metadata timestamp, or zero when unavailable.
func (contact Contact) UpdatedAt() time.Time {
	return contact.updatedAt
}

// IngestSeq returns the application-local metadata ordering sequence.
func (contact Contact) IngestSeq() uint64 {
	return contact.ingestSeq
}

// ChatInput is the unvalidated input accepted by NewChat.
type ChatInput struct {
	ID            string
	ContactID     string
	DisplayName   string
	IsGroup       bool
	LastMessageAt time.Time
	UnreadCount   uint32
	Muted         bool
	Archived      bool
	Placeholder   bool
	UpdatedAt     time.Time
	IngestSeq     uint64
}

// Chat is immutable bounded chat metadata.
type Chat struct {
	id            ChatID
	contactID     ContactID
	hasContact    bool
	displayName   string
	nameTruncated bool
	isGroup       bool
	lastMessageAt time.Time
	unreadCount   uint32
	muted         bool
	archived      bool
	placeholder   bool
	updatedAt     time.Time
	ingestSeq     uint64
}

// NewChat validates the chat identity and takes bounded ownership of its
// normalized display name.
func NewChat(input ChatInput) (Chat, error) {
	id, err := NewChatID(input.ID)
	if err != nil {
		return Chat{}, err
	}
	var contactID ContactID
	if input.ContactID != "" {
		contactID, err = NewContactID(input.ContactID)
		if err != nil {
			return Chat{}, err
		}
	}
	if err := validateIngestSeq(input.IngestSeq); err != nil {
		return Chat{}, err
	}
	displayName, truncated := normalizeBoundedUTF8(
		input.DisplayName,
		MaxChatDisplayNameBytes,
	)
	return Chat{
		id:            id,
		contactID:     contactID,
		hasContact:    input.ContactID != "",
		displayName:   displayName,
		nameTruncated: truncated,
		isGroup:       input.IsGroup,
		lastMessageAt: input.LastMessageAt,
		unreadCount:   input.UnreadCount,
		muted:         input.Muted,
		archived:      input.Archived,
		placeholder:   input.Placeholder,
		updatedAt:     input.UpdatedAt,
		ingestSeq:     input.IngestSeq,
	}, nil
}

// ID returns the chat identifier.
func (chat Chat) ID() ChatID {
	return chat.id
}

// ContactID returns the linked contact identifier. Call HasContact before
// interpreting its zero value.
func (chat Chat) ContactID() ContactID {
	return chat.contactID
}

// HasContact reports whether the chat links to a contact.
func (chat Chat) HasContact() bool {
	return chat.hasContact
}

// DisplayName returns the normalized bounded display name.
func (chat Chat) DisplayName() string {
	return chat.displayName
}

// NameTruncated reports whether normalization omitted source bytes.
func (chat Chat) NameTruncated() bool {
	return chat.nameTruncated
}

// IsGroup reports whether this is a group conversation.
func (chat Chat) IsGroup() bool {
	return chat.isGroup
}

// LastMessageAt returns the newest known message time, or zero when unknown.
func (chat Chat) LastMessageAt() time.Time {
	return chat.lastMessageAt
}

// UnreadCount returns the cache's bounded unread count.
func (chat Chat) UnreadCount() uint32 {
	return chat.unreadCount
}

// Muted reports whether the chat is muted.
func (chat Chat) Muted() bool {
	return chat.muted
}

// Archived reports whether the chat is archived.
func (chat Chat) Archived() bool {
	return chat.archived
}

// Placeholder reports whether this chat was created only to satisfy message
// identity and ordering requirements.
func (chat Chat) Placeholder() bool {
	return chat.placeholder
}

// UpdatedAt returns the source metadata timestamp, or zero when unavailable.
func (chat Chat) UpdatedAt() time.Time {
	return chat.updatedAt
}

// IngestSeq returns the application-local metadata ordering sequence.
func (chat Chat) IngestSeq() uint64 {
	return chat.ingestSeq
}

func validateIngestSeq(sequence uint64) *ValidationError {
	if sequence > ^uint64(0)>>1 {
		return newValidationError(InvalidValue, "ingest_seq", 0)
	}
	return nil
}

// WithoutBody returns a copy with its text and quoted excerpt/reference removed.
// Identity, timestamp, direction, and the source truncation marker are preserved.
func (message Message) WithoutBody() Message {
	withoutBody := message
	withoutBody.text = ""
	withoutBody.quote = TextQuote{}
	withoutBody.bodyRetained = false
	byteSize, ok := messageByteSize(
		withoutBody.chatID.value,
		withoutBody.messageID.value,
		withoutBody.text,
		withoutBody.quote,
		withoutBody.media,
		withoutBody.senderID,
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
	quote, err := cloneTextQuote(message.quote)
	if err != nil {
		return Message{}, 0, err
	}
	media, err := cloneMedia(message.media)
	if err != nil {
		return Message{}, 0, err
	}
	return Message{
		chatID:        chatID,
		messageID:     messageID,
		sentAt:        message.sentAt,
		fromMe:        message.fromMe,
		text:          strings.Clone(message.text),
		quote:         quote,
		media:         media,
		senderID:      ContactID{value: strings.Clone(message.senderID.value)},
		isGroup:       message.isGroup,
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
