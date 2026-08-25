package tui

import "github.com/gdamore/tcell/v2"

const (
	settingsPopupWidth  = 30
	settingsPopupHeight = 8
)

func drawSettingsPopup(screen tcell.Screen, model *viewModel, width, height int) {
	if width <= 0 || height <= 0 {
		return
	}
	lines := []string{
		"Settings",
		"Theme: " + model.options.Theme,
		"Time: " + enabledLabel(model.options.ShowTimestamps),
		"Confirm quit: " + yesNoLabel(model.options.ConfirmQuit),
		"",
		"Esc close",
	}
	if width < 18 || height < settingsPopupHeight {
		for row, line := range lines {
			if row >= height {
				break
			}
			style := tcell.StyleDefault
			if row == 0 {
				style = style.Bold(true)
			}
			putText(screen, 0, row, width, line, style)
		}
		return
	}

	popupWidth := settingsPopupWidth
	if popupWidth > width-2 {
		popupWidth = width - 2
	}
	left := (width - popupWidth) / 2
	top := (height - settingsPopupHeight) / 2
	right := left + popupWidth - 1
	bottom := top + settingsPopupHeight - 1
	for row := top; row <= bottom; row++ {
		for column := left; column <= right; column++ {
			screen.SetContent(column, row, ' ', nil, tcell.StyleDefault)
		}
	}
	drawHorizontal(screen, left+1, right-1, top, '─')
	drawHorizontal(screen, left+1, right-1, bottom, '─')
	drawVertical(screen, top+1, bottom-1, left, '│')
	drawVertical(screen, top+1, bottom-1, right, '│')
	setRune(screen, left, top, '┌')
	setRune(screen, right, top, '┐')
	setRune(screen, left, bottom, '└')
	setRune(screen, right, bottom, '┘')
	for row, line := range lines {
		style := tcell.StyleDefault
		if row == 0 {
			style = style.Bold(true)
		}
		putText(screen, left+2, top+1+row, right-1, line, style)
	}
}

func enabledLabel(value bool) string {
	if value {
		return "enabled"
	}
	return "disabled"
}

func yesNoLabel(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
