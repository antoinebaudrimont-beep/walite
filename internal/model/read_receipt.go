package model

import (
	"errors"
	"time"
)

// MaxReadReceiptMessages matches the bounded selected-chat message working set.
// Receipt construction never loads older history solely for acknowledgement.
const MaxReadReceiptMessages = 32

// ReadReceiptMessageInput is one received message in a bounded read frontier.
type ReadReceiptMessageInput struct {
	MessageID string
	SentAt    time.Time
	SenderID  string
}

// ReadReceiptMessage is one immutable transport-neutral receipt identity.
type ReadReceiptMessage struct {
	messageID MessageID
	sentAt    time.Time
	senderID  ContactID
}

func (message ReadReceiptMessage) MessageID() MessageID { return message.messageID }
func (message ReadReceiptMessage) SentAt() time.Time    { return message.sentAt }
func (message ReadReceiptMessage) SenderID() ContactID  { return message.senderID }

// ReadReceiptRequest contains only the bounded, currently known incoming
// frontier for one chat. It deliberately carries no protocol JIDs or payloads.
type ReadReceiptRequest struct {
	chatID   ChatID
	group    bool
	messages [MaxReadReceiptMessages]ReadReceiptMessage
	count    int
}

// NewReadReceiptRequest validates and copies a bounded read frontier.
func NewReadReceiptRequest(chatID string, group bool, inputs []ReadReceiptMessageInput) (ReadReceiptRequest, error) {
	if len(inputs) == 0 || len(inputs) > MaxReadReceiptMessages {
		return ReadReceiptRequest{}, errors.New("read receipt request rejected")
	}
	id, err := NewChatID(chatID)
	if err != nil {
		return ReadReceiptRequest{}, err
	}
	request := ReadReceiptRequest{chatID: id, group: group, count: len(inputs)}
	for index, input := range inputs {
		messageID, err := NewMessageID(input.MessageID)
		if err != nil || input.SentAt.IsZero() {
			return ReadReceiptRequest{}, errors.New("read receipt request rejected")
		}
		for previous := 0; previous < index; previous++ {
			if request.messages[previous].messageID == messageID {
				return ReadReceiptRequest{}, errors.New("read receipt request rejected")
			}
		}
		var senderID ContactID
		if input.SenderID != "" {
			senderID, err = NewContactID(input.SenderID)
			if err != nil {
				return ReadReceiptRequest{}, err
			}
		}
		if group && senderID.String() == "" {
			return ReadReceiptRequest{}, errors.New("read receipt request rejected")
		}
		request.messages[index] = ReadReceiptMessage{messageID: messageID, sentAt: input.SentAt, senderID: senderID}
	}
	return request, nil
}

func (request ReadReceiptRequest) ChatID() ChatID { return request.chatID }
func (request ReadReceiptRequest) IsGroup() bool  { return request.group }
func (request ReadReceiptRequest) Len() int       { return request.count }

func (request ReadReceiptRequest) At(index int) (ReadReceiptMessage, bool) {
	if index < 0 || index >= request.count {
		return ReadReceiptMessage{}, false
	}
	return request.messages[index], true
}

// NewestSentAt identifies the newest/highest frontier used for coalescing.
func (request ReadReceiptRequest) NewestSentAt() time.Time {
	var newest time.Time
	for index := 0; index < request.count; index++ {
		if request.messages[index].sentAt.After(newest) {
			newest = request.messages[index].sentAt
		}
	}
	return newest
}
