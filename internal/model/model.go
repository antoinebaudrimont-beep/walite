package model

import "time"

// ChatID is a validated, immutable chat identifier.
type ChatID struct {
	value string
}

// String returns the identifier exactly as supplied to NewChatID.
func (id ChatID) String() string {
	return id.value
}

// ContactID is a validated, immutable opaque contact identifier.
type ContactID struct {
	value string
}

// String returns the identifier exactly as supplied to NewContactID.
func (id ContactID) String() string {
	return id.value
}

// MessageID is a validated, immutable message identifier.
type MessageID struct {
	value string
}

// String returns the identifier exactly as supplied to NewMessageID.
func (id MessageID) String() string {
	return id.value
}

// MessageInput is the unvalidated input accepted by NewMessage.
type MessageInput struct {
	ChatID, MessageID string
	SentAt            time.Time
	FromMe            bool
	Text              string
	Quote             TextQuote
	SenderID          string
	IsGroup           bool
	// BodyTruncated preserves an existing normalization marker when restoring
	// retained text from a store; it cannot disable truncation of new input.
	BodyTruncated bool
}

// Message is an immutable normalized message value.
type Message struct {
	chatID        ChatID
	messageID     MessageID
	sentAt        time.Time
	fromMe        bool
	text          string
	quote         TextQuote
	senderID      ContactID
	isGroup       bool
	bodyTruncated bool
	bodyRetained  bool
	byteSize      int
}

// ChatID returns the message's chat identifier.
func (message Message) ChatID() ChatID {
	return message.chatID
}

// WithChatID returns a validated immutable copy addressed to an opaque chat
// identity established by the caller. Message identity is still chat-scoped;
// no alias or transport-specific semantics live in model.
func (message Message) WithChatID(id ChatID) (Message, error) {
	ownedID, err := NewChatID(id.String())
	if err != nil {
		return Message{}, err
	}
	if _, err := validateMessage(message); err != nil {
		return Message{}, err
	}
	message.chatID = ownedID
	message.byteSize, _ = messageByteSize(id.String(), message.messageID.String(), message.text, message.quote, message.senderID)
	return message, nil
}

// MessageID returns the message's identifier.
func (message Message) MessageID() MessageID {
	return message.messageID
}

// SentAt returns the message timestamp.
func (message Message) SentAt() time.Time {
	return message.sentAt
}

// FromMe reports whether the message was sent by the local user.
func (message Message) FromMe() bool {
	return message.fromMe
}

// Text returns the normalized retained text.
func (message Message) Text() string {
	return message.text
}

// Quote returns the bounded same-chat reply reference, or zero for plain text.
func (message Message) Quote() TextQuote { return message.quote }

func (message Message) SenderID() ContactID { return message.senderID }
func (message Message) IsGroup() bool       { return message.isGroup }

func (message Message) WithSender(sender ContactID, group bool) Message {
	message.senderID, message.isGroup = sender, group
	message.byteSize, _ = messageByteSize(message.chatID.value, message.messageID.value, message.text, message.quote, sender)
	return message
}

// WithQuote returns an immutable copy with a constructor-owned quote. Like
// WithoutBody, it leaves identity, timestamps and direction unchanged. Event
// and batch constructors still validate and charge the complete message.
func (message Message) WithQuote(quote TextQuote) Message {
	message.quote = quote
	message.byteSize, _ = messageByteSize(message.chatID.value, message.messageID.value, message.text, quote, message.senderID)
	return message
}

// BodyTruncated reports whether normalization omitted input text.
func (message Message) BodyTruncated() bool {
	return message.bodyTruncated
}

// BodyRetained reports whether the message body is retained.
func (message Message) BodyRetained() bool {
	return message.bodyRetained
}

// ByteSize returns the exact variable-byte charge for the normalized message.
func (message Message) ByteSize() int {
	return message.byteSize
}

// Event is an immutable normalized real-time event.
type Event struct {
	message    Message
	receivedAt time.Time
	byteSize   int
}

// Message returns the normalized message carried by the event.
func (event Event) Message() Message {
	return event.message
}

// ReceivedAt returns the event receipt timestamp.
func (event Event) ReceivedAt() time.Time {
	return event.receivedAt
}

// ByteSize returns the event's conservative normalized byte charge.
func (event Event) ByteSize() int {
	return event.byteSize
}
