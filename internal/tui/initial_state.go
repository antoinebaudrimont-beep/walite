package tui

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"
)

// The initial snapshot is deliberately bounded to the TUI's fixed in-memory
// working set. The chat bound is a small presentation capacity, not the
// application's global retained-chat limit. Loading older pages remains a
// later increment.
const (
	ChatWorkingSetCapacity    = 16
	MaxInitialMessagesPerChat = 32
)

// InitialState is an immutable-by-convention startup snapshot. Run takes an
// owned copy before screen initialization and never retains these slices.
type InitialState struct {
	Chats []InitialChat
}

// InitialChat contains only the application data needed to render a chat.
type InitialChat struct {
	ID           string
	Title        string
	IsGroup      bool
	UnreadCount  uint32
	ActivityTime time.Time
	Messages     []InitialMessage
}

// InitialMessage is ordered oldest-first within InitialChat.Messages.
type InitialMessage struct {
	ID           string
	SentAt       time.Time
	FromMe       bool
	Text         string
	BodyRetained bool
}

// LiveMessage is an immutable-by-convention committed message presentation
// event. It deliberately contains no service or storage types.
type LiveMessage struct {
	ChatID       string
	MessageID    string
	SentAt       time.Time
	FromMe       bool
	Text         string
	BodyRetained bool
	UnreadCount  uint32
	ActivityTime time.Time
}

// SendRequest is an immutable-by-convention outgoing presentation request.
// Stable IDs, never presentation indexes, cross the application boundary.
type SendRequest struct {
	ChatID    string
	Text      string
	ReplyToID string
}

// Input supplies configuration and application-owned initial/live data to Run.
type Input struct {
	Options      Options
	InitialState InitialState
	LiveEvents   <-chan LiveMessage
	Send         func(context.Context, SendRequest) error
}

func chatStateFromInitial(initial InitialState) (*chatState, error) {
	if len(initial.Chats) > ChatWorkingSetCapacity {
		return nil, errors.New("tui initial state rejected")
	}
	state := &chatState{chatCount: len(initial.Chats)}
	seenChats := make(map[string]struct{}, len(initial.Chats))
	for chatIndex, sourceChat := range initial.Chats {
		if sourceChat.ID == "" || !utf8.ValidString(sourceChat.ID) || !utf8.ValidString(sourceChat.Title) || len(sourceChat.Messages) > MaxInitialMessagesPerChat {
			return nil, errors.New("tui initial state rejected")
		}
		if _, exists := seenChats[sourceChat.ID]; exists {
			return nil, errors.New("tui initial state rejected")
		}
		seenChats[sourceChat.ID] = struct{}{}
		chat := &state.chats[chatIndex]
		chat.id = sourceChat.ID
		chat.title = sourceChat.Title
		chat.isGroup = sourceChat.IsGroup
		chat.unreadCount = sourceChat.UnreadCount
		chat.activityTime = sourceChat.ActivityTime
		chat.messageCount = len(sourceChat.Messages)
		seenMessages := make(map[string]struct{}, len(sourceChat.Messages))
		for messageIndex, sourceMessage := range sourceChat.Messages {
			if sourceMessage.ID == "" || !utf8.ValidString(sourceMessage.ID) || !utf8.ValidString(sourceMessage.Text) {
				return nil, errors.New("tui initial state rejected")
			}
			if _, exists := seenMessages[sourceMessage.ID]; exists {
				return nil, errors.New("tui initial state rejected")
			}
			seenMessages[sourceMessage.ID] = struct{}{}
			text := sourceMessage.Text
			if !sourceMessage.BodyRetained {
				text = ""
			}
			chat.messages[messageIndex] = messageView{
				id:           messageID(sourceMessage.ID),
				sentAt:       sourceMessage.SentAt,
				time:         sourceMessage.SentAt.Format("15:04"),
				text:         text,
				fromMe:       sourceMessage.FromMe,
				bodyRetained: sourceMessage.BodyRetained,
			}
		}
	}
	return state, nil
}
