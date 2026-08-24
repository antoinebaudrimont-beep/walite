package tui

import "github.com/gdamore/tcell/v2"

func handleKey(model *viewModel, event *tcell.EventKey, width, height int) (changed, exit bool) {
	if event.Key() == tcell.KeyCtrlC {
		return false, true
	}
	if model.emojiPicker.open {
		return handleEmojiKey(model, event, width, height), false
	}
	if model.replySelect.valid && model.replySelect.fromCompose {
		return handleComposeReplySelectionKey(model, event, width, height), false
	}
	if model.mode == modeCompose {
		return handleComposeKey(model, event, width, height), false
	}

	switch {
	case event.Key() == tcell.KeyEscape:
		if model.replySelect.valid {
			model.replySelect = replySelectionState{}
			return true, false
		}
		return false, true
	case event.Key() == tcell.KeyEnter:
		if model.replySelect.valid {
			return chooseReplyTarget(model), false
		}
		model.mode = modeCompose
		model.composer.clear()
		model.replyTarget = replyTarget{}
		model.replySelect = replySelectionState{}
		return true, false
	case event.Key() == tcell.KeyUp:
		return moveMessageFocus(model, -1, width, height), false
	case event.Key() == tcell.KeyDown:
		return moveMessageFocus(model, 1, width, height), false
	case event.Key() == tcell.KeyRune && event.Rune() == 'k':
		return moveChatSelection(model, -1), false
	case event.Key() == tcell.KeyRune && event.Rune() == 'j':
		return moveChatSelection(model, 1), false
	case event.Key() == tcell.KeyPgUp || event.Key() == tcell.KeyCtrlU:
		return scrollPage(model, width, height, true), false
	case event.Key() == tcell.KeyPgDn || event.Key() == tcell.KeyCtrlD:
		return scrollPage(model, width, height, false), false
	}
	return false, false
}

func moveChatSelection(model *viewModel, delta int) bool {
	next := model.selectedChat + delta
	if next < 0 || next >= model.chatCount {
		return false
	}
	model.selectedChat = next
	model.chats[next].unreadCount = 0
	model.scrollOffset = 0
	model.replySelect = replySelectionState{}
	model.replyTarget = replyTarget{}
	return true
}

func handleComposeKey(model *viewModel, event *tcell.EventKey, width, height int) bool {
	switch event.Key() {
	case tcell.KeyEscape:
		model.composer.clear()
		model.replyTarget = replyTarget{}
		model.mode = modeNavigate
		return true
	case tcell.KeyEnter:
		return submitLocalMessage(model)
	case tcell.KeyCtrlE:
		model.emojiPicker.prepareOpen()
		return true
	case tcell.KeyCtrlR:
		return focusReplyFromCompose(model, width, height)
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

func handleComposeReplySelectionKey(model *viewModel, event *tcell.EventKey, width, height int) bool {
	switch {
	case event.Key() == tcell.KeyEscape:
		model.replySelect = replySelectionState{}
		return true
	case event.Key() == tcell.KeyEnter:
		return chooseReplyTarget(model)
	case event.Key() == tcell.KeyUp || event.Key() == tcell.KeyRune && event.Rune() == 'k':
		return moveMessageFocus(model, -1, width, height)
	case event.Key() == tcell.KeyDown || event.Key() == tcell.KeyRune && event.Rune() == 'j':
		return moveMessageFocus(model, 1, width, height)
	}
	return false
}

func handleEmojiKey(model *viewModel, event *tcell.EventKey, width, height int) bool {
	columns := emojiGridColumnsForSize(width, height)
	switch event.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlE:
		model.emojiPicker.open = false
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
		if !model.composer.insertText(value) {
			return false
		}
		model.emojiPicker.remember(value)
		if model.preferencesPath != "" {
			_ = saveEmojiPreferences(model.preferencesPath, &model.emojiPicker)
		}
		model.emojiPicker.open = false
		return true
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

func submitLocalMessage(model *viewModel) bool {
	model.composer.normalize()
	if model.composer.length == 0 || model.selectedChat < 0 || model.selectedChat >= model.chatCount {
		return false
	}
	chat := &model.chats[model.selectedChat]
	message := messageView{id: model.allocateMessageID(), time: "now", text: model.composer.text()}
	if model.replyTarget.valid {
		message.replyToID = model.replyTarget.id
		message.hasReply = true
	}
	appendBoundedMessage(chat, message)
	model.composer.clear()
	model.replyTarget = replyTarget{}
	model.replySelect = replySelectionState{}
	model.scrollOffset = 0
	touchChatActivity(model, model.selectedChat)
	return true
}

func scrollPage(model *viewModel, width, height int, older bool) bool {
	start, end := visibleMessageRange(model, width, height)
	page := end - start
	if page < 1 {
		page = 1
	}
	previous := model.scrollOffset
	if older {
		model.scrollOffset += page
		maximum := maximumScrollOffset(model, width, height)
		if model.scrollOffset > maximum {
			model.scrollOffset = maximum
		}
	} else {
		model.scrollOffset -= page
		if model.scrollOffset < 0 {
			model.scrollOffset = 0
		}
	}
	return model.scrollOffset != previous
}

func clampView(model *viewModel, width, height int) {
	if model.chatCount < 1 {
		model.selectedChat = 0
		model.scrollOffset = 0
		return
	}
	model.composer.normalize()
	model.emojiPicker.clamp()
	if model.selectedChat < 0 {
		model.selectedChat = 0
	}
	if model.selectedChat >= model.chatCount {
		model.selectedChat = model.chatCount - 1
	}
	if model.scrollOffset < 0 {
		model.scrollOffset = 0
	}
	if maximum := maximumScrollOffset(model, width, height); model.scrollOffset > maximum {
		model.scrollOffset = maximum
	}
	clampReplySelection(model, width, height)
}
