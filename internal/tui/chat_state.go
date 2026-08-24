package tui

import (
	"strconv"
	"unicode/utf8"
)

const (
	maxChats    = 4
	maxMessages = 32
)

type messageID uint32

type messageView struct {
	id        messageID
	time      string
	text      string
	replyToID messageID
	hasReply  bool
}

type chatView struct {
	title        string
	messages     [maxMessages]messageView
	messageCount int
	unreadCount  uint16
	activity     uint64
}

type chatState struct {
	chats         [maxChats]chatView
	chatCount     int
	selected      int
	nextMessageID messageID
	nextActivity  uint64
}

func newDemoChatState() *chatState {
	state := &chatState{
		chats: [maxChats]chatView{
			newDemoChat("Demo Chat", "Demo", 18),
			newDemoChat("Project Room", "Project", 15),
			newDemoChat("Family Demo", "Family-demo", 12),
			newDemoChat("Test Contact", "Test-contact", 10),
		},
		chatCount:    maxChats,
		nextActivity: maxChats + 1,
	}
	for index := 0; index < state.chatCount; index++ {
		state.chats[index].activity = uint64(state.chatCount - index)
	}
	state.chats[0].messages[0] = messageView{time: "09:42", text: "Synthetic message one"}
	state.chats[0].messages[1] = messageView{time: "09:45", text: "Synthetic reply"}
	state.chats[0].messages[2] = messageView{time: "09:47", text: "Synthetic terminal preview"}
	state.chats[0].messages[3] = messageView{
		time: "09:49",
		text: "Synthetic wrapping preview that stays bounded while demonstrating a longer conversation line",
	}
	state.chats[1].unreadCount = 3
	state.chats[2].unreadCount = 1
	state.chats[3].unreadCount = 12
	state.assignMessageIDs()
	return state
}

func newDemoChat(title, label string, count int) chatView {
	chat := chatView{title: title, messageCount: count}
	for index := 0; index < count; index++ {
		chat.messages[index] = messageView{
			time: "10:" + twoDigits(index),
			text: label + " synthetic message " + strconv.Itoa(index+1),
		}
	}
	return chat
}

func twoDigits(value int) string {
	if value < 10 {
		return "0" + strconv.Itoa(value)
	}
	return strconv.Itoa(value)
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

func (state *chatState) moveSelection(delta int) bool {
	if state == nil {
		return false
	}
	next := state.selected + delta
	if next < 0 || next >= state.chatCount {
		return false
	}
	state.selected = next
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
	if message.id == 0 {
		message.id = state.allocateMessageID()
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

func (state *chatState) findMessageByID(chatIndex int, id messageID) (messageView, bool) {
	chat, ok := state.chatAt(chatIndex)
	if !ok {
		return messageView{}, false
	}
	for index := 0; index < chat.messageCount; index++ {
		if chat.messages[index].id == id {
			return chat.messages[index], true
		}
	}
	return messageView{}, false
}

func (state *chatState) allocateMessageID() messageID {
	if state.nextMessageID == 0 {
		state.nextMessageID = 1
	}
	id := state.nextMessageID
	state.nextMessageID++
	return id
}

func (state *chatState) assignMessageIDs() {
	next := messageID(1)
	for chatIndex := 0; chatIndex < state.chatCount; chatIndex++ {
		chat := &state.chats[chatIndex]
		for messageIndex := 0; messageIndex < chat.messageCount; messageIndex++ {
			chat.messages[messageIndex].id = next
			next++
		}
	}
	state.nextMessageID = next
}

func (state *chatState) promoteChatActivity(chatIndex int) bool {
	if state == nil || chatIndex < 0 || chatIndex >= state.chatCount {
		return false
	}
	if state.nextActivity == 0 {
		state.nextActivity = 1
	}
	state.chats[chatIndex].activity = state.nextActivity
	state.nextActivity++
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
	if !state.appendMessage(chatIndex, messageView{time: "now", text: text}) {
		return false
	}
	if !selected && state.chats[chatIndex].unreadCount < ^uint16(0) {
		state.chats[chatIndex].unreadCount++
	}
	return state.promoteChatActivity(chatIndex)
}

func (state *chatState) recordUnreadActivity(chatIndex int, count uint16) bool {
	chat, ok := state.chatAt(chatIndex)
	if !ok || count == 0 {
		return false
	}
	if count > ^uint16(0)-chat.unreadCount {
		chat.unreadCount = ^uint16(0)
	} else {
		chat.unreadCount += count
	}
	return state.promoteChatActivity(chatIndex)
}
