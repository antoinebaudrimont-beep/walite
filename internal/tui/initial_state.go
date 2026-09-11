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
	Reactions      []ReactionGroup
}

// ReactionGroup is one bounded presentation aggregate. Count includes the
// linked account when Own is true.
type ReactionGroup struct {
	Emoji string
	Count uint16
	Own   bool
}

// ReactionUpdate replaces the complete visible aggregate for one message.
type ReactionUpdate struct {
	ChatID, TargetMessageID string
	Groups                  []ReactionGroup
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
	ChatID                 string
	Text                   string
	FilePath               string
	ReplyToID              string
	ReplyToText            string
	ReplyToFromMe          bool
	ReplyMediaKind         string
	ReplyMediaName         string
	ReplyMediaMIME         string
	ReactionTargetID       string
	ReactionTargetSenderID string
	ReactionTargetFromMe   bool
	ReactionIsGroup        bool
	ReactionEmoji          string
}

// Input supplies configuration and application-owned initial/live data to Run.
type Input struct {
	Options         Options
	InitialState    InitialState
	LiveEvents      <-chan LiveMessage
	ReactionUpdates <-chan ReactionUpdate
	Send            func(context.Context, SendRequest) error
	// When non-nil, Send performs bounded admission only; completion arrives
	// here and the draft remains protected until that result is processed.
	SendResults      <-chan SendResult
	DisplayUpdates   <-chan DisplayMetadata
	SummaryUpdates   <-chan InitialState
	ChatLoads        <-chan ChatLoadResult
	LoadChat         func(ChatLoadRequest) bool
	OlderHistory     func(OlderHistoryRequest) bool
	OlderResults     <-chan OlderHistoryResult
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
// explicit user selection, reply, or compose/send interaction. Messages is
// always limited to 32 known incoming entries and contains no FromMe entries.
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

// OlderHistoryPageSize is both the upstream request maximum and the bounded
// application result size. Repeated pages accumulate only for the selected
// chat, within SelectedChatHistoryCapacity.
const OlderHistoryPageSize = 50

// OlderHistoryRequest identifies one explicit page before the selected chat's
// stable oldest frontier. Revision is the transient selection epoch, not the
// chat's live-data revision.
type OlderHistoryRequest struct {
	ChatID          string
	Revision        uint64
	OldestMessageID string
	OldestSentAt    time.Time
	OldestFromMe    bool
	Count           int
}

type OlderHistoryResultKind uint8

const (
	OlderHistoryLoaded OlderHistoryResultKind = iota + 1
	OlderHistoryNoMore
	OlderHistoryUnavailable
	OlderHistoryFailed
)

// OlderHistoryResult carries at most one bounded page, oldest-first. Request
// is echoed verbatim so the TUI can reject stale chat/frontier completions.
type OlderHistoryResult struct {
	Request  OlderHistoryRequest
	Kind     OlderHistoryResultKind
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
			chat.messages = make([]messageView, maxMessages)
		}
		seenMessages := make(map[string]struct{}, len(sourceChat.Messages))
		for messageIndex, sourceMessage := range sourceChat.Messages {
			message, err := messageViewFromInitial(sourceMessage)
			if err != nil {
				return nil, errors.New("tui initial state rejected")
			}
			if _, exists := seenMessages[sourceMessage.ID]; exists {
				return nil, errors.New("tui initial state rejected")
			}
			seenMessages[sourceMessage.ID] = struct{}{}
			chat.messages[messageIndex] = message
		}
	}
	if state.chatCount > 0 {
		state.expandMessageBuffer(state.selected)
	}
	return state, nil
}

func messageViewFromInitial(source InitialMessage) (messageView, error) {
	if len(source.SenderID) > 512 || !utf8.ValidString(source.SenderID) ||
		!utf8.ValidString(source.MediaKind) || !utf8.ValidString(source.MediaName) || !validMediaMIME(source.MediaMIME) ||
		!validMediaPresentation(source.MediaKind, source.MediaName) ||
		!validReplyMetadata(source.ReplyToID, source.ReplyToText, source.ReplyToFromMe, source.ReplyMediaKind, source.ReplyMediaName, source.ReplyMediaMIME) ||
		source.ID == "" || !utf8.ValidString(source.ID) || !utf8.ValidString(source.Text) || !validReactionGroups(source.Reactions) {
		return messageView{}, errors.New("tui initial message rejected")
	}
	text := source.Text
	if !source.BodyRetained {
		text = ""
	}
	message := messageView{
		id:             messageID(source.ID),
		sentAt:         source.SentAt,
		time:           localMessageTime(source.SentAt),
		text:           text,
		mediaKind:      source.MediaKind,
		mediaName:      source.MediaName,
		mediaMIME:      source.MediaMIME,
		fromMe:         source.FromMe,
		bodyRetained:   source.BodyRetained,
		replyText:      source.ReplyToText,
		replyFromMe:    source.ReplyToFromMe,
		replyMediaKind: source.ReplyMediaKind,
		replyMediaName: source.ReplyMediaName,
		replyMediaMIME: source.ReplyMediaMIME,
		replyToID:      messageID(source.ReplyToID),
		hasReply:       source.ReplyToID != "",
		senderID:       source.SenderID,
		isGroup:        source.IsGroup,
	}
	message.reactionCount = len(source.Reactions)
	for index, group := range source.Reactions {
		message.reactions[index] = reactionGroupView{emoji: group.Emoji, count: group.Count, own: group.Own}
	}
	return message, nil
}

func validReactionGroups(groups []ReactionGroup) bool {
	if len(groups) > maxReactionGroups {
		return false
	}
	seen := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		if len(group.Emoji) > maxReactionEmojiBytes || !validEmojiPreference(group.Emoji) || group.Count == 0 || group.Count > 64 {
			return false
		}
		if _, duplicate := seen[group.Emoji]; duplicate {
			return false
		}
		seen[group.Emoji] = struct{}{}
	}
	return true
}
