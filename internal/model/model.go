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
}

// Message is an immutable normalized message value.
type Message struct {
	chatID        ChatID
	messageID     MessageID
	sentAt        time.Time
	fromMe        bool
	text          string
	bodyTruncated bool
	bodyRetained  bool
	byteSize      int
}

// ChatID returns the message's chat identifier.
func (message Message) ChatID() ChatID {
	return message.chatID
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
