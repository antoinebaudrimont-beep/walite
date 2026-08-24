package tui

import (
	"strconv"

	"github.com/gdamore/tcell/v2"
)

// chatViewState contains transient reading position for the selected chat.
// It is deliberately outside chatState and is never persisted.
type chatViewState struct {
	scrollOffset int
}

func (state *chatViewState) reset() {
	state.scrollOffset = 0
}

func scrollMessages(model *viewModel, width, height int, older bool) bool {
	previous := model.chatView.scrollOffset
	if older {
		model.chatView.scrollOffset++
	} else {
		model.chatView.scrollOffset--
	}
	clampMessageViewport(model, width, height)
	return model.chatView.scrollOffset != previous
}

func jumpMessageViewport(model *viewModel, width, height int, oldest bool) bool {
	previous := model.chatView.scrollOffset
	if oldest {
		model.chatView.scrollOffset = maximumScrollOffset(model, width, height)
	} else {
		model.chatView.reset()
	}
	return model.chatView.scrollOffset != previous
}

func clampMessageViewport(model *viewModel, width, height int) {
	if model.chatView.scrollOffset < 0 {
		model.chatView.scrollOffset = 0
	}
	if maximum := maximumScrollOffset(model, width, height); model.chatView.scrollOffset > maximum {
		model.chatView.scrollOffset = maximum
	}
}

func newerMessageCount(model *viewModel, width, height int) int {
	chat, ok := model.chats.selectedChat()
	if !ok {
		return 0
	}
	_, end := visibleMessageRange(model, width, height)
	if end < 0 || end >= chat.messageCount {
		return 0
	}
	return chat.messageCount - end
}

func drawNewerMessagesIndicator(screen tcell.Screen, model *viewModel, width, height, x, y, limit int) {
	count := newerMessageCount(model, width, height)
	if count < 1 {
		return
	}
	label := "↓ " + strconv.Itoa(count) + " newer message"
	if count != 1 {
		label += "s"
	}
	putText(screen, x, y, limit, label, tcell.StyleDefault.Dim(true))
}

// recordIncomingMessage applies synthetic incoming activity while keeping the
// currently visible messages anchored when the user is reading older history.
func recordIncomingMessage(model *viewModel, chatIndex int, text string) bool {
	selected := chatIndex == model.chats.selectedIndex()
	readingOlder := selected && model.chatView.scrollOffset > 0
	if !model.chats.recordIncomingMessage(chatIndex, text) {
		return false
	}
	if readingOlder {
		model.chatView.scrollOffset++
		if model.terminalWidth > 0 && model.terminalHeight > 0 {
			clampMessageViewport(model, model.terminalWidth, model.terminalHeight)
		}
	}
	return true
}
