package tui

type replySelectionState struct {
	valid       bool
	index       int
	fromCompose bool
}

type replyTarget struct {
	valid bool
	id    messageID
}

func focusNewestVisibleMessage(model *viewModel, width, height int) bool {
	if _, ok := model.chats.selectedChat(); !ok {
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
	if model.mode != modeCompose {
		return false
	}
	if _, ok := model.chats.selectedChat(); !ok {
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
	chat, ok := model.chats.selectedChat()
	if !ok {
		return false
	}
	next := model.replySelect.index + delta
	if next < 0 || next >= chat.messageCount {
		return false
	}
	model.replySelect.index = next
	ensureReplySelectionVisible(model, width, height)
	return true
}

func chooseReplyTarget(model *viewModel) bool {
	if !model.replySelect.valid {
		return false
	}
	chat, ok := model.chats.selectedChat()
	if !ok {
		return false
	}
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
	chat, ok := model.chats.selectedChat()
	if !ok {
		model.replySelect = replySelectionState{}
		return
	}
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
	chat, ok := model.chats.selectedChat()
	if !ok {
		return
	}
	model.chatView.scrollOffset = chat.messageCount - model.replySelect.index - 1
	if model.chatView.scrollOffset < 0 {
		model.chatView.scrollOffset = 0
	}
	if maximum := maximumScrollOffset(model, width, height); model.chatView.scrollOffset > maximum {
		model.chatView.scrollOffset = maximum
	}
}
