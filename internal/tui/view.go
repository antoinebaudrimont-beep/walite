package tui

import (
	"strconv"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
)

const (
	narrowWidth = 70
	shortHeight = 8
	maxChats    = 4
	maxMessages = 32
	prefixWidth = 7
)

type messageView struct {
	time string
	text string
}

type chatView struct {
	title        string
	messages     [maxMessages]messageView
	messageCount int
	unreadCount  uint16
}

type viewModel struct {
	chats        [maxChats]chatView
	chatCount    int
	selectedChat int
	scrollOffset int
	mode         inputMode
	composer     composerState
}

func defaultDemoView() viewModel {
	model := viewModel{
		chats: [maxChats]chatView{
			newDemoChat("Demo Chat", "Demo", 18),
			newDemoChat("Project Room", "Project", 15),
			newDemoChat("Family Demo", "Family-demo", 12),
			newDemoChat("Test Contact", "Test-contact", 10),
		},
		chatCount: maxChats,
	}
	model.chats[0].messages[0] = messageView{time: "09:42", text: "Synthetic message one"}
	model.chats[0].messages[1] = messageView{time: "09:45", text: "Synthetic reply"}
	model.chats[0].messages[2] = messageView{time: "09:47", text: "Synthetic terminal preview"}
	model.chats[0].messages[3] = messageView{time: "09:49", text: "Synthetic wrapping preview that stays bounded while demonstrating a longer conversation line"}
	model.chats[1].unreadCount = 3
	model.chats[2].unreadCount = 1
	model.chats[3].unreadCount = 12
	return model
}

func newDemoChat(title, label string, count int) chatView {
	chat := chatView{title: title, messageCount: count}
	for index := 0; index < count; index++ {
		chat.messages[index] = messageView{
			time: "10:" + twoDigits(index),
			text: label + " synthetic message " + strconv.Itoa(index+1),
		}
	}
	return chat
}

func twoDigits(value int) string {
	if value < 10 {
		return "0" + strconv.Itoa(value)
	}
	return strconv.Itoa(value)
}

func draw(screen tcell.Screen, model *viewModel) {
	width, height := screen.Size()
	screen.Clear()
	screen.HideCursor()
	if width <= 0 || height <= 0 {
		return
	}
	if height < shortHeight {
		drawCompact(screen, model, width, height)
		return
	}
	if width < narrowWidth {
		drawNarrow(screen, model, width, height)
		return
	}
	drawTwoPane(screen, model, width, height)
}

func drawCompact(screen tcell.Screen, model *viewModel, width, height int) {
	putText(screen, 0, 0, width, title, tcell.StyleDefault.Bold(true))
	if height > 2 {
		putText(screen, 0, 2, width, "terminal too small", tcell.StyleDefault)
	}
	if height > 4 {
		escapeAction := "Esc quit"
		if model.mode == modeCompose {
			escapeAction = "Esc cancel"
		}
		putText(screen, 0, height-2, width, escapeAction, tcell.StyleDefault.Dim(true))
	}
}

func drawNarrow(screen tcell.Screen, model *viewModel, width, height int) {
	chat := &model.chats[model.selectedChat]
	composeSeparator := height - 3
	putText(screen, 0, 0, width, title, tcell.StyleDefault.Bold(true))
	putText(screen, 0, 2, width, chat.title, tcell.StyleDefault.Bold(true))
	start, end := visibleMessageRange(model, width, height)
	drawMessages(screen, chat, start, end, 0, 4, width, composeSeparator)
	drawHorizontal(screen, 0, width-1, composeSeparator, '─')
	drawComposer(screen, model, 0, height-2, width)
	footer := "j/k select  PgUp/PgDn scroll  Enter compose  Esc quit"
	if model.mode == modeCompose {
		footer = "Enter send  Esc cancel  ←/→ move"
	}
	putText(screen, 0, height-1, width, footer, tcell.StyleDefault.Dim(true))
}

