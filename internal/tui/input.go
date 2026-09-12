package tui

import "github.com/gdamore/tcell/v2"

func handleKey(model *viewModel, event *tcell.EventKey, width, height int) (changed, exit bool) {
	if event.Key() == tcell.KeyEscape && model.closeExternalPreview != nil && model.closeExternalPreview() {
		closeMedia(model)
		model.sendStatus = "External preview closing"
		return true, false
	}
	if event.Key() == tcell.KeyCtrlC {
		return false, true
	}
	if model.quitConfirm {
		switch event.Key() {
		case tcell.KeyEnter:
			return false, true
		case tcell.KeyEscape, tcell.KeyCtrlP:
			model.quitConfirm = false
			return true, false
		}
		return false, false
	}
	if model.settingsOpen {
		return handleSettingsKey(model, event), false
	}
	if model.linkPicker.open {
		return handleLinkPickerKey(model, event), false
	}
	if event.Key() == tcell.KeyCtrlP {
		closeMedia(model)
		openSettings(model)
		return true, false
	}
	// Protect the admitted draft against edits, cancellation, and key-repeat
	// resends. Reading, settings, resize and Ctrl-C remain responsive.
	if model.sendPending || model.sendUncertain {
		switch event.Key() {
		case tcell.KeyUp:
			return scrollMessages(model, width, height, true), false
		case tcell.KeyDown:
			return scrollMessages(model, width, height, false), false
		case tcell.KeyPgUp:
			return scrollPage(model, width, height, true), false
		case tcell.KeyPgDn:
			return scrollPage(model, width, height, false), false
		case tcell.KeyEnd:
			return jumpMessageViewport(model, width, height, false), false
		case tcell.KeyEscape:
			if model.sendUncertain {
				model.sendUncertain, model.sendStatus = false, ""
				if model.reactionTarget.valid {
					model.reactionTarget = reactionTargetState{}
					model.replySelect = replySelectionState{}
					return true, false
				}
				model.composer.clear()
				model.mode = modeNavigate
				return true, false
			}
		}
		return false, false
	}
	if model.emojiPicker.open {
		return handleEmojiKey(model, event, width, height), false
	}
	if event.Key() == tcell.KeyEscape && model.mediaTarget.active {
		closeMedia(model)
		model.sendStatus = "Preview closed"
		return true, false
	}
	if model.replySelect.valid && model.replySelect.fromCompose {
		return handleComposeReplySelectionKey(model, event, width, height), false
	}
	if model.mode == modeCompose {
		return handleComposeKey(model, event, width, height), false
	}
	if model.mode == modeFile {
		return handleFileKey(model, event), false
	}
	if model.replySelect.valid {
		return handleReplySelectionKey(model, event, width, height), false
	}
	if closesMediaPreview(event) {
		closeMedia(model)
	}

	switch {
	case event.Key() == tcell.KeyTAB && width < narrowWidth:
		closeMedia(model)
		if model.narrowPane == narrowPaneChats {
			model.narrowPane = narrowPaneConversation
		} else {
			model.narrowPane = narrowPaneChats
		}
		return true, false
	case event.Key() == tcell.KeyEscape:
		if model.options.ConfirmQuit {
			model.quitConfirm = true
			return true, false
		}
		return false, true
	case event.Key() == tcell.KeyEnter:
		selected, ok := model.chats.selectedChat()
		if !ok {
			return false, false
		}
		if selected.readOnly {
			model.sendStatus = "Sending is not supported for this chat type"
			return true, false
		}
		markSelectedChatReadOnInteraction(model)
		model.mode = modeCompose
		model.composer.clear()
		model.replyTarget = replyTarget{}
		model.replySelect = replySelectionState{}
		return true, false
	case event.Key() == tcell.KeyRune && event.Rune() == 'F':
		selected, ok := model.chats.selectedChat()
		if !ok {
			return false, false
		}
		if selected.readOnly {
			model.sendStatus = "Sending is not supported for this chat type"
			return true, false
		}
		closeMedia(model)
		markSelectedChatReadOnInteraction(model)
		model.mode = modeFile
		model.composer.clear()
		model.replyTarget = replyTarget{}
		model.replySelect = replySelectionState{}
		model.sendStatus = ""
		return true, false
	case event.Key() == tcell.KeyCtrlR:
		read := markSelectedChatReadOnInteraction(model)
		return focusNewestVisibleMessage(model, width, height) || read, false
	case event.Key() == tcell.KeyUp:
		return scrollMessages(model, width, height, true), false
	case event.Key() == tcell.KeyDown:
		return scrollMessages(model, width, height, false), false
	case event.Key() == tcell.KeyHome:
		return jumpMessageViewport(model, width, height, true), false
	case event.Key() == tcell.KeyEnd:
		return jumpMessageViewport(model, width, height, false), false
	case event.Key() == tcell.KeyRune && event.Rune() == 'k':
		return moveChatSelection(model, -1), false
	case event.Key() == tcell.KeyRune && event.Rune() == 'j':
		return moveChatSelection(model, 1), false
	case event.Key() == tcell.KeyPgUp || event.Key() == tcell.KeyCtrlU:
		return scrollPage(model, width, height, true), false
	case event.Key() == tcell.KeyPgDn || event.Key() == tcell.KeyCtrlD:
		return scrollPage(model, width, height, false), false
	case event.Key() == tcell.KeyRune && (event.Rune() == 'P' || event.Rune() == 'p'):
		return requestVisibleMedia(model, MediaPreview, width, height), false
	case event.Key() == tcell.KeyRune && (event.Rune() == 'S' || event.Rune() == 's'):
		return requestVisibleMedia(model, MediaSave, width, height), false
	case event.Key() == tcell.KeyRune && (event.Rune() == 'O' || event.Rune() == 'o'):
		return requestOlderHistory(model, width, height), false
	}
	return false, false
}

