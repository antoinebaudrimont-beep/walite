package tui

import "github.com/gdamore/tcell/v2"

const (
	settingsPopupWidth  = 44
	settingsPopupHeight = 11
	settingsSaveRow     = 3
)

type settingsState struct {
	draft       Options
	selected    int
	initialized bool
	request     bool
	pending     bool
	status      string
}

func openSettings(model *viewModel) {
	if !model.settings.pending {
		model.settings = settingsState{draft: model.options, initialized: true}
	}
	model.settingsOpen = true
}

func handleSettingsKey(model *viewModel, event *tcell.EventKey) bool {
	if event.Key() == tcell.KeyEscape || event.Key() == tcell.KeyCtrlP {
		model.settingsOpen = false
		return true
	}
	if !model.settings.initialized {
		openSettings(model)
	}
	state := &model.settings
	if state.pending {
		return false
	}
	switch {
	case event.Key() == tcell.KeyUp || event.Key() == tcell.KeyRune && event.Rune() == 'k':
		if state.selected > 0 {
			state.selected--
			return true
		}
	case event.Key() == tcell.KeyDown || event.Key() == tcell.KeyRune && event.Rune() == 'j':
		if state.selected < settingsSaveRow {
			state.selected++
			return true
		}
	case event.Key() == tcell.KeyEnter || event.Key() == tcell.KeyRune && event.Rune() == ' ':
		state.status = ""
		switch state.selected {
		case 0:
			state.draft.ShowTimestamps = !state.draft.ShowTimestamps
		case 1:
			state.draft.ConfirmQuit = !state.draft.ConfirmQuit
		case 2:
			state.draft.SendReadReceipts = !state.draft.SendReadReceipts
		case settingsSaveRow:
			if state.draft == model.options {
				model.settingsOpen = false
			} else {
				state.request = true
			}
		}
		return true
	}
	return false
}

func requestSettingsSave(model *viewModel, save func(Options) bool) {
	if !model.settings.request {
		return
	}
	model.settings.request = false
	if save == nil || !save(model.settings.draft) {
		model.settings.status = "Save unavailable; changes not applied"
		return
	}
	model.settings.pending = true
	model.settings.status = "Saving…"
}

func finishSettingsSave(model *viewModel, err error) bool {
	if !model.settings.pending {
		return false
	}
	model.settings.pending = false
	if err != nil {
		model.settings.status = "Save failed; changes not applied"
		model.settingsOpen = true
		return true
	}
	model.options = model.settings.draft
	if !model.options.SendReadReceipts {
		// Discard any frontier waiting for a cache page. Enabling later must
		// not acknowledge a chat opened while the preference was off.
		model.readIntent = readReceiptIntent{}
		model.readRequest = ReadReceiptRequest{}
	}
	model.settings.status = ""
	model.settingsOpen = false
	return true
}

func onOffLabel(value bool) string {
	if value {
		return "On"
	}
	return "Off"
}

func drawSettingsPopup(screen tcell.Screen, model *viewModel, width, height int) {
	if width <= 0 || height <= 0 {
		return
	}
	options := model.options
	if model.settings.initialized {
		options = model.settings.draft
	}
	left, top, right, bottom := popupInterior(screen, width, height, settingsPopupWidth, settingsPopupHeight)
	rows := []string{
		settingsValueLabel("Timestamps", options.ShowTimestamps, right-left-2),
		settingsValueLabel("Confirm quit", options.ConfirmQuit, right-left-2),
		settingsValueLabel("Send read receipts", options.SendReadReceipts, right-left-2),
		"Save and close",
	}
	selected := min(max(model.settings.selected, 0), settingsSaveRow)
	status := model.settings.status
	if status == "" {
		status = "Enter/Space change · Esc/Ctrl-P cancel"
	}
	if bottom-top < 8 {
		// Tiny grids always retain the selected row; navigating scrolls this
		// one-row viewport instead of hiding keyboard focus below the screen.
		putText(screen, left, top, right, "> "+rows[selected], tcell.StyleDefault.Reverse(true).Bold(true))
		if bottom-top > 1 {
			putText(screen, left, top+1, right, "Settings · ↑↓/jk move", tcell.StyleDefault.Bold(true))
		}
		if bottom-top > 2 {
			putText(screen, left, top+2, right, status, tcell.StyleDefault)
		}
		return
	}
	putText(screen, left, top, right, "Settings · Theme: "+options.Theme, tcell.StyleDefault.Bold(true))
	for index, row := range rows {
		style := tcell.StyleDefault
		prefix := "  "
		if index == selected {
			prefix = "> "
			style = style.Reverse(true).Bold(true)
		}
		fillMessageRow(screen, left, top+1+index, right, style)
		putText(screen, left, top+1+index, right, prefix+row, style)
	}
	putText(screen, left, top+6, right, "↑/↓ or j/k move · Save applies changes", tcell.StyleDefault)
	putText(screen, left, top+7, right, status, tcell.StyleDefault)
}

// Preserve the value when a narrow panel needs to shorten a setting's name.
func settingsValueLabel(name string, value bool, width int) string {
	suffix := ": " + onOffLabel(value)
	if width <= len(suffix) {
		return truncateDisplayWidth(onOffLabel(value), width)
	}
	return truncateDisplayWidth(name, width-len(suffix)) + suffix
}

// popupInterior clears all cells, then returns exclusive content bounds. A
// very small screen uses a borderless fallback with the same cell clipping.
func popupInterior(screen tcell.Screen, width, height, desiredWidth, desiredHeight int) (left, top, right, bottom int) {
	w, h := min(width, desiredWidth), min(height, desiredHeight)
	left, top = (width-w)/2, (height-h)/2
	right, bottom = left+w, top+h
	for y := top; y < bottom; y++ {
		fillMessageRow(screen, left, y, right, tcell.StyleDefault)
	}
	screen.LockRegion(left, top, w, h, false)
	if w < 6 || h < 5 {
		return
	}
	drawHorizontal(screen, left+1, right-2, top, '─')
	drawHorizontal(screen, left+1, right-2, bottom-1, '─')
	drawVertical(screen, top+1, bottom-2, left, '│')
	drawVertical(screen, top+1, bottom-2, right-1, '│')
	setRune(screen, left, top, '┌')
	setRune(screen, right-1, top, '┐')
	setRune(screen, left, bottom-1, '└')
	setRune(screen, right-1, bottom-1, '┘')
	return left + 1, top + 1, right - 1, bottom - 1
}

func drawQuitConfirmation(screen tcell.Screen, width, height int) {
	left, top, right, bottom := popupInterior(screen, width, height, 34, 5)
	putText(screen, left, top, right, "Quit walite?", tcell.StyleDefault.Bold(true))
	if bottom-top > 1 {
		putText(screen, left, top+1, right, "Enter quit · Esc cancel", tcell.StyleDefault.Reverse(true))
	}
}