func drawTwoPane(screen tcell.Screen, model *viewModel, width, height int) {
	footerTop := height - 3
	composeSeparator := footerTop - 2
	composeRow := footerTop - 1
	separator := paneSeparator(width)

	drawHorizontal(screen, 1, width-2, 0, '─')
	drawHorizontal(screen, 1, width-2, footerTop, '─')
	drawHorizontal(screen, 1, width-2, height-1, '─')
	drawVertical(screen, 1, footerTop-1, 0, '│')
	drawVertical(screen, 1, footerTop-1, separator, '│')
	drawVertical(screen, 1, footerTop-1, width-1, '│')
	drawVertical(screen, footerTop+1, height-2, 0, '│')
	drawVertical(screen, footerTop+1, height-2, width-1, '│')

	setRune(screen, 0, 0, '┌')
	setRune(screen, separator, 0, '┬')
	setRune(screen, width-1, 0, '┐')
	setRune(screen, 0, footerTop, '├')
	setRune(screen, separator, footerTop, '┴')
	setRune(screen, width-1, footerTop, '┤')
	drawHorizontal(screen, separator+1, width-2, composeSeparator, '─')
	setRune(screen, separator, composeSeparator, '├')
	setRune(screen, width-1, composeSeparator, '┤')
	setRune(screen, 0, height-1, '└')
	setRune(screen, width-1, height-1, '┘')

	chat := &model.chats[model.selectedChat]
	putText(screen, 2, 1, separator-2, title, tcell.StyleDefault.Bold(true))
	putText(screen, separator+2, 1, width-2, chat.title, tcell.StyleDefault.Bold(true))

	chatY := 3
	for index := 0; index < model.chatCount && chatY+index < footerTop; index++ {
		y := chatY + index
		chat := &model.chats[index]
		selected := index == model.selectedChat
		style := tcell.StyleDefault.Bold(chat.unreadCount > 0).Reverse(selected)
		if selected {
			for x := 1; x < separator; x++ {
				screen.SetContent(x, y, ' ', nil, style)
			}
		}
		label := formatChatRow(chat.title, chat.unreadCount, selected, separator-3)
		putText(screen, 2, y, separator-1, label, style)
	}

	start, end := visibleMessageRange(model, width, height)
	drawMessages(screen, chat, start, end, separator+2, 3, width-2, composeSeparator)
	drawComposer(screen, model, separator+2, composeRow, width-2)
	footer := "↑/↓ j/k select  PgUp/PgDn scroll  Enter compose  Esc quit"
	if model.mode == modeCompose {
		footer = "Enter send demo  Esc cancel  ←/→ move  Backspace delete"
	}
	putText(screen, 2, height-2, width-2, footer, tcell.StyleDefault.Dim(true))
}

func drawComposer(screen tcell.Screen, model *viewModel, x, y, limit int) {
	if x >= limit {
		return
	}
	putText(screen, x, y, limit, "> ", tcell.StyleDefault)
	inputX := x + 2
	if model.mode != modeCompose {
		putText(screen, inputX, y, limit, "Write a message…", tcell.StyleDefault.Dim(true))
		return
	}
	model.composer.normalize()
	available := limit - inputX
	start, end := visibleDraftSpan(&model.composer, available)
	visible := string(model.composer.data[start:end])
	cursorX := inputX
	draftCursorX := -1
	position := start
	for visible != "" && cursorX < limit {
		if position == model.composer.cursor {
			draftCursorX = cursorX
		}
		rest, width := screen.Put(cursorX, y, visible, tcell.StyleDefault)
		if width <= 0 || rest == visible {
			break
		}
		consumed := len(visible) - len(rest)
		position += consumed
		cursorX += width
		visible = rest
	}
	if position == model.composer.cursor {
		draftCursorX = cursorX
	}
	if draftCursorX < inputX {
		draftCursorX = inputX
	}
	if draftCursorX >= limit {
		draftCursorX = limit - 1
	}
	screen.ShowCursor(draftCursorX, y)
}

func visibleDraftSpan(composer *composerState, width int) (int, int) {
	composer.normalize()
	if width <= 0 {
		return composer.cursor, composer.cursor
	}
	after := runeCountForward(composer.data[composer.cursor:composer.length], width/2)
	beforeLimit := width - 1 - after
	if beforeLimit < 0 {
		beforeLimit = 0
	}
	start := moveBytesLeft(composer.data[:composer.cursor], composer.cursor, beforeLimit)
	used := utf8.RuneCount(composer.data[start:composer.cursor])
	end := moveBytesRight(composer.data[:composer.length], composer.cursor, width-used)
	return start, end
}

func runeCountForward(data []byte, limit int) int {
	count := 0
	for len(data) > 0 && count < limit {
		_, size := utf8.DecodeRune(data)
		data = data[size:]
		count++
	}
	return count
}

func moveBytesLeft(data []byte, cursor, count int) int {
	for cursor > 0 && count > 0 {
		_, size := utf8.DecodeLastRune(data[:cursor])
		cursor -= size
		count--
	}
	return cursor
}

func moveBytesRight(data []byte, cursor, count int) int {
	for cursor < len(data) && count > 0 {
		_, size := utf8.DecodeRune(data[cursor:])
		cursor += size
		count--
	}
	return cursor
}

func drawMessages(screen tcell.Screen, chat *chatView, start, end, x, y, limit, bottom int) {
	for index := start; index < end && y < bottom; index++ {
		y += drawMessage(screen, chat.messages[index], x, y, limit, bottom)
	}
}

