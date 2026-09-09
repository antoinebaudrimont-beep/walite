package tui

// readReceiptIntent survives the selected-chat cache page load. It is created
// only by explicit user interaction, never startup, live arrival, or summary
// refresh. At most one pending load intent exists because chat loads coalesce.
type readReceiptIntent struct {
	chatID      string
	unreadCount uint32
}

func prepareReadReceipt(model *viewModel, chat *chatView) {
	if model != nil && !model.options.SendReadReceipts {
		model.readIntent = readReceiptIntent{}
		model.readRequest = ReadReceiptRequest{}
		return
	}
	if model == nil || chat == nil || model.readIntent.chatID != chat.id || model.readIntent.unreadCount == 0 {
		return
	}
	if chat.messageCount == 0 {
		return
	}
	// UnreadCount counts incoming messages, so locate that many incoming
	// identities from newest to oldest rather than assuming every tail entry is
	// incoming. This excludes interleaved local sends without losing a receipt.
	first := chat.messageCount
	remaining := model.readIntent.unreadCount
	for index := chat.messageCount - 1; index >= 0 && remaining > 0; index-- {
		if chat.messages[index].fromMe {
			continue
		}
		first = index
		remaining--
	}
	messages := make([]ReadReceiptMessage, 0, MaxInitialMessagesPerChat)
	// Walk newest-first so interleaved outgoing messages cannot consume the
	// request bound, then reverse to deterministic oldest-first API order.
	for index := chat.messageCount - 1; index >= first && len(messages) < MaxInitialMessagesPerChat; index-- {
		message := chat.messages[index]
		if message.fromMe || message.id == "" || message.sentAt.IsZero() || chat.isGroup && message.senderID == "" {
			continue
		}
		messages = append(messages, ReadReceiptMessage{
			MessageID: string(message.id), SentAt: message.sentAt, SenderID: message.senderID,
		})
	}
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
	model.readIntent = readReceiptIntent{}
	if len(messages) != 0 {
		model.readRequest = ReadReceiptRequest{ChatID: chat.id, IsGroup: chat.isGroup, Messages: messages}
	}
}

// markSelectedChatReadOnInteraction separates arrival from reading. An
// explicit compose/reply action clears the local unread frontier immediately
// and prepares one bounded remote acknowledgement when enabled.
func markSelectedChatReadOnInteraction(model *viewModel) bool {
	if model == nil || model.chats == nil {
		return false
	}
	chat, ok := model.chats.selectedChat()
	if !ok || chat.unreadCount == 0 {
		return false
	}
	boundary := unreadBoundaryForChat(chat)
	model.localReadRequest = LocalReadRequest{ChatID: chat.id, ActivityTime: chat.activityTime}
	model.readIntent = readReceiptIntent{chatID: chat.id, unreadCount: chat.unreadCount}
	prepareReadReceipt(model, chat)
	chat.unreadCount = 0
	if !model.chatView.unreadBoundary.valid {
		model.chatView.unreadBoundary = boundary
	}
	return true
}

// requestPendingReadReceipt performs bounded admission only. A failed or
// unavailable admission is not retried forever, and never restores local unread.
func requestPendingReadReceipt(model *viewModel, send func(ReadReceiptRequest) bool) {
	if model == nil || model.readRequest.ChatID == "" {
		return
	}
	request := model.readRequest
	model.readRequest = ReadReceiptRequest{}
	if send != nil && model.options.SendReadReceipts {
		send(request)
	}
}
