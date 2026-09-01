package tui

import (
	"errors"
	"github.com/gdamore/tcell/v2"
)

func handleKey(model *viewModel, event *tcell.EventKey, width, height int) (changed, exit bool) {
	if event.Key() == tcell.KeyCtrlC {
		return false, true
	}
	if model.settingsOpen {
		if event.Key() == tcell.KeyEscape {
			model.settingsOpen = false
			return true, false
		}
		return false, false
	}
	if event.Key() == tcell.KeyCtrlP {
		model.settingsOpen = true
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
	if model.replySelect.valid && model.replySelect.fromCompose {
		return handleComposeReplySelectionKey(model, event, width, height), false
	}
	if model.mode == modeCompose {
		return handleComposeKey(model, event, width, height), false
	}
	if model.replySelect.valid {
		return handleReplySelectionKey(model, event, width, height), false
	}

	switch {
	case event.Key() == tcell.KeyEscape:
		return false, true
	case event.Key() == tcell.KeyEnter:
		if _, ok := model.chats.selectedChat(); !ok {
			return false, false
		}
		model.mode = modeCompose
		model.composer.clear()
		model.replyTarget = replyTarget{}
		model.replySelect = replySelectionState{}
		return true, false
	case event.Key() == tcell.KeyCtrlR:
		return focusNewestVisibleMessage(model, width, height), false
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
	}
	return false, false
}

func moveChatSelection(model *viewModel, delta int) bool {
	nextChat, ok := model.chats.chatAt(model.chats.selectedIndex() + delta)
	if !ok {
		return false
	}
	boundary := unreadBoundaryForChat(nextChat)
	if nextChat.unreadCount > 0 {
		model.localReadRequest = LocalReadRequest{ChatID: nextChat.id, ActivityTime: nextChat.activityTime}
	}
	if !model.chats.moveSelection(delta) {
		return false
	}
	model.chatView.reset()
	model.chatView.unreadBoundary = boundary
	model.replySelect = replySelectionState{}
	model.replyTarget = replyTarget{}
	return true
}

func handleReplySelectionKey(model *viewModel, event *tcell.EventKey, width, height int) bool {
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

func handleComposeKey(model *viewModel, event *tcell.EventKey, width, height int) bool {
	switch event.Key() {
	case tcell.KeyEscape:
		model.sendStatus = ""
		model.composer.clear()
		model.replyTarget = replyTarget{}
		model.mode = modeNavigate
		return true
	case tcell.KeyEnter:
		return submitOutgoingMessage(model)
	case tcell.KeyCtrlE:
		model.emojiPicker.prepareOpen()
		return true
	case tcell.KeyCtrlR:
		return focusReplyFromCompose(model, width, height)
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
		model.emojiPicker.close()
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
		model.emojiPicker.close()
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
	request := SendRequest{ChatID: selected.id, Text: model.composer.text()}
	if model.replyTarget.valid {
		request.ReplyToID = string(model.replyTarget.id)
		if target, found := model.chats.findMessageByID(model.chats.selectedIndex(), model.replyTarget.id); found && target.bodyRetained {
			request.ReplyToText, request.ReplyToFromMe = target.text, target.fromMe
		}
	}
	if err := model.send(request); err != nil {
		if model.asyncSend {
			model.sendStatus = "Send unavailable or rejected; draft kept"
			if errors.Is(err, ErrGroupReplyUnavailable) {
				model.sendStatus = "Group replies not available yet; draft kept"
			}
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
