package tui

import "github.com/gdamore/tcell/v2"

func handleKey(model *viewModel, event *tcell.EventKey, width, height int) (changed, exit bool) {
	if event.Key() == tcell.KeyEscape || event.Key() == tcell.KeyCtrlC {
		return false, true
	}

	switch {
	case event.Key() == tcell.KeyUp || event.Key() == tcell.KeyRune && event.Rune() == 'k':
		if model.selectedChat > 0 {
			model.selectedChat--
			model.scrollOffset = 0
			return true, false
		}
	case event.Key() == tcell.KeyDown || event.Key() == tcell.KeyRune && event.Rune() == 'j':
		if model.selectedChat+1 < model.chatCount {
			model.selectedChat++
			model.scrollOffset = 0
			return true, false
		}
	case event.Key() == tcell.KeyPgUp || event.Key() == tcell.KeyCtrlU:
		return scrollPage(model, width, height, true), false
	case event.Key() == tcell.KeyPgDn || event.Key() == tcell.KeyCtrlD:
		return scrollPage(model, width, height, false), false
	}
	return false, false
}

func scrollPage(model *viewModel, width, height int, older bool) bool {
	start, end := visibleMessageRange(*model, width, height)
	page := end - start
	if page < 1 {
		page = 1
	}
	previous := model.scrollOffset
	if older {
		model.scrollOffset += page
		maximum := maximumScrollOffset(*model, width, height)
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
	if maximum := maximumScrollOffset(*model, width, height); model.scrollOffset > maximum {
		model.scrollOffset = maximum
	}
}