func closesMediaPreview(event *tcell.EventKey) bool {
	if event == nil {
		return false
	}
	switch event.Key() {
	case tcell.KeyEnter, tcell.KeyCtrlR, tcell.KeyUp, tcell.KeyDown, tcell.KeyHome, tcell.KeyEnd, tcell.KeyPgUp, tcell.KeyPgDn, tcell.KeyCtrlU, tcell.KeyCtrlD:
		return true
	case tcell.KeyRune:
		return event.Rune() == 'j' || event.Rune() == 'k' || event.Rune() == 'o' || event.Rune() == 'O' || event.Rune() == 'F' || event.Rune() == 'L' || event.Rune() == 'l'
	default:
		return false
	}
}

func handleFileKey(model *viewModel, event *tcell.EventKey) bool {
	switch event.Key() {
	case tcell.KeyEscape:
		model.sendStatus = ""
		model.composer.clear()
		model.mode = modeNavigate
		return true
	case tcell.KeyEnter:
		return submitOutgoingFile(model)
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		return model.composer.backspace()
	case tcell.KeyDelete:
		return model.composer.delete()
	case tcell.KeyLeft:
		return model.composer.moveLeft()
	case tcell.KeyRight:
		return model.composer.moveRight()
	case tcell.KeyRune:
		return model.composer.insert(event.Rune())
	default:
		return false
	}
}

func moveChatSelection(model *viewModel, delta int) bool {
	nextChat, ok := model.chats.chatAt(model.chats.selectedIndex() + delta)
	if !ok {
		return false
	}
	model.readIntent = readReceiptIntent{}
	boundary := unreadBoundaryForChat(nextChat)
	if nextChat.unreadCount > 0 {
		model.localReadRequest = LocalReadRequest{ChatID: nextChat.id, ActivityTime: nextChat.activityTime}
		model.readIntent = readReceiptIntent{chatID: nextChat.id, unreadCount: nextChat.unreadCount}
		prepareReadReceipt(model, nextChat)
	}
	if !model.chats.moveSelection(delta) {
		return false
	}
	model.selectionEpoch++
	model.chatView.reset()
	model.chatView.unreadBoundary = boundary
	model.replySelect = replySelectionState{}
	model.replyTarget = replyTarget{}
	return true
}

func handleReplySelectionKey(model *viewModel, event *tcell.EventKey, width, height int) bool {
	switch {
	case event.Key() == tcell.KeyEscape:
		closeMedia(model)
		model.replySelect = replySelectionState{}
		return true
	case event.Key() == tcell.KeyEnter:
		closeMedia(model)
		read := markSelectedChatReadOnInteraction(model)
		if selected, ok := model.chats.selectedChat(); ok && selected.readOnly {
			model.sendStatus = "Sending is not supported for this chat type"
			return true
		}
		return chooseReplyTarget(model) || read
	case event.Key() == tcell.KeyUp || event.Key() == tcell.KeyRune && event.Rune() == 'k':
		closeMedia(model)
		return moveMessageFocus(model, -1, width, height)
	case event.Key() == tcell.KeyDown || event.Key() == tcell.KeyRune && event.Rune() == 'j':
		closeMedia(model)
		return moveMessageFocus(model, 1, width, height)
	case event.Key() == tcell.KeyRune && (event.Rune() == 'P' || event.Rune() == 'p'):
		return requestMediaAt(model, MediaPreview, model.replySelect.index, width, height)
	case event.Key() == tcell.KeyRune && (event.Rune() == 'S' || event.Rune() == 's'):
		return requestMediaAt(model, MediaSave, model.replySelect.index, width, height)
	case event.Key() == tcell.KeyRune && (event.Rune() == 'L' || event.Rune() == 'l'):
		return openFocusedLinks(model)
	case event.Key() == tcell.KeyRune && event.Rune() == 'R':
		closeMedia(model)
		if selected, ok := model.chats.selectedChat(); ok && selected.readOnly {
			model.sendStatus = "Sending is not supported for this chat type"
			return true
		}
		return beginReaction(model)
	}
	return false
}

