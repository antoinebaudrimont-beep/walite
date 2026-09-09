package tui

type mediaTargetState struct {
	active, previewing bool
	chatID             string
	messageID          messageID
}

func requestVisibleMedia(model *viewModel, action MediaAction, width, height int) bool {
	if model == nil || model.media == nil {
		if model != nil {
			model.sendStatus = "Media action unavailable"
		}
		return model != nil
	}
	chat, ok := model.chats.selectedChat()
	if !ok {
		return false
	}
	start, end := visibleMessageRange(model, width, height)
	index := -1
	for candidate := end - 1; candidate >= start; candidate-- {
		if chat.messages[candidate].mediaKind != "" {
			index = candidate
			break
		}
	}
	if index < 0 {
		model.sendStatus = "No media message in the visible history"
		return true
	}
	return requestMediaAt(model, action, index, width, height)
}

func requestMediaAt(model *viewModel, action MediaAction, index, width, height int) bool {
	chat, ok := model.chats.selectedChat()
	if !ok || index < 0 || index >= chat.messageCount {
		return false
	}
	message := chat.messages[index]
	if message.mediaKind == "" {
		model.sendStatus = "Selected message has no media"
		return true
	}
	if action == MediaPreview && model.mediaTarget.active && model.mediaTarget.chatID == chat.id && model.mediaTarget.messageID == message.id {
		if model.mediaTarget.previewing {
			closeMedia(model)
			model.sendStatus = "Preview closed"
		} else {
			model.sendStatus = "Media preview is already loading"
		}
		return true
	}
	x, y, previewWidth, previewHeight := mediaPreviewRectangle(width, height)
	request := MediaRequest{Action: action, ChatID: chat.id, MessageID: string(message.id), Kind: message.mediaKind, X: x, Y: y, Width: previewWidth, Height: previewHeight}
	if !model.media(request) {
		model.sendStatus = "Media worker busy"
		return true
	}
	model.mediaTarget = mediaTargetState{active: true, chatID: chat.id, messageID: message.id}
	if action == MediaPreview {
		model.sendStatus = "Loading media preview…"
	} else {
		model.sendStatus = "Saving media…"
	}
	return true
}

func mediaPreviewRectangle(width, height int) (x, y, previewWidth, previewHeight int) {
	x, y = 0, 3
	right, bottom := width, height-3
	if width >= narrowWidth {
		x, right = paneSeparator(width)+2, width-2
	}
	if width <= 0 || height < shortHeight {
		return 0, 0, 0, 0
	}
	return x, y, right - x, bottom - y
}

func closeMedia(model *viewModel) {
	if model == nil {
		return
	}
	if model.mediaTarget.active && model.closeMedia != nil {
		model.closeMedia()
	}
	if model.mediaTarget.active {
		model.sendStatus = ""
	}
	model.mediaTarget = mediaTargetState{}
}

func applyMediaResult(model *viewModel, result MediaResult) bool {
	if model == nil || !model.mediaTarget.active || model.mediaTarget.chatID != result.ChatID || string(model.mediaTarget.messageID) != result.MessageID {
		return false
	}
	model.sendStatus = result.Status
	model.mediaTarget.previewing = result.Previewing
	if !result.Previewing {
		model.mediaTarget = mediaTargetState{}
	}
	return true
}
