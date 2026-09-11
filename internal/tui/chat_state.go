package tui

import (
	"strconv"
	"time"
	"unicode/utf8"
)

const (
	maxChats    = ChatWorkingSetCapacity
	maxMessages = MaxInitialMessagesPerChat
	// SelectedChatHistoryCapacity bounds the only expanded in-memory chat.
	SelectedChatHistoryCapacity = 256
	maxReplyIDBytes             = 512
	maxReplyTextBytes           = 1024
)

type messageID string

type messageView struct {
	id             messageID
	sentAt         time.Time
	time           string
	text           string
	mediaKind      string
	mediaName      string
	mediaMIME      string
	fromMe         bool
	bodyRetained   bool
	replyToID      messageID
	hasReply       bool
	replyText      string
	replyFromMe    bool
	replyMediaKind string
	replyMediaName string
	replyMediaMIME string
	senderID       string
	senderName     string
	senderQuality  uint8
	isGroup        bool
	reactions      [maxReactionGroups]reactionGroupView
	reactionCount  int
}

const (
	maxReactionGroups     = 16
	maxReactionEmojiBytes = 64
)

type reactionGroupView struct {
	emoji string
	count uint16
	own   bool
}

// Presentation mirrors the application's bounded quote fields without an
// application-model dependency. The adapter tests check fidelity at this seam.
func validReplyMetadata(id, text string, fromMe bool, mediaKind, mediaName, mediaMIME string) bool {
	if id == "" {
		return text == "" && !fromMe && mediaKind == "" && mediaName == "" && mediaMIME == ""
	}
	return len(id) <= maxReplyIDBytes && utf8.ValidString(id) && len(text) <= maxReplyTextBytes && utf8.ValidString(text) &&
		validMediaPresentation(mediaKind, mediaName) && validMediaMIME(mediaMIME) &&
		(mediaKind != "" || mediaName == "" && mediaMIME == "") && (text != "" || mediaKind != "")
}

type chatView struct {
	id           string
	title        string
	titleQuality uint8
	isGroup      bool
	messages     []messageView
	messageCount int
	unreadCount  uint32
	activityTime time.Time
	revision     uint64
	readOnly     bool
}

type chatState struct {
	chats       [maxChats]chatView
	chatCount   int
	selected    int
	nextLocalID uint64
}

func (state *chatState) count() int {
	if state == nil {
		return 0
	}
	return state.chatCount
}

func (state *chatState) selectedIndex() int {
	if state == nil {
		return 0
	}
	return state.selected
}

func (state *chatState) chatAt(index int) (*chatView, bool) {
	if state == nil || index < 0 || index >= state.chatCount {
		return nil, false
	}
	return &state.chats[index], true
}

func (state *chatState) selectedChat() (*chatView, bool) {
	return state.chatAt(state.selectedIndex())
}

func (state *chatState) chatIndexByID(id string) (int, bool) {
	if state == nil || id == "" {
		return 0, false
	}
	for index := 0; index < state.chatCount; index++ {
		if state.chats[index].id == id {
			return index, true
		}
	}
	return 0, false
}

func (state *chatState) moveSelection(delta int) bool {
	if state == nil {
		return false
	}
	next := state.selected + delta
	if next < 0 || next >= state.chatCount {
		return false
	}
	state.compactMessageBuffer(state.selected)
	state.selected = next
	state.expandMessageBuffer(next)
	state.chats[next].revision++
	state.chats[next].unreadCount = 0
	return true
}

func (state *chatState) clampSelection() {
	if state == nil {
		return
	}
	if state.chatCount < 1 {
		state.selected = 0
		return
	}
	if state.selected < 0 {
		state.selected = 0
	}
	if state.selected >= state.chatCount {
		state.selected = state.chatCount - 1
	}
}

func (state *chatState) appendMessage(chatIndex int, message messageView) bool {
	chat, ok := state.chatAt(chatIndex)
	if !ok {
		return false
	}
	if message.id == "" {
		message.id = state.allocateMessageID()
	}
	if chat.messages == nil && chatIndex == state.selectedIndex() {
		chat.messages = make([]messageView, SelectedChatHistoryCapacity)
	} else {
		chat.ensureMessages()
	}
	if chat.messageCount < len(chat.messages) {
		chat.messages[chat.messageCount] = message
		chat.messageCount++
		return true
	}
	copy(chat.messages[:], chat.messages[1:])
	chat.messages[len(chat.messages)-1] = message
	return true
}

