package tui

import "github.com/gdamore/tcell/v2"

func handleKey(model *viewModel, event *tcell.EventKey, width, height int) (changed, exit bool) {
	if event.Key() == tcell.KeyCtrlC {
		return false, true
	}
	if model.mode == modeCompose {
		return handleComposeKey(model, event), false
	}

	switch {
	case event.Key() == tcell.KeyEscape:
		return false, true
	case event.Key() == tcell.KeyEnter:
		model.mode = modeCompose
		model.composer.clear()
		return true, false
	case event.Key() == tcell.KeyUp || event.Key() == tcell.KeyRune && event.Rune() == 'k':
		return moveChatSelection(model, -1), false
	case event.Key() == tcell.KeyDown || event.Key() == tcell.KeyRune && event.Rune() == 'j':
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
	return true
}

func handleComposeKey(model *viewModel, event *tcell.EventKey) bool {
	switch event.Key() {
	case tcell.KeyEscape:
		model.composer.clear()
		model.mode = modeNavigate
		return true
	case tcell.KeyEnter:
		return submitLocalMessage(model)
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

func submitLocalMessage(model *viewModel) bool {
	model.composer.normalize()
	if model.composer.length == 0 || model.selectedChat < 0 || model.selectedChat >= model.chatCount {
		return false
	}
	chat := &model.chats[model.selectedChat]
	message := messageView{time: "now", text: model.composer.text()}
	if chat.messageCount < len(chat.messages) {
		chat.messages[chat.messageCount] = message
		chat.messageCount++
	} else {
		copy(chat.messages[:], chat.messages[1:])
		chat.messages[len(chat.messages)-1] = message
	}
	model.composer.clear()
	model.scrollOffset = 0
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
}
