package tui

import (
	"strconv"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

const (
	maxRecentEmoji      = 10
	emojiCellWidth      = 4
	emojiPopupMaxWidth  = 42
	emojiPopupMaxHeight = 12
	emojiFallback       = "□"
)

type emojiCategory struct {
	name   string
	values []string
}

var emojiCategories = [...]emojiCategory{
	{name: "Smileys", values: []string{
		"😀", "😃", "😄", "😁", "😆", "😅", "😂", "🙂",
		"🙃", "😉", "😊", "🥰", "😍", "🤔", "😭", "😡",
	}},
	{name: "People", values: []string{
		"👍", "👎", "👌", "✌️", "🤞", "🙏", "👏", "💪",
		"👍🏽", "🫶", "🤝", "👋", "🙋", "🧑‍💻", "👨‍👩‍👧‍👦", "🧑‍🤝‍🧑",
	}},
	{name: "Animals", values: []string{
		"🐶", "🐱", "🐭", "🐹", "🐰", "🦊", "🐻", "🐼",
		"🐨", "🐯", "🦁", "🐸", "🐵", "🐧", "🦋", "🐙",
	}},
	{name: "Food", values: []string{
		"🍎", "🍊", "🍋", "🍉", "🍇", "🍓", "🍒", "🥑",
		"🍕", "🍔", "🍜", "🍣", "🍰", "🎂", "☕", "🥤",
	}},
	{name: "Activities", values: []string{
		"⚽", "🏀", "🏈", "⚾", "🎾", "🏐", "🏓", "🏸",
		"🚲", "🏊", "🎮", "🎯", "🎵", "🎸", "🎨", "🎉",
	}},
	{name: "Travel", values: []string{
		"🚗", "🚕", "🚌", "🚎", "🏎️", "🚓", "🚑", "🚒",
		"🚆", "✈️", "🚀", "🚲", "⛵", "🏠", "🏔️", "🏖️",
	}},
	{name: "Objects", values: []string{
		"⌚", "📱", "💻", "⌨️", "🖥️", "📷", "💡", "🔦",
		"📚", "✏️", "📌", "🔑", "🔒", "🎁", "🧭", "🛠️",
	}},
	{name: "Symbols", values: []string{
		"❤️", "🧡", "💛", "💚", "💙", "💜", "🖤", "🤍",
		"🔥", "⭐", "✅", "❌", "💯", "♻️", "⚠️", "❓",
	}},
	{name: "Flags", values: []string{
		"🇦🇹", "🇩🇪", "🇫🇷", "🇮🇹", "🇪🇸", "🇬🇧", "🇺🇸", "🇨🇦",
		"🇯🇵", "🇮🇳", "🇧🇷", "🇦🇺", "🇳🇿", "🇺🇦", "🇪🇺", "🏳️‍🌈",
	}},
}

type emojiPickerFocus uint8

const (
	emojiFocusCategory emojiPickerFocus = iota
	emojiFocusRecent
)

type emojiPickerState struct {
	open           bool
	focus          emojiPickerFocus
	category       int
	categoryCursor int
	recentCursor   int
	recent         [maxRecentEmoji]string
	recentCount    int
}

func (picker *emojiPickerState) prepareOpen() {
	picker.clamp()
	picker.open = true
	if picker.recentCount > 0 {
		picker.focus = emojiFocusRecent
		picker.recentCursor = 0
	} else {
		picker.focus = emojiFocusCategory
	}
}

func (picker *emojiPickerState) close() {
	picker.open = false
	picker.clamp()
}

func (picker *emojiPickerState) selectedEmoji() string {
	picker.clamp()
	if picker.focus == emojiFocusRecent && picker.recentCount > 0 {
		return picker.recent[picker.recentCursor]
	}
	values := emojiCategories[picker.category].values
	if len(values) == 0 {
		return ""
	}
	return values[picker.categoryCursor]
}

func (picker *emojiPickerState) moveHorizontal(delta int) bool {
	picker.clamp()
	if delta == 0 {
		return false
	}
	if picker.focus == emojiFocusRecent && picker.recentCount > 0 {
		next := clampIndex(picker.recentCursor+delta, picker.recentCount)
		if next == picker.recentCursor {
			return false
		}
		picker.recentCursor = next
		return true
	}
	values := emojiCategories[picker.category].values
	next := clampIndex(picker.categoryCursor+delta, len(values))
	if next == picker.categoryCursor {
		return false
	}
	picker.categoryCursor = next
	return true
}

func (picker *emojiPickerState) moveVertical(delta, columns int) bool {
	picker.clamp()
	if delta == 0 {
		return false
	}
	if columns < 1 {
		columns = 1
	}
	if picker.focus == emojiFocusRecent {
		if delta < 0 {
			return false
		}
		picker.focus = emojiFocusCategory
		return true
	}
	if delta < 0 && picker.categoryCursor < columns && picker.recentCount > 0 {
		picker.focus = emojiFocusRecent
		return true
	}
	values := emojiCategories[picker.category].values
	next := clampIndex(picker.categoryCursor+delta*columns, len(values))
	if next == picker.categoryCursor {
		return false
	}
	picker.categoryCursor = next
	return true
}

func (picker *emojiPickerState) moveCategory(delta int) bool {
	picker.clamp()
	if len(emojiCategories) == 0 || delta == 0 {
		return false
	}
	next := picker.category + delta
	if next < 0 {
		next = len(emojiCategories) - 1
	}
	if next >= len(emojiCategories) {
		next = 0
	}
	if next == picker.category {
		return false
	}
	picker.category = next
	picker.categoryCursor = 0
	picker.focus = emojiFocusCategory
	picker.clamp()
	return true
}

func (picker *emojiPickerState) remember(value string) {
	if !validEmojiPreference(value) {
		return
	}
	existing := -1
	for index := 0; index < picker.recentCount; index++ {
		if picker.recent[index] == value {
			existing = index
			break
		}
	}
	if existing >= 0 {
		copy(picker.recent[1:existing+1], picker.recent[:existing])
		picker.recent[0] = value
		picker.recentCursor = 0
		return
	}
	if picker.recentCount < len(picker.recent) {
		picker.recentCount++
	}
	copy(picker.recent[1:picker.recentCount], picker.recent[:picker.recentCount-1])
	picker.recent[0] = value
	picker.recentCursor = 0
}

func (picker *emojiPickerState) clamp() {
	if picker.recentCount < 0 {
		picker.recentCount = 0
	}
	if picker.recentCount > len(picker.recent) {
		picker.recentCount = len(picker.recent)
	}
	if picker.category < 0 {
		picker.category = 0
	}
	if picker.category >= len(emojiCategories) {
		picker.category = len(emojiCategories) - 1
	}
	picker.recentCursor = clampIndex(picker.recentCursor, picker.recentCount)
	picker.categoryCursor = clampIndex(picker.categoryCursor, len(emojiCategories[picker.category].values))
	if picker.focus == emojiFocusRecent && picker.recentCount == 0 {
		picker.focus = emojiFocusCategory
	}
}

func clampIndex(index, count int) int {
	if count <= 0 || index < 0 {
		return 0
	}
	if index >= count {
		return count - 1
	}
	return index
}

func validEmojiPreference(value string) bool {
	return value != "" && utf8.ValidString(value) && uniseg.GraphemeClusterCount(value) == 1
}

type emojiPickerLayout struct {
	left, top, right, bottom int
	columns, gridRows        int
	compact                  bool
}

func emojiLayout(width, height int) emojiPickerLayout {
	if width < 24 || height < 7 {
		return emojiPickerLayout{compact: true, columns: 1}
	}
	popupWidth := width
	if popupWidth > emojiPopupMaxWidth {
		popupWidth = emojiPopupMaxWidth
	}
	if width > emojiPopupMaxWidth && popupWidth > width-2 {
		popupWidth = width - 2
	}
	popupHeight := height
	if popupHeight > emojiPopupMaxHeight {
		popupHeight = emojiPopupMaxHeight
	}
	if height > emojiPopupMaxHeight && popupHeight > height-2 {
		popupHeight = height - 2
	}
	left := (width - popupWidth) / 2
	top := (height - popupHeight) / 2
	columns := (popupWidth - 4) / emojiCellWidth
	if columns < 1 {
		columns = 1
	}
	return emojiPickerLayout{
		left: left, top: top, right: left + popupWidth - 1, bottom: top + popupHeight - 1,
		columns: columns, gridRows: popupHeight - 5,
	}
}

func emojiGridColumnsForSize(width, height int) int {
	return emojiLayout(width, height).columns
}

func drawEmojiPicker(screen tcell.Screen, model *viewModel, width, height int) {
	screen.HideCursor()
	model.emojiPicker.clamp()
	layout := emojiLayout(width, height)
	if layout.compact {
		drawCompactEmojiPicker(screen, width, height)
		return
	}
	clearEmojiRegion(screen, layout.left, layout.top, layout.right+1, layout.bottom+1)
	drawHorizontal(screen, layout.left+1, layout.right-1, layout.top, '─')
	drawHorizontal(screen, layout.left+1, layout.right-1, layout.bottom, '─')
	drawVertical(screen, layout.top+1, layout.bottom-1, layout.left, '│')
	drawVertical(screen, layout.top+1, layout.bottom-1, layout.right, '│')
	setRune(screen, layout.left, layout.top, '┌')
	setRune(screen, layout.right, layout.top, '┐')
	setRune(screen, layout.left, layout.bottom, '└')
	setRune(screen, layout.right, layout.bottom, '┘')
	putText(screen, layout.left+2, layout.top, layout.right-1, " Emoji ", tcell.StyleDefault.Bold(true))

	contentLeft := layout.left + 2
	contentRight := layout.right - 1
	recentX := contentLeft + len("Recent: ")
	putText(screen, contentLeft, layout.top+1, recentX, "Recent: ", tcell.StyleDefault.Bold(true))
	if model.emojiPicker.recentCount == 0 {
		putText(screen, recentX, layout.top+1, contentRight, "(none)", tcell.StyleDefault.Dim(true))
	} else {
		drawEmojiRowWindow(screen, model.emojiPicker.recent[:model.emojiPicker.recentCount],
			model.emojiPicker.recentCursor, model.emojiPicker.focus == emojiFocusRecent,
			recentX, layout.top+1, contentRight)
	}

	category := emojiCategories[model.emojiPicker.category]
	categoryLabel := category.name + "  " + strconv.Itoa(model.emojiPicker.category+1) + "/" + strconv.Itoa(len(emojiCategories)) + "  Tab category"
	putText(screen, contentLeft, layout.top+2, contentRight, categoryLabel, tcell.StyleDefault.Bold(true))
	drawEmojiCategoryGrid(screen, category.values, model.emojiPicker.categoryCursor,
		model.emojiPicker.focus == emojiFocusCategory, contentLeft, layout.top+3,
		contentRight, layout.gridRows, layout.columns)
	footer := "arrows  Tab category  Enter  Esc close"
	if contentRight-contentLeft < uniseg.StringWidth(footer) {
		footer = "arrows  Tab category  Enter  Esc"
	}
	putText(screen, contentLeft, layout.bottom-1, contentRight, footer, tcell.StyleDefault.Dim(true))
}

func drawEmojiRowWindow(screen tcell.Screen, values []string, selected int, active bool, x, y, limit int) {
	clearEmojiRegion(screen, x, y, limit, y+1)
	capacity := (limit - x) / emojiCellWidth
	if capacity < 1 {
		return
	}
	selected = clampIndex(selected, len(values))
	start := 0
	if selected >= capacity {
		start = selected - capacity + 1
	}
	end := start + capacity
	if end > len(values) {
		end = len(values)
	}
	for index := start; index < end; index++ {
		drawEmojiCell(screen, values[index], active && selected == index,
			x+(index-start)*emojiCellWidth, y, limit)
	}
}

func drawEmojiCategoryGrid(screen tcell.Screen, values []string, selected int, active bool, x, y, limit, rows, columns int) {
	if rows < 1 || columns < 1 {
		return
	}
	clearEmojiRegion(screen, x, y, limit, y+rows)
	selected = clampIndex(selected, len(values))
	startRow := 0
	if selected/columns >= rows {
		startRow = selected/columns - rows + 1
	}
	start := startRow * columns
	end := start + rows*columns
	if end > len(values) {
		end = len(values)
	}
	for index := start; index < end; index++ {
		position := index - start
		drawEmojiCell(screen, values[index], active && selected == index,
			x+(position%columns)*emojiCellWidth, y+position/columns, limit)
	}
}

func clearEmojiRegion(screen tcell.Screen, left, top, right, bottom int) {
	for y := top; y < bottom; y++ {
		for x := left; x < right; x++ {
			screen.SetContent(x, y, ' ', nil, tcell.StyleDefault)
		}
	}
}

func drawEmojiCell(screen tcell.Screen, value string, selected bool, x, y, limit int) {
	style := tcell.StyleDefault
	if selected {
		style = style.Bold(true).Reverse(true)
	}
	for offset := 0; offset < emojiCellWidth && x+offset < limit; offset++ {
		screen.SetContent(x+offset, y, ' ', nil, style)
	}
	rendered := value
	displayWidth := uniseg.StringWidth(rendered)
	if !validEmojiPreference(rendered) || displayWidth < 1 || displayWidth >= emojiCellWidth {
		rendered = emojiFallback
		displayWidth = 1
	}
	// Keep the grapheme within the first three columns. Some terminals expose a
	// continuation marker when a wide grapheme starts after a styled blank.
	contentX := x + (emojiCellWidth-1-displayWidth)/2
	rest, width := screen.Put(contentX, y, rendered, style)
	if rest == "" && width == displayWidth {
		return
	}
	for offset := 0; offset < emojiCellWidth && x+offset < limit; offset++ {
		screen.SetContent(x+offset, y, ' ', nil, style)
	}
	screen.SetContent(x+(emojiCellWidth-1)/2, y, '□', nil, style)
}

func drawCompactEmojiPicker(screen tcell.Screen, width, height int) {
	screen.Clear()
	if width <= 0 || height <= 0 {
		return
	}
	putText(screen, 0, 0, width, "Emoji", tcell.StyleDefault.Bold(true))
	if height > 2 {
		putText(screen, 0, 2, width, "terminal too small", tcell.StyleDefault)
	}
	if height > 4 {
		putText(screen, 0, height-2, width, "Esc close", tcell.StyleDefault.Dim(true))
	}
}