func (chat *chatView) ensureMessages() {
	if chat != nil && chat.messages == nil {
		chat.messages = make([]messageView, maxMessages)
	}
}

func (state *chatState) expandMessageBuffer(chatIndex int) {
	chat, ok := state.chatAt(chatIndex)
	if !ok || len(chat.messages) == SelectedChatHistoryCapacity || chat.messageCount == 0 && chat.messages == nil {
		return
	}
	expanded := make([]messageView, SelectedChatHistoryCapacity)
	if chat.messages != nil {
		copy(expanded, chat.messages[:chat.messageCount])
	}
	chat.messages = expanded
}

func (state *chatState) compactMessageBuffer(chatIndex int) {
	chat, ok := state.chatAt(chatIndex)
	if !ok || len(chat.messages) <= maxMessages {
		return
	}
	if chat.messageCount == 0 {
		chat.messages = nil
		return
	}
	start := 0
	if chat.messageCount > maxMessages {
		start = chat.messageCount - maxMessages
	}
	compact := make([]messageView, maxMessages)
	chat.messageCount = copy(compact, chat.messages[start:chat.messageCount])
	chat.messages = compact
}

func (state *chatState) findMessageByID(chatIndex int, id messageID) (messageView, bool) {
	index, found := state.messageIndexByID(chatIndex, id)
	if !found {
		return messageView{}, false
	}
	return state.chats[chatIndex].messages[index], true
}

func (state *chatState) messageIndexByID(chatIndex int, id messageID) (int, bool) {
	chat, ok := state.chatAt(chatIndex)
	if !ok || id == "" || chat.messages == nil {
		return 0, false
	}
	for index := 0; index < chat.messageCount; index++ {
		if chat.messages[index].id == id {
			return index, true
		}
	}
	return 0, false
}

func (state *chatState) allocateMessageID() messageID {
	for {
		state.nextLocalID++
		id := messageID("local-" + strconv.FormatUint(state.nextLocalID, 10))
		if !state.containsMessageID(id) {
			return id
		}
	}
}

func (state *chatState) containsMessageID(id messageID) bool {
	for chatIndex := 0; chatIndex < state.chatCount; chatIndex++ {
		chat := &state.chats[chatIndex]
		if chat.messages == nil {
			continue
		}
		for messageIndex := 0; messageIndex < chat.messageCount; messageIndex++ {
			if chat.messages[messageIndex].id == id {
				return true
			}
		}
	}
	return false
}

func (state *chatState) promoteChatActivity(chatIndex int) bool {
	if state == nil || chatIndex < 0 || chatIndex >= state.chatCount {
		return false
	}
	if chatIndex == 0 {
		return true
	}

	selectedIndex := state.selected
	active := state.chats[chatIndex]
	copy(state.chats[1:chatIndex+1], state.chats[:chatIndex])
	state.chats[0] = active
	switch {
	case selectedIndex == chatIndex:
		state.selected = 0
	case selectedIndex >= 0 && selectedIndex < chatIndex:
		state.selected = selectedIndex + 1
	}
	return true
}

func (state *chatState) recordIncomingMessage(chatIndex int, text string) bool {
	if state == nil || text == "" || len(text) > maxDraftBytes || !utf8.ValidString(text) {
		return false
	}
	selected := chatIndex == state.selected
	if !state.appendMessage(chatIndex, messageView{time: "now", text: text, bodyRetained: true}) {
		return false
	}
	if !selected && state.chats[chatIndex].unreadCount < ^uint32(0) {
		state.chats[chatIndex].unreadCount++
	}
	return state.promoteChatActivity(chatIndex)
}

func (state *chatState) recordUnreadActivity(chatIndex int, count uint32) bool {
	chat, ok := state.chatAt(chatIndex)
	if !ok || count == 0 {
		return false
	}
	if count > ^uint32(0)-chat.unreadCount {
		chat.unreadCount = ^uint32(0)
	} else {
		chat.unreadCount += count
	}
	return state.promoteChatActivity(chatIndex)
}
