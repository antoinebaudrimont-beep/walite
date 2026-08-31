package tui

import (
	"unicode/utf8"
)

type liveMessageMutation struct {
	changed        bool
	inserted       bool
	insertionIndex int
}

func applyLiveMessage(model *viewModel, event LiveMessage) bool {
	if model == nil || model.chats == nil || !validLiveMessage(event) {
		return false
	}
	chatIndex, ok := model.chats.chatIndexByID(event.ChatID)
	if !ok {
		chatIndex, ok = model.chats.admitLiveChat(event)
		if !ok {
			return false
		}
	}
	selectedChat, selected := model.chats.selectedChat()
	selectedID := ""
	if selected {
		selectedID = selectedChat.id
	}
	selectedEvent := selectedID == event.ChatID
	readingOlder := selectedEvent && model.chatView.scrollOffset > 0
	oldVisibleEnd := 0
	if readingOlder {
		_, oldVisibleEnd = visibleMessageRange(model, model.terminalWidth, model.terminalHeight)
	}

	focusedID := messageID("")
	focusedFromCompose := false
	if selectedEvent && model.replySelect.valid {
		if chat, available := model.chats.chatAt(chatIndex); available && model.replySelect.index >= 0 && model.replySelect.index < chat.messageCount {
			focusedID = chat.messages[model.replySelect.index].id
			focusedFromCompose = model.replySelect.fromCompose
		}
	}

	mutation := model.chats.applyLiveMessage(chatIndex, event)
	if !mutation.changed {
		return false
	}
	model.chats.sortByActivity()

	if selectedEvent && mutation.inserted && readingOlder && mutation.insertionIndex >= oldVisibleEnd {
		model.chatView.scrollOffset++
	}
	if focusedID != "" {
		selectedIndex := model.chats.selectedIndex()
		if index, found := model.chats.messageIndexByID(selectedIndex, focusedID); found {
			model.replySelect = replySelectionState{valid: true, index: index, fromCompose: focusedFromCompose}
		} else {
			model.replySelect = replySelectionState{}
		}
	}
	if model.terminalWidth > 0 && model.terminalHeight > 0 {
		clampMessageViewport(model, model.terminalWidth, model.terminalHeight)
		clampReplySelection(model, model.terminalWidth, model.terminalHeight)
	}
	return true
}

// admitLiveChat adds an unknown committed chat to the fixed presentation
// working set. When full, only a more-recent chat may replace the least-active
// non-selected chat; the selected chat is pinned. Live events do not carry a
// display name, so the stable chat ID is the bounded presentation fallback.
func (state *chatState) admitLiveChat(event LiveMessage) (int, bool) {
	if state == nil || state.chatCount < 0 || state.chatCount > len(state.chats) {
		return 0, false
	}
	incoming := chatView{id: event.ChatID, title: event.ChatID, activityTime: event.ActivityTime}
	if state.chatCount < len(state.chats) {
		index := state.chatCount
		state.chats[index] = incoming
		state.chatCount++
		return index, true
	}

	eviction := -1
	for index := 0; index < state.chatCount; index++ {
		if index == state.selected {
			continue
		}
		if eviction < 0 || chatBefore(state.chats[eviction], state.chats[index]) {
			eviction = index
		}
	}
	if eviction < 0 || !chatBefore(incoming, state.chats[eviction]) {
		return 0, false
	}
	state.chats[eviction] = incoming
	return eviction, true
}

func validLiveMessage(event LiveMessage) bool {
	return event.ChatID != "" && event.MessageID != "" &&
		utf8.ValidString(event.ChatID) && utf8.ValidString(event.MessageID) && utf8.ValidString(event.Text) &&
		validReplyMetadata(event.ReplyToID, event.ReplyToText, event.ReplyToFromMe) &&
		!event.SentAt.IsZero() && !event.ActivityTime.IsZero() && !event.ActivityTime.Before(event.SentAt)
}

func (state *chatState) applyLiveMessage(chatIndex int, event LiveMessage) liveMessageMutation {
	chat, ok := state.chatAt(chatIndex)
	if !ok {
		return liveMessageMutation{}
	}
	changed := false
	if chat.unreadCount != event.UnreadCount {
		chat.unreadCount = event.UnreadCount
		changed = true
	}
	if event.ActivityTime.After(chat.activityTime) {
		chat.activityTime = event.ActivityTime
		changed = true
	}
	if _, duplicate := state.messageIndexByID(chatIndex, messageID(event.MessageID)); duplicate {
		return liveMessageMutation{changed: changed}
	}

	text := event.Text
	if !event.BodyRetained {
		text = ""
	}
	message := messageView{
		id:           messageID(event.MessageID),
		sentAt:       event.SentAt,
		time:         event.SentAt.Format("15:04"),
		text:         text,
		fromMe:       event.FromMe,
		bodyRetained: event.BodyRetained,
		replyText:    event.ReplyToText,
		replyFromMe:  event.ReplyToFromMe,
		replyToID:    messageID(event.ReplyToID),
		hasReply:     event.ReplyToID != "",
	}
	inserted, insertionIndex := insertBoundedMessage(chat, message)
	return liveMessageMutation{changed: changed || inserted, inserted: inserted, insertionIndex: insertionIndex}
}

func insertBoundedMessage(chat *chatView, message messageView) (bool, int) {
	if chat == nil {
		return false, -1
	}
	insertion := chat.messageCount
	for index := 0; index < chat.messageCount; index++ {
		if messageBefore(message, chat.messages[index]) {
			insertion = index
			break
		}
	}
	if chat.messageCount < len(chat.messages) {
		copy(chat.messages[insertion+1:chat.messageCount+1], chat.messages[insertion:chat.messageCount])
		chat.messages[insertion] = message
		chat.messageCount++
		return true, insertion
	}
	if insertion == 0 {
		return false, -1
	}
	insertion--
	copy(chat.messages[:insertion], chat.messages[1:insertion+1])
	chat.messages[insertion] = message
	return true, insertion
}

func messageBefore(left, right messageView) bool {
	if left.sentAt.IsZero() || right.sentAt.IsZero() {
		if left.sentAt.IsZero() == right.sentAt.IsZero() {
			return false
		}
		return !left.sentAt.IsZero()
	}
	if left.sentAt.Equal(right.sentAt) {
		return left.id < right.id
	}
	return left.sentAt.Before(right.sentAt)
}

func (state *chatState) sortByActivity() bool {
	if state == nil || state.chatCount < 2 {
		return false
	}
	selectedID := ""
	if selected, ok := state.selectedChat(); ok {
		selectedID = selected.id
	}
	var before [maxChats]string
	for index := 0; index < state.chatCount; index++ {
		before[index] = state.chats[index].id
	}
	for index := 1; index < state.chatCount; index++ {
		chat := state.chats[index]
		position := index
		for position > 0 && chatBefore(chat, state.chats[position-1]) {
			state.chats[position] = state.chats[position-1]
			position--
		}
		state.chats[position] = chat
	}
	changed := false
	for index := 0; index < state.chatCount; index++ {
		if before[index] != state.chats[index].id {
			changed = true
		}
		if state.chats[index].id == selectedID {
			state.selected = index
		}
	}
	return changed
}

func chatBefore(left, right chatView) bool {
	if left.activityTime.Equal(right.activityTime) {
		return left.id < right.id
	}
	return left.activityTime.After(right.activityTime)
}