func handleComposeKey(model *viewModel, event *tcell.EventKey, width, height int) bool {
	switch event.Key() {
	case tcell.KeyEscape:
		model.sendStatus = ""
		model.composer.clear()
		model.replyTarget = replyTarget{}
		model.mode = modeNavigate
		return true
	case tcell.KeyEnter:
		read := markSelectedChatReadOnInteraction(model)
		return submitOutgoingMessage(model) || read
	case tcell.KeyCtrlE:
		model.emojiPicker.prepareOpen()
		return true
	case tcell.KeyCtrlR:
		read := markSelectedChatReadOnInteraction(model)
		return focusReplyFromCompose(model, width, height) || read
	case tcell.KeyUp:
		return scrollMessages(model, width, height, true)
	case tcell.KeyDown:
		return scrollMessages(model, width, height, false)
	case tcell.KeyPgUp:
		return scrollPage(model, width, height, true)
	case tcell.KeyPgDn:
		return scrollPage(model, width, height, false)
	case tcell.KeyEnd:
		return jumpMessageViewport(model, width, height, false)
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		return model.composer.backspace()
	case tcell.KeyDelete:
		return model.composer.delete()
	case tcell.KeyLeft:
		return model.composer.moveLeft()
	case tcell.KeyRight:
		return model.composer.moveRight()
	case tcell.KeyRune:
		read := markSelectedChatReadOnInteraction(model)
		return model.composer.insert(event.Rune()) || read
	default:
		return false
	}
}

func handleComposeReplySelectionKey(model *viewModel, event *tcell.EventKey, width, height int) bool {
	switch {
	case event.Key() == tcell.KeyEscape:
		model.replySelect = replySelectionState{}
		return true
	case event.Key() == tcell.KeyEnter:
		closeMedia(model)
		read := markSelectedChatReadOnInteraction(model)
		if selected, ok := model.chats.selectedChat(); ok && selected.readOnly {
			model.sendStatus = "Sending is not supported for this chat type"
			return true
		}
		return chooseReplyTarget(model) || read
	case event.Key() == tcell.KeyUp || event.Key() == tcell.KeyRune && event.Rune() == 'k':
		closeMedia(model)
		return moveMessageFocus(model, -1, width, height)
	case event.Key() == tcell.KeyDown || event.Key() == tcell.KeyRune && event.Rune() == 'j':
		closeMedia(model)
		return moveMessageFocus(model, 1, width, height)
	case event.Key() == tcell.KeyRune && (event.Rune() == 'P' || event.Rune() == 'p'):
		return requestMediaAt(model, MediaPreview, model.replySelect.index, width, height)
	case event.Key() == tcell.KeyRune && (event.Rune() == 'S' || event.Rune() == 's'):
		return requestMediaAt(model, MediaSave, model.replySelect.index, width, height)
	case event.Key() == tcell.KeyRune && (event.Rune() == 'L' || event.Rune() == 'l'):
		return openFocusedLinks(model)
	case event.Key() == tcell.KeyRune && event.Rune() == 'R':
		closeMedia(model)
		if selected, ok := model.chats.selectedChat(); ok && selected.readOnly {
			model.sendStatus = "Sending is not supported for this chat type"
			return true
		}
		return beginReaction(model)
	}
	return false
}

