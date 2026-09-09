package tui

import "time"

type olderHistoryState struct {
	active  bool
	request OlderHistoryRequest
}

func requestOlderHistory(model *viewModel, width, height int) bool {
	if model == nil || model.chats == nil {
		return false
	}
	chat, ok := model.chats.selectedChat()
	if !ok || chat.messageCount == 0 || chat.messages == nil {
		model.sendStatus = "No older messages available"
		return true
	}
	if chat.messageCount >= SelectedChatHistoryCapacity {
		model.sendStatus = "Selected chat history limit reached"
		return true
	}
	if model.chatView.scrollOffset != maximumScrollOffset(model, width, height) {
		model.sendStatus = "Reach the oldest loaded message before loading older history"
		return true
	}
	frontier := chat.messages[0]
	request := OlderHistoryRequest{
		ChatID: chat.id, Revision: model.selectionEpoch, OldestMessageID: string(frontier.id),
		OldestSentAt: frontier.sentAt, OldestFromMe: frontier.fromMe, Count: OlderHistoryPageSize,
	}
	if model.olderHistory.active && model.olderHistory.request == request {
		model.sendStatus = "Loading older messages…"
		return true
	}
	if model.loadOlder == nil || !model.loadOlder(request) {
		model.sendStatus = "Older history unavailable while disconnected"
		return true
	}
	model.olderHistory = olderHistoryState{active: true, request: request}
	model.sendStatus = "Loading older messages…"
	return true
}

func applyOlderHistoryResult(model *viewModel, result OlderHistoryResult) bool {
	if model == nil || model.chats == nil || !model.olderHistory.active || result.Request != model.olderHistory.request {
		return false
	}
	model.olderHistory = olderHistoryState{}
	chat, ok := model.chats.selectedChat()
	if !ok || chat.id != result.Request.ChatID || model.selectionEpoch != result.Request.Revision || chat.messageCount == 0 || chat.messages == nil {
		return false
	}
	frontier := chat.messages[0]
	if string(frontier.id) != result.Request.OldestMessageID || !frontier.sentAt.Equal(result.Request.OldestSentAt) || frontier.fromMe != result.Request.OldestFromMe {
		return false
	}
	switch result.Kind {
	case OlderHistoryNoMore:
		model.sendStatus = "No older messages available"
		return true
	case OlderHistoryUnavailable:
		model.sendStatus = "Older history unavailable while disconnected"
		return true
	case OlderHistoryFailed:
		model.sendStatus = "Older history could not be loaded"
		return true
	case OlderHistoryLoaded:
	default:
		return false
	}
	if len(result.Messages) == 0 {
		model.sendStatus = "No older messages available"
		return true
	}
	if len(result.Messages) > OlderHistoryPageSize || !validOlderPage(result.Messages, frontier) {
		model.sendStatus = "Older history could not be loaded"
		return true
	}
	converted := make([]messageView, len(result.Messages))
	for index, source := range result.Messages {
		message, err := messageViewFromInitial(source)
		if err != nil {
			model.sendStatus = "Older history could not be loaded"
			return true
		}
		converted[index] = message
	}
	focusedID := messageID("")
	focusedFromCompose := false
	if model.replySelect.valid && model.replySelect.index >= 0 && model.replySelect.index < chat.messageCount {
		focusedID = chat.messages[model.replySelect.index].id
		focusedFromCompose = model.replySelect.fromCompose
	}
	added := 0
	// Insert nearest-first so a partial final page retains the messages closest
	// to the existing frontier. The fixed-capacity insertion policy then ignores
	// older overflow rather than evicting recent history.
	for index := len(converted) - 1; index >= 0; index-- {
		message := converted[index]
		if _, duplicate := model.chats.messageIndexByID(model.chats.selectedIndex(), message.id); duplicate {
			continue
		}
		if inserted, _ := insertBoundedMessage(chat, message); inserted {
			added++
		}
	}
	if added == 0 {
		if chat.messageCount >= SelectedChatHistoryCapacity {
			model.sendStatus = "Selected chat history limit reached"
			return true
		}
		model.sendStatus = "No older messages available"
		return true
	}
	chat.revision++
	if focusedID != "" {
		if index, found := model.chats.messageIndexByID(model.chats.selectedIndex(), focusedID); found {
			model.replySelect = replySelectionState{valid: true, index: index, fromCompose: focusedFromCompose}
		} else {
			model.replySelect = replySelectionState{}
		}
	}
	clampMessageViewport(model, model.terminalWidth, model.terminalHeight)
	enrichChatDisplay(chat, &model.display)
	model.sendStatus = "Loaded " + decimal(added) + " older messages"
	return true
}

func validOlderPage(messages []InitialMessage, frontier messageView) bool {
	seen := make(map[string]struct{}, len(messages))
	previousTime := time.Time{}
	previousID := ""
	for _, message := range messages {
		if message.ID == "" || !olderInitialBefore(message.SentAt, message.ID, frontier.sentAt, string(frontier.id)) {
			return false
		}
		if _, duplicate := seen[message.ID]; duplicate {
			return false
		}
		seen[message.ID] = struct{}{}
		if !previousTime.IsZero() && olderInitialBefore(message.SentAt, message.ID, previousTime, previousID) {
			return false
		}
		previousTime, previousID = message.SentAt, message.ID
	}
	return true
}

func olderInitialBefore(leftTime time.Time, leftID string, rightTime time.Time, rightID string) bool {
	if leftTime.Equal(rightTime) {
		return leftID < rightID
	}
	return leftTime.Before(rightTime)
}

func decimal(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	position := len(digits)
	for value > 0 {
		position--
		digits[position] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[position:])
}
