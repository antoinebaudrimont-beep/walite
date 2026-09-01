package model

import "errors"

// MaxBootstrapMessagesPerChat is the maximum supported message history copied
// from one transport bootstrap conversation into walite-owned memory.
const MaxBootstrapMessagesPerChat = 50

// MaxChatSummaries is the fixed application/TUI chat-list bound.
const MaxChatSummaries = 10_000

// BootstrapCategory is the transport-neutral classification of an upstream
// conversation-history delivery.
type BootstrapCategory uint8

const (
	BootstrapInitial BootstrapCategory = iota + 1
	BootstrapRecent
	BootstrapFull
	BootstrapOnDemand
)

// BootstrapRecord is one bounded conversation import, or an end-of-delivery
// marker. It never retains an upstream protocol object.
type BootstrapRecord struct {
	chat                Chat
	messages            []Message
	category            BootstrapCategory
	complete            bool
	unreadAuthoritative bool
}

// NewBootstrapRecord validates and takes bounded ownership of one conversation.
func NewBootstrapRecord(category BootstrapCategory, chat Chat, messages []Message) (BootstrapRecord, error) {
	if !validBootstrapCategory(category) || chat.ID().String() == "" || len(messages) > MaxBootstrapMessagesPerChat {
		return BootstrapRecord{}, errors.New("bootstrap record rejected")
	}
	owned := make([]Message, len(messages))
	for index, message := range messages {
		if message.ChatID() != chat.ID() {
			return BootstrapRecord{}, errors.New("bootstrap record rejected")
		}
		owned[index] = message
	}
	return BootstrapRecord{chat: chat, messages: owned, category: category, unreadAuthoritative: true}, nil
}

// NewBootstrapComplete creates an end marker after all preceding records in
// one upstream delivery have been admitted.
func NewBootstrapComplete(category BootstrapCategory) (BootstrapRecord, error) {
	if !validBootstrapCategory(category) {
		return BootstrapRecord{}, errors.New("bootstrap record rejected")
	}
	return BootstrapRecord{category: category, complete: true}, nil
}

func validBootstrapCategory(category BootstrapCategory) bool {
	return category >= BootstrapInitial && category <= BootstrapOnDemand
}

func (record BootstrapRecord) Category() BootstrapCategory { return record.category }
func (record BootstrapRecord) Complete() bool              { return record.complete }
func (record BootstrapRecord) Chat() Chat                  { return record.chat }
func (record BootstrapRecord) Len() int                    { return len(record.messages) }
func (record BootstrapRecord) UnreadAuthoritative() bool   { return record.unreadAuthoritative }
func (record BootstrapRecord) WithUnreadAuthoritative(authoritative bool) BootstrapRecord {
	record.unreadAuthoritative = authoritative
	return record
}
func (record BootstrapRecord) At(index int) (Message, bool) {
	if index < 0 || index >= len(record.messages) {
		return Message{}, false
	}
	return record.messages[index], true
}