func drawMessage(screen tcell.Screen, message messageView, x, y, limit, bottom int) int {
	if y >= bottom || x >= limit {
		return 0
	}
	width := limit - x
	bodyX := x
	if width > prefixWidth {
		putText(screen, x, y, x+5, message.time, tcell.StyleDefault)
		bodyX += prefixWidth
	}
	remaining := message.text
	rows := 0
	for remaining != "" && y+rows < bottom {
		line, rest := nextWrappedLine(remaining, limit-bodyX)
		putText(screen, bodyX, y+rows, limit, line, tcell.StyleDefault)
		remaining = rest
		rows++
	}
	if rows == 0 {
		return 1
	}
	return rows
}

func visibleMessageRange(model *viewModel, width, height int) (int, int) {
	if model.chatCount == 0 || model.selectedChat < 0 || model.selectedChat >= model.chatCount {
		return 0, 0
	}
	chat := &model.chats[model.selectedChat]
	messageWidth, rows := conversationViewport(width, height)
	if chat.messageCount == 0 || messageWidth <= 0 || rows <= 0 {
		return 0, 0
	}
	offset := model.scrollOffset
	maximum := maximumScrollOffset(model, width, height)
	if offset < 0 {
		offset = 0
	}
	if offset > maximum {
		offset = maximum
	}
	end := chat.messageCount - offset
	start := end
	used := 0
	for start > 0 {
		lines := wrappedMessageLines(chat.messages[start-1], messageWidth)
		if used > 0 && used+lines > rows {
			break
		}
		start--
		used += lines
		if used >= rows {
			break
		}
	}
	return start, end
}

func maximumScrollOffset(model *viewModel, width, height int) int {
	if model.chatCount == 0 || model.selectedChat < 0 || model.selectedChat >= model.chatCount {
		return 0
	}
	chat := &model.chats[model.selectedChat]
	messageWidth, rows := conversationViewport(width, height)
	if chat.messageCount == 0 || messageWidth <= 0 || rows <= 0 {
		return 0
	}
	end := 0
	used := 0
	for end < chat.messageCount {
		lines := wrappedMessageLines(chat.messages[end], messageWidth)
		if used > 0 && used+lines > rows {
			break
		}
		end++
		used += lines
		if used >= rows {
			break
		}
	}
	return chat.messageCount - end
}

func wrappedMessageLines(message messageView, width int) int {
	if width <= 0 {
		return 0
	}
	bodyWidth := width
	if width > prefixWidth {
		bodyWidth -= prefixWidth
	}
	if bodyWidth <= 0 {
		bodyWidth = 1
	}
	if message.text == "" {
		return 1
	}
	lines := 0
	remaining := message.text
	for remaining != "" {
		_, remaining = nextWrappedLine(remaining, bodyWidth)
		lines++
	}
	return lines
}

func nextWrappedLine(value string, width int) (string, string) {
	if value == "" || width <= 0 {
		return "", ""
	}
	if utf8.RuneCountInString(value) <= width {
		return value, ""
	}
	cut := len(value)
	lastSpace := -1
	runes := 0
	for index, character := range value {
		if runes == width {
			cut = index
			break
		}
		if character == ' ' {
			lastSpace = index
		}
		runes++
	}
	if lastSpace > 0 && lastSpace < cut {
		cut = lastSpace
	}
	rest := value[cut:]
	for len(rest) > 0 && rest[0] == ' ' {
		rest = rest[1:]
	}
	return value[:cut], rest
}

func conversationViewport(width, height int) (int, int) {
	if height < shortHeight || width <= 0 {
		return 0, 0
	}
	if width < narrowWidth {
		return width, height - 7
	}
	separator := paneSeparator(width)
	return width - separator - 4, height - 8
}

func paneSeparator(width int) int {
	separator := width / 3
	if separator < 22 {
		separator = 22
	}
	if maximum := width - 42; separator > maximum {
		separator = maximum
	}
	return separator
}

func putText(screen tcell.Screen, x, y, limit int, value string, style tcell.Style) {
	for value != "" && x < limit {
		rest, width := screen.Put(x, y, value, style)
		if width <= 0 || rest == value {
			return
		}
		x += width
		value = rest
	}
}

func drawHorizontal(screen tcell.Screen, from, to, y int, character rune) {
	for x := from; x <= to; x++ {
		setRune(screen, x, y, character)
	}
}

func drawVertical(screen tcell.Screen, from, to, x int, character rune) {
	for y := from; y <= to; y++ {
		setRune(screen, x, y, character)
	}
}

func setRune(screen tcell.Screen, x, y int, character rune) {
	screen.SetContent(x, y, character, nil, tcell.StyleDefault)
}
