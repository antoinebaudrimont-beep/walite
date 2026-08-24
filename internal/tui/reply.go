package tui

type messageID uint32

type replySelectionState struct {
	valid       bool
	index       int
	fromCompose bool
}

type replyTarget struct {
	valid bool
	id    messageID
}

func (model *viewModel) assignMessageIDs() {
	next := messageID(1)
	for chatIndex := 0; chatIndex < model.chatCount; chatIndex++ {
		chat := &model.chats[chatIndex]
		for messageIndex := 0; messageIndex < chat.messageCount; messageIndex++ {
			chat.messages[messageIndex].id = next
			next++
		}
	}
	model.nextMessageID = next
}

func (model *viewModel) allocateMessageID() messageID {
	if model.nextMessageID == 0 {
		model.nextMessageID = 1
	}
	id := model.nextMessageID
	model.nextMessageID++
	return id
}

func focusNewestVisibleMessage(model *viewModel, width, height int) bool {
	if model.selectedChat < 0 || model.selectedChat >= model.chatCount {
		return false
	}
	start, end := visibleMessageRange(model, width, height)
	if start >= end {
		return false
	}
	model.replySelect = replySelectionState{valid: true, index: end - 1}
	model.replyTarget = replyTarget{}
	return true
}

func focusReplyFromCompose(model *viewModel, width, height int) bool {
	if model.mode != modeCompose || model.selectedChat < 0 || model.selectedChat >= model.chatCount {
		return false
	}
	start, end := visibleMessageRange(model, width, height)
	if start >= end {
		return false
	}
	model.replySelect = replySelectionState{valid: true, index: end - 1, fromCompose: true}
	return true
}

func moveMessageFocus(model *viewModel, delta, width, height int) bool {
	if !model.replySelect.valid {
		return focusNewestVisibleMessage(model, width, height)
	}
	if model.selectedChat < 0 || model.selectedChat >= model.chatCount {
		return false
	}
	chat := &model.chats[model.selectedChat]
	next := model.replySelect.index + delta
	if next < 0 || next >= chat.messageCount {
		return false
	}
	model.replySelect.index = next
	ensureReplySelectionVisible(model, width, height)
	return true
}

func chooseReplyTarget(model *viewModel) bool {
	if !model.replySelect.valid || model.selectedChat < 0 || model.selectedChat >= model.chatCount {
		return false
	}
	chat := &model.chats[model.selectedChat]
	index := model.replySelect.index
	if index < 0 || index >= chat.messageCount || chat.messages[index].id == 0 {
		return false
	}
	preserveDraft := model.replySelect.fromCompose
	model.replyTarget = replyTarget{valid: true, id: chat.messages[index].id}
	model.replySelect = replySelectionState{}
	model.mode = modeCompose
	if !preserveDraft {
		model.composer.clear()
	}
	return true
}

func clampReplySelection(model *viewModel, width, height int) {
	if !model.replySelect.valid {
		return
	}
	if model.selectedChat < 0 || model.selectedChat >= model.chatCount {
		model.replySelect = replySelectionState{}
		return
	}
	chat := &model.chats[model.selectedChat]
	if chat.messageCount == 0 {
		model.replySelect = replySelectionState{}
		return
	}
	model.replySelect.valid = true
	if model.replySelect.index < 0 {
		model.replySelect.index = 0
	}
	if model.replySelect.index >= chat.messageCount {
		model.replySelect.index = chat.messageCount - 1
	}
	ensureReplySelectionVisible(model, width, height)
}

func ensureReplySelectionVisible(model *viewModel, width, height int) {
	if !model.replySelect.valid {
		return
	}
	start, end := visibleMessageRange(model, width, height)
	if model.replySelect.index >= start && model.replySelect.index < end {
		return
	}
	chat := &model.chats[model.selectedChat]
	model.scrollOffset = chat.messageCount - model.replySelect.index - 1
	if model.scrollOffset < 0 {
		model.scrollOffset = 0
	}
	if maximum := maximumScrollOffset(model, width, height); model.scrollOffset > maximum {
		model.scrollOffset = maximum
	}
}

func findMessageByID(chat *chatView, id messageID) (messageView, bool) {
	for index := 0; index < chat.messageCount; index++ {
		if chat.messages[index].id == id {
			return chat.messages[index], true
		}
	}
	return messageView{}, false
}
