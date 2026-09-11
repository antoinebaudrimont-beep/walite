package tui

import "github.com/gdamore/tcell/v2"

// pasteRoutingState consumes one complete tcell bracketed-paste sequence at
// the event-loop boundary. Payload keys never reach command dispatch.
type pasteRoutingState struct {
	active     bool
	insertText bool
}

func (paste *pasteRoutingState) transition(model *viewModel, event *tcell.EventPaste) {
	if event == nil {
		return
	}
	if event.Start() {
		paste.active = true
		paste.insertText = acceptsPastedText(model)
		return
	}
	paste.active = false
	paste.insertText = false
}

func (paste *pasteRoutingState) routeKey(model *viewModel, event *tcell.EventKey) (handled, changed bool) {
	if paste == nil || !paste.active {
		return false, false
	}
	if !paste.insertText || model == nil || event == nil || event.Key() != tcell.KeyRune {
		return true, false
	}
	return true, model.composer.insert(event.Rune())
}

func acceptsPastedText(model *viewModel) bool {
	if model == nil || model.settingsOpen || model.emojiPicker.open || model.linkPicker.open || model.replySelect.valid || model.quitConfirm ||
		model.sendPending || model.sendUncertain {
		return false
	}
	return model.mode == modeCompose || model.mode == modeFile
}
