package tui

func selectedChatLoadRequest(model *viewModel) (ChatLoadRequest, bool) {
	if model == nil || model.chats == nil {
		return ChatLoadRequest{}, false
	}
	chat, ok := model.chats.selectedChat()
	if !ok {
		return ChatLoadRequest{}, false
	}
	return ChatLoadRequest{ChatID: chat.id, Revision: chat.revision}, true
}

func requestSelectedChatLoad(model *viewModel, load func(ChatLoadRequest) bool) {
	if load == nil {
		return
	}
	if request, ok := selectedChatLoadRequest(model); ok {
		load(request)
	}
}

func applyChatLoad(model *viewModel, result ChatLoadResult) bool {
	if model == nil || model.chats == nil || result.ChatID == "" || len(result.Messages) > MaxInitialMessagesPerChat {
		return false
	}
	chat, ok := model.chats.selectedChat()
	if !ok || chat.id != result.ChatID || chat.revision != result.Revision {
		return false
	}
	loaded, err := chatStateFromInitial(InitialState{Chats: []InitialChat{{
		ID: chat.id, Title: chat.title, IsGroup: chat.isGroup, UnreadCount: chat.unreadCount,
		ActivityTime: chat.activityTime, Messages: result.Messages,
	}}})
	if err != nil {
		return false
	}
	loadedChat, _ := loaded.selectedChat()
	// A committed live message may have entered this chat before the cache page
	// completed. Merge the current bounded working set so a cache load can never
	// erase already-visible committed data.
	if chat.messages != nil {
		for index := 0; index < chat.messageCount; index++ {
			message := chat.messages[index]
			if _, found := loaded.messageIndexByID(0, message.id); !found {
				insertBoundedMessage(loadedChat, message)
			}
		}
	}
	chat.messages, chat.messageCount = loadedChat.messages, loadedChat.messageCount
	// Display metadata can arrive before this asynchronous page. Reapply the
	// bounded presentation cache after replacing the raw persisted messages so
	// every accepted page gets the same sender-name enrichment.
	enrichChatDisplay(chat, &model.display)
	chat.revision++
	model.chatView.reset()
	model.chatView.unreadBoundary = unreadBoundaryForChat(chat)
	prepareReadReceipt(model, chat)
	if model.readIntent.chatID == chat.id {
		model.readIntent = readReceiptIntent{}
	}
	clampMessageViewport(model, model.terminalWidth, model.terminalHeight)
	return true
}

func applyChatSummaries(model *viewModel, update InitialState) bool {
	if model == nil || model.chats == nil {
		return false
	}
	fresh, err := chatStateFromInitial(update)
	if err != nil {
		return false
	}
	selectedID := ""
	if selected, ok := model.chats.selectedChat(); ok {
		selectedID = selected.id
	}
	old := make(map[string]chatView, model.chats.chatCount)
	for index := 0; index < model.chats.chatCount; index++ {
		old[model.chats.chats[index].id] = model.chats.chats[index]
	}
	for index := 0; index < fresh.chatCount; index++ {
		chat := &fresh.chats[index]
		if prior, ok := old[chat.id]; ok {
			chat.messages = prior.messages
			chat.messageCount = prior.messageCount
			chat.revision = prior.revision
		}
		if chat.id == selectedID {
			fresh.selected = index
		}
	}
	newSelectedID := ""
	if selected, ok := fresh.selectedChat(); ok {
		newSelectedID = selected.id
	}
	for index := 0; index < fresh.chatCount; index++ {
		if index == fresh.selectedIndex() {
			fresh.expandMessageBuffer(index)
		} else {
			fresh.compactMessageBuffer(index)
		}
	}
	changed := fresh.chatCount != model.chats.chatCount
	if !changed {
		for index := 0; index < fresh.chatCount; index++ {
			left, right := fresh.chats[index], model.chats.chats[index]
			if left.id != right.id || left.title != right.title || left.isGroup != right.isGroup ||
				left.unreadCount != right.unreadCount || !left.activityTime.Equal(right.activityTime) {
				changed = true
				break
			}
		}
	}
	if !changed {
		return false
	}
	*model.chats = *fresh
	if newSelectedID != selectedID {
		model.selectionEpoch++
		model.olderHistory = olderHistoryState{}
	}
	model.chats.clampSelection()
	clampMessageViewport(model, model.terminalWidth, model.terminalHeight)
	return true
}
