package tui

import "github.com/gdamore/tcell/v2"

const (
	settingsPopupWidth  = 44
	settingsPopupHeight = 11
	settingsSaveRow     = 4
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
			state.draft.Theme = nextTheme(state.draft.Theme)
		case 1:
			state.draft.ShowTimestamps = !state.draft.ShowTimestamps
		case 2:
			state.draft.ConfirmQuit = !state.draft.ConfirmQuit
		case 3:
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

func nextTheme(theme string) string {
	switch theme {
	case ThemeTerminal:
		return ThemeDark
	case ThemeDark:
		return ThemeLight
	case ThemeLight:
		return ThemeHighContrast
	default:
		return ThemeTerminal
	}
}

func themeLabel(theme string) string {
	switch theme {
	case ThemeDark:
		return "Dark"
	case ThemeLight:
		return "Light"
	case ThemeHighContrast:
		return "High contrast"
	default:
		return "Terminal"
	}
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
	styles := stylesFor(options.Theme)
	if model.settings.initialized {
		options = model.settings.draft
		styles = stylesFor(options.Theme)
	}
	left, top, right, bottom := popupInterior(screen, width, height, settingsPopupWidth, settingsPopupHeight, styles.popup, styles.border)
	rows := []string{
		settingsTextValueLabel("Theme", themeLabel(options.Theme), right-left-2),
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
		putText(screen, left, top, right, "> "+rows[selected], styles.popupSelected)
		if bottom-top > 1 {
			putText(screen, left, top+1, right, "Settings · ↑↓/jk move", styles.popup.Bold(true))
		}
		if bottom-top > 2 {
			putText(screen, left, top+2, right, status, styles.popup)
		}
		return
	}
	putText(screen, left, top, right, "Settings", styles.popup.Bold(true))
	for index, row := range rows {
		style := styles.popup
		prefix := "  "
		if index == selected {
			prefix = "> "
			style = styles.popupSelected
		}
		fillMessageRow(screen, left, top+1+index, right, style)
		putText(screen, left, top+1+index, right, prefix+row, style)
	}
	putText(screen, left, top+6, right, "↑/↓ or j/k move · Save applies changes", styles.popup)
	putText(screen, left, top+7, right, status, styles.popup)
}

// Preserve the value when a narrow panel needs to shorten a setting's name.
func settingsValueLabel(name string, value bool, width int) string {
	return settingsTextValueLabel(name, onOffLabel(value), width)
}

func settingsTextValueLabel(name, value string, width int) string {
	suffix := ": " + value
	if width <= len(suffix) {
		return truncateDisplayWidth(value, width)
	}
	return truncateDisplayWidth(name, width-len(suffix)) + suffix
}

// popupInterior clears all cells, then returns exclusive content bounds. A
// very small screen uses a borderless fallback with the same cell clipping.
func popupInterior(screen tcell.Screen, width, height, desiredWidth, desiredHeight int, fill, border tcell.Style) (left, top, right, bottom int) {
	w, h := min(width, desiredWidth), min(height, desiredHeight)
	left, top = (width-w)/2, (height-h)/2
	right, bottom = left+w, top+h
	for y := top; y < bottom; y++ {
		fillMessageRow(screen, left, y, right, fill)
	}
	screen.LockRegion(left, top, w, h, false)
	if w < 6 || h < 5 {
		return
	}
	drawHorizontalStyle(screen, left+1, right-2, top, '─', border)
	drawHorizontalStyle(screen, left+1, right-2, bottom-1, '─', border)
	drawVerticalStyle(screen, top+1, bottom-2, left, '│', border)
	drawVerticalStyle(screen, top+1, bottom-2, right-1, '│', border)
	setRuneStyle(screen, left, top, '┌', border)
	setRuneStyle(screen, right-1, top, '┐', border)
	setRuneStyle(screen, left, bottom-1, '└', border)
	setRuneStyle(screen, right-1, bottom-1, '┘', border)
	return left + 1, top + 1, right - 1, bottom - 1
}

func drawQuitConfirmation(screen tcell.Screen, model *viewModel, width, height int) {
	styles := model.styles()
	left, top, right, bottom := popupInterior(screen, width, height, 34, 5, styles.popup, styles.border)
	putText(screen, left, top, right, "Quit walite?", styles.popup.Bold(true))
	if bottom-top > 1 {
		putText(screen, left, top+1, right, "Enter quit · Esc cancel", styles.popupSelected)
	}
}
