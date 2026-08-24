package tui

import "unicode/utf8"

func touchChatActivity(model *viewModel, chatIndex int) bool {
	if model == nil || chatIndex < 0 || chatIndex >= model.chatCount {
		return false
	}
	if model.nextActivity == 0 {
		model.nextActivity = 1
	}
	model.chats[chatIndex].activity = model.nextActivity
	model.nextActivity++
	if chatIndex == 0 {
		return true
	}

	selectedIndex := model.selectedChat
	active := model.chats[chatIndex]
	copy(model.chats[1:chatIndex+1], model.chats[:chatIndex])
	model.chats[0] = active
	switch {
	case selectedIndex == chatIndex:
		model.selectedChat = 0
	case selectedIndex >= 0 && selectedIndex < chatIndex:
		model.selectedChat = selectedIndex + 1
	}
	return true
}

func recordSyntheticIncoming(model *viewModel, chatIndex int, text string) bool {
	if model == nil || chatIndex < 0 || chatIndex >= model.chatCount ||
		text == "" || len(text) > maxDraftBytes || !utf8.ValidString(text) {
		return false
	}
	selected := chatIndex == model.selectedChat
	message := messageView{id: model.allocateMessageID(), time: "now", text: text}
	appendBoundedMessage(&model.chats[chatIndex], message)
	if !selected && model.chats[chatIndex].unreadCount < ^uint16(0) {
		model.chats[chatIndex].unreadCount++
	}
	return touchChatActivity(model, chatIndex)
}

func recordUnreadActivity(model *viewModel, chatIndex int, count uint16) bool {
	if model == nil || chatIndex < 0 || chatIndex >= model.chatCount || count == 0 {
		return false
	}
	unread := model.chats[chatIndex].unreadCount
	if count > ^uint16(0)-unread {
		unread = ^uint16(0)
	} else {
		unread += count
	}
	model.chats[chatIndex].unreadCount = unread
	return touchChatActivity(model, chatIndex)
}

func appendBoundedMessage(chat *chatView, message messageView) {
	if chat.messageCount < len(chat.messages) {
		chat.messages[chat.messageCount] = message
		chat.messageCount++
		return
	}
	copy(chat.messages[:], chat.messages[1:])
	chat.messages[len(chat.messages)-1] = message
}
