package tui

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"
)

// Chat summaries and message working memory have independent fixed bounds.
// Metadata-only chats do not allocate message slots.
const (
	ChatWorkingSetCapacity    = 10_000
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
	ID             string
	SentAt         time.Time
	FromMe         bool
	Text           string
	BodyRetained   bool
	MediaKind      string
	MediaName      string
	MediaMIME      string
	ReplyToID      string
	ReplyToText    string
	ReplyToFromMe  bool
	ReplyMediaKind string
	ReplyMediaName string
	ReplyMediaMIME string
	SenderID       string
	IsGroup        bool
}

// LiveMessage is an immutable-by-convention committed message presentation
// event. It deliberately contains no service or storage types.
type LiveMessage struct {
	ChatID         string
	MessageID      string
	SentAt         time.Time
	FromMe         bool
	Text           string
	BodyRetained   bool
	MediaKind      string
	MediaName      string
	MediaMIME      string
	ReplyToID      string
	ReplyToText    string
	ReplyToFromMe  bool
	ReplyMediaKind string
	ReplyMediaName string
	ReplyMediaMIME string
	UnreadCount    uint32
	ActivityTime   time.Time
	SenderID       string
	IsGroup        bool
}

// SendRequest is an immutable-by-convention outgoing presentation request.
// Stable IDs, never presentation indexes, cross the application boundary.
type SendRequest struct {
	ChatID         string
	Text           string
	ReplyToID      string
	ReplyToText    string
	ReplyToFromMe  bool
	ReplyMediaKind string
	ReplyMediaName string
	ReplyMediaMIME string
}

// Input supplies configuration and application-owned initial/live data to Run.
type Input struct {
	Options      Options
	InitialState InitialState
	LiveEvents   <-chan LiveMessage
	Send         func(context.Context, SendRequest) error
	// When non-nil, Send performs bounded admission only; completion arrives
	// here and the draft remains protected until that result is processed.
	SendResults      <-chan SendResult
	DisplayUpdates   <-chan DisplayMetadata
	SummaryUpdates   <-chan InitialState
	ChatLoads        <-chan ChatLoadResult
	LoadChat         func(ChatLoadRequest) bool
	PersistLocalRead func(LocalReadRequest) bool
	SendReadReceipt  func(ReadReceiptRequest) bool
	Media            func(MediaRequest) bool
	MediaResults     <-chan MediaResult
	CloseMedia       func()
	// CloseExternalPreview admits asynchronous termination and reports whether
	// an active viewer consumed Escape.
	CloseExternalPreview func() bool
	// SaveOptions admits one explicit save without performing I/O. OptionsResults
	// completes it; options become active only after successful persistence.
	SaveOptions    func(Options) bool
	OptionsResults <-chan error
}

type MediaAction uint8

const (
	MediaPreview MediaAction = iota + 1
	MediaSave
)

// MediaRequest identifies one explicit, currently visible user selection.
// Stable IDs cross the boundary; paths and download credentials never do.
type MediaRequest struct {
	Action              MediaAction
	ChatID, MessageID   string
	Kind                string
	X, Y, Width, Height int
}

// MediaResult completes one bounded asynchronous request.
type MediaResult struct {
	Action            MediaAction
	ChatID, MessageID string
	Status            string
	Previewing        bool
}

// LocalReadRequest clears cached unread state only through the activity that
// was visible when the user opened the chat.
type LocalReadRequest struct {
	ChatID       string
	ActivityTime time.Time
}

// ReadReceiptMessage contains only the stable identity needed to acknowledge
// one known incoming message. Bodies never cross this side-effect boundary.
type ReadReceiptMessage struct {
	MessageID string
	SentAt    time.Time
	SenderID  string
}

// ReadReceiptRequest is the bounded transport-neutral frontier captured by an
// explicit user chat selection. Messages is always limited to the chat working
// set and contains no FromMe entries.
type ReadReceiptRequest struct {
	ChatID   string
	IsGroup  bool
	Messages []ReadReceiptMessage
}

// ChatLoadRequest identifies one selected-chat page and the presentation
// revision that it is allowed to replace.
type ChatLoadRequest struct {
	ChatID   string
	Revision uint64
}

// ChatLoadResult is one bounded oldest-first cached page.
type ChatLoadResult struct {
	ChatID   string
	Revision uint64
	Messages []InitialMessage
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
		if chat.messageCount > 0 {
			chat.messages = new([maxMessages]messageView)
		}
		seenMessages := make(map[string]struct{}, len(sourceChat.Messages))
		for messageIndex, sourceMessage := range sourceChat.Messages {
			if len(sourceMessage.SenderID) > 512 || !utf8.ValidString(sourceMessage.SenderID) ||
				!utf8.ValidString(sourceMessage.MediaKind) || !utf8.ValidString(sourceMessage.MediaName) || !validMediaMIME(sourceMessage.MediaMIME) ||
				!validMediaPresentation(sourceMessage.MediaKind, sourceMessage.MediaName) {
				return nil, errors.New("tui initial state rejected")
			}
			if !validReplyMetadata(sourceMessage.ReplyToID, sourceMessage.ReplyToText, sourceMessage.ReplyToFromMe, sourceMessage.ReplyMediaKind, sourceMessage.ReplyMediaName, sourceMessage.ReplyMediaMIME) {
				return nil, errors.New("tui initial state rejected")
			}
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
				id:             messageID(sourceMessage.ID),
				sentAt:         sourceMessage.SentAt,
				time:           localMessageTime(sourceMessage.SentAt),
				text:           text,
				mediaKind:      sourceMessage.MediaKind,
				mediaName:      sourceMessage.MediaName,
				mediaMIME:      sourceMessage.MediaMIME,
				fromMe:         sourceMessage.FromMe,
				bodyRetained:   sourceMessage.BodyRetained,
				replyText:      sourceMessage.ReplyToText,
				replyFromMe:    sourceMessage.ReplyToFromMe,
				replyMediaKind: sourceMessage.ReplyMediaKind,
				replyMediaName: sourceMessage.ReplyMediaName,
				replyMediaMIME: sourceMessage.ReplyMediaMIME,
				replyToID:      messageID(sourceMessage.ReplyToID),
				hasReply:       sourceMessage.ReplyToID != "",
				senderID:       sourceMessage.SenderID, isGroup: sourceMessage.IsGroup,
			}
		}
	}
	return state, nil
}