func handleEmojiKey(model *viewModel, event *tcell.EventKey, width, height int) bool {
	columns := emojiGridColumnsForSize(width, height)
	switch event.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlE:
		model.emojiPicker.close()
		model.reactionTarget = reactionTargetState{}
		return true
	case tcell.KeyLeft:
		return model.emojiPicker.moveHorizontal(-1)
	case tcell.KeyRight:
		return model.emojiPicker.moveHorizontal(1)
	case tcell.KeyUp:
		return model.emojiPicker.moveVertical(-1, columns)
	case tcell.KeyDown:
		return model.emojiPicker.moveVertical(1, columns)
	case tcell.KeyTAB:
		return model.emojiPicker.moveCategory(1)
	case tcell.KeyBacktab:
		return model.emojiPicker.moveCategory(-1)
	case tcell.KeyEnter:
		value := model.emojiPicker.selectedEmoji()
		if model.reactionTarget.valid {
			return submitOutgoingReaction(model, value)
		}
		if !model.composer.insertText(value) {
			return false
		}
		model.emojiPicker.remember(value)
		if model.preferencesPath != "" {
			_ = saveEmojiPreferences(model.preferencesPath, &model.emojiPicker)
		}
		model.emojiPicker.close()
		return true
	case tcell.KeyDelete, tcell.KeyBackspace, tcell.KeyBackspace2:
		if model.reactionTarget.valid {
			return submitOutgoingReaction(model, "")
		}
		return false
	case tcell.KeyRune:
		switch event.Rune() {
		case 'h':
			return model.emojiPicker.moveHorizontal(-1)
		case 'l':
			return model.emojiPicker.moveHorizontal(1)
		case 'k':
			return model.emojiPicker.moveVertical(-1, columns)
		case 'j':
			return model.emojiPicker.moveVertical(1, columns)
		}
	}
	return false
}

func submitOutgoingMessage(model *viewModel) bool {
	if model.sendPending || model.sendUncertain {
		return false
	}
	model.composer.normalize()
	if model.composer.length == 0 || model.send == nil {
		return false
	}
	selected, ok := model.chats.selectedChat()
	if !ok {
		return false
	}
	if selected.readOnly {
		model.sendStatus = "Sending is not supported for this chat type"
		return true
	}
	request := SendRequest{ChatID: selected.id, Text: model.composer.text()}
	if model.replyTarget.valid {
		request.ReplyToID = string(model.replyTarget.id)
		if target, found := model.chats.findMessageByID(model.chats.selectedIndex(), model.replyTarget.id); found && (target.bodyRetained || target.mediaKind != "") {
			request.ReplyToText, request.ReplyToFromMe = target.text, target.fromMe
			request.ReplyMediaKind, request.ReplyMediaName, request.ReplyMediaMIME = target.mediaKind, target.mediaName, target.mediaMIME
			request.ReplyTargetSenderID, request.ReplyIsGroup = target.senderID, target.isGroup
		}
	}
	if err := model.send(request); err != nil {
		if model.asyncSend {
			model.sendStatus = "Send unavailable or rejected; draft kept"
			return true
		}
		return false
	}
	if model.asyncSend {
		model.sendPending = true
		model.sendStatus = "Sending… draft protected"
		return true
	}
	clearSentDraft(model)
	return true
}

func submitOutgoingFile(model *viewModel) bool {
	if model.sendPending || model.sendUncertain {
		return false
	}
	model.composer.normalize()
	if model.composer.length == 0 || model.send == nil {
		return false
	}
	selected, ok := model.chats.selectedChat()
	if !ok {
		return false
	}
	if selected.readOnly {
		model.sendStatus = "Sending is not supported for this chat type"
		return true
	}
	if err := model.send(SendRequest{ChatID: selected.id, FilePath: model.composer.text()}); err != nil {
		if model.asyncSend {
			model.sendStatus = "File send unavailable or rejected; path kept"
			return true
		}
		return false
	}
	if model.asyncSend {
		model.sendPending = true
		model.sendStatus = "Sending file… path protected"
		return true
	}
	clearSentDraft(model)
	model.mode = modeNavigate
	return true
}

func scrollPage(model *viewModel, width, height int, older bool) bool {
	start, end := visibleMessageRange(model, width, height)
	page := end - start
	if page < 1 {
		page = 1
	}
	previous := model.chatView.scrollOffset
	if older {
		model.chatView.scrollOffset += page
		maximum := maximumScrollOffset(model, width, height)
		if model.chatView.scrollOffset > maximum {
			model.chatView.scrollOffset = maximum
		}
	} else {
		model.chatView.scrollOffset -= page
		if model.chatView.scrollOffset < 0 {
			model.chatView.scrollOffset = 0
		}
	}
	return model.chatView.scrollOffset != previous
}

func clampView(model *viewModel, width, height int) {
	model.terminalWidth = width
	model.terminalHeight = height
	if model.chats.count() < 1 {
		model.chats.clampSelection()
		model.chatView.reset()
		return
	}
	model.composer.normalize()
	model.emojiPicker.clamp()
	model.chats.clampSelection()
	clampMessageViewport(model, width, height)
	clampReplySelection(model, width, height)
}
