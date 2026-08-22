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
	maxMessages = 18
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
}

type viewModel struct {
	chats        [maxChats]chatView
	chatCount    int
	selectedChat int
	scrollOffset int
}

func defaultDemoView() viewModel {
	model := viewModel{
		chats: [maxChats]chatView{
			newDemoChat("Demo Chat", "Demo", maxMessages),
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

func draw(screen tcell.Screen, model viewModel) {
	width, height := screen.Size()
	screen.Clear()
	if width <= 0 || height <= 0 {
		return
	}
	if height < shortHeight {
		drawCompact(screen, width, height)
		return
	}
	if width < narrowWidth {
		drawNarrow(screen, model, width, height)
		return
	}
	drawTwoPane(screen, model, width, height)
}

func drawCompact(screen tcell.Screen, width, height int) {
	putText(screen, 0, 0, width, title, tcell.StyleDefault.Bold(true))
	if height > 2 {
		putText(screen, 0, 2, width, "terminal too small", tcell.StyleDefault)
	}
	if height > 4 {
		putText(screen, 0, height-2, width, "Esc quit", tcell.StyleDefault.Dim(true))
	}
}

func drawNarrow(screen tcell.Screen, model viewModel, width, height int) {
	chat := model.chats[model.selectedChat]
	putText(screen, 0, 0, width, title, tcell.StyleDefault.Bold(true))
	putText(screen, 0, 2, width, chat.title, tcell.StyleDefault.Bold(true))
	start, end := visibleMessageRange(model, width, height)
	drawMessages(screen, chat, start, end, 0, 4, width, height-1)
	putText(screen, 0, height-1, width, "j/k select  PgUp/PgDn scroll  Esc quit", tcell.StyleDefault.Dim(true))
}

func drawTwoPane(screen tcell.Screen, model viewModel, width, height int) {
	footerTop := height - 3
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
	setRune(screen, 0, height-1, '└')
	setRune(screen, width-1, height-1, '┘')

	chat := model.chats[model.selectedChat]
	putText(screen, 2, 1, separator-2, title, tcell.StyleDefault.Bold(true))
	putText(screen, separator+2, 1, width-2, chat.title, tcell.StyleDefault.Bold(true))

	chatY := 3
	for index := 0; index < model.chatCount && chatY+index < footerTop; index++ {
		y := chatY + index
		style := tcell.StyleDefault
		if index == model.selectedChat {
			style = style.Reverse(true)
			for x := 1; x < separator; x++ {
				screen.SetContent(x, y, ' ', nil, style)
			}
			putText(screen, 2, y, separator-1, "> "+model.chats[index].title, style)
			continue
		}
		putText(screen, 3, y, separator-1, model.chats[index].title, style)
	}

	start, end := visibleMessageRange(model, width, height)
	drawMessages(screen, chat, start, end, separator+2, 3, width-2, footerTop)
	putText(screen, 2, height-2, width-2, "↑/↓ j/k select  PgUp/Ctrl-U  PgDn/Ctrl-D  Esc quit", tcell.StyleDefault.Dim(true))
}

func drawMessages(screen tcell.Screen, chat chatView, start, end, x, y, limit, bottom int) {
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

func visibleMessageRange(model viewModel, width, height int) (int, int) {
	if model.chatCount == 0 || model.selectedChat < 0 || model.selectedChat >= model.chatCount {
		return 0, 0
	}
	chat := model.chats[model.selectedChat]
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

func maximumScrollOffset(model viewModel, width, height int) int {
	if model.chatCount == 0 || model.selectedChat < 0 || model.selectedChat >= model.chatCount {
		return 0
	}
	chat := model.chats[model.selectedChat]
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
		return width, height - 5
	}
	separator := paneSeparator(width)
	return width - separator - 4, height - 6
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
