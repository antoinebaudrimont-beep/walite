package tui

import (
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

const (
	narrowWidth = 70
	shortHeight = 8
	maxChats    = 4
	maxMessages = 32
	prefixWidth = 7
)

type messageView struct {
	id        messageID
	time      string
	text      string
	replyToID messageID
	hasReply  bool
}

type chatView struct {
	title        string
	messages     [maxMessages]messageView
	messageCount int
	unreadCount  uint16
	activity     uint64
}

type viewModel struct {
	chats           [maxChats]chatView
	chatCount       int
	selectedChat    int
	scrollOffset    int
	mode            inputMode
	composer        composerState
	emojiPicker     emojiPickerState
	replySelect     replySelectionState
	replyTarget     replyTarget
	nextMessageID   messageID
	nextActivity    uint64
	preferencesPath string
}

func defaultDemoView() viewModel {
	model := viewModel{
		chats: [maxChats]chatView{
			newDemoChat("Demo Chat", "Demo", 18),
			newDemoChat("Project Room", "Project", 15),
			newDemoChat("Family Demo", "Family-demo", 12),
			newDemoChat("Test Contact", "Test-contact", 10),
		},
		chatCount:    maxChats,
		nextActivity: maxChats + 1,
	}
	for index := 0; index < model.chatCount; index++ {
		model.chats[index].activity = uint64(model.chatCount - index)
	}
	model.chats[0].messages[0] = messageView{time: "09:42", text: "Synthetic message one"}
	model.chats[0].messages[1] = messageView{time: "09:45", text: "Synthetic reply"}
	model.chats[0].messages[2] = messageView{time: "09:47", text: "Synthetic terminal preview"}
	model.chats[0].messages[3] = messageView{time: "09:49", text: "Synthetic wrapping preview that stays bounded while demonstrating a longer conversation line"}
	model.chats[1].unreadCount = 3
	model.chats[2].unreadCount = 1
	model.chats[3].unreadCount = 12
	model.assignMessageIDs()
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
	} else if width < narrowWidth {
		drawNarrow(screen, model, width, height)
	} else {
		drawTwoPane(screen, model, width, height)
	}
	if model.emojiPicker.open {
		drawEmojiPicker(screen, model, width, height)
	}
}

func drawCompact(screen tcell.Screen, model *viewModel, width, height int) {
	putText(screen, 0, 0, width, title, tcell.StyleDefault.Bold(true))
	if height > 2 {
		putText(screen, 0, 2, width, "terminal too small", tcell.StyleDefault)
	}
	if height > 4 {
		escapeAction := "Esc quit"
		if model.emojiPicker.open {
			escapeAction = "Esc close"
		} else if model.mode == modeCompose || model.replySelect.valid {
			escapeAction = "Esc cancel"
		}
		putText(screen, 0, height-2, width, escapeAction, tcell.StyleDefault.Dim(true))
	}
}

func drawNarrow(screen tcell.Screen, model *viewModel, width, height int) {
	chat := &model.chats[model.selectedChat]
	replyRows := 0
	if model.replyTarget.valid {
		replyRows = 1
	}
	composeSeparator := height - 3 - replyRows
	putText(screen, 0, 0, width, title, tcell.StyleDefault.Bold(true))
	putText(screen, 0, 2, width, chat.title, tcell.StyleDefault.Bold(true))
	start, end := visibleMessageRange(model, width, height)
	drawMessages(screen, model, chat, start, end, 0, 4, width, composeSeparator)
	drawHorizontal(screen, 0, width-1, composeSeparator, '─')
	if replyRows > 0 {
		drawReplyPreview(screen, model, chat, 0, composeSeparator+1, width, replyRows)
	}
	drawComposer(screen, model, 0, height-2, width)
	footer := navigationFooter(model, true)
	if model.replySelect.valid {
		footer = navigationFooter(model, true)
	} else if model.mode == modeCompose {
		footer = "Enter send  Ctrl-R reply  Ctrl-E emoji  Esc cancel"
	}
	putText(screen, 0, height-1, width, footer, tcell.StyleDefault.Dim(true))
}

func drawTwoPane(screen tcell.Screen, model *viewModel, width, height int) {
	footerTop := height - 3
	replyRows := 0
	if model.replyTarget.valid {
		replyRows = 2
	}
	composeSeparator := footerTop - 2 - replyRows
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
	drawMessages(screen, model, chat, start, end, separator+2, 3, width-2, composeSeparator)
	if replyRows > 0 {
		drawReplyPreview(screen, model, chat, separator+2, composeSeparator+1, width-2, replyRows)
	}
	drawComposer(screen, model, separator+2, composeRow, width-2)
	footer := navigationFooter(model, false)
	if model.replySelect.valid {
		footer = navigationFooter(model, false)
	} else if model.mode == modeCompose {
		footer = "Enter send  Ctrl-R reply  Ctrl-E emoji  Esc cancel"
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
	start, end, cursorOffset := visibleDraftSpan(&model.composer, available)
	putText(screen, inputX, y, limit, string(model.composer.data[start:end]), tcell.StyleDefault)
	if model.replySelect.valid {
		return
	}
	draftCursorX := inputX + cursorOffset
	if draftCursorX < inputX {
		draftCursorX = inputX
	}
	if draftCursorX >= limit {
		draftCursorX = limit - 1
	}
	screen.ShowCursor(draftCursorX, y)
}

func navigationFooter(model *viewModel, narrow bool) string {
	if model.replySelect.valid {
		escape := "Esc clear"
		if model.replySelect.fromCompose {
			escape = "Esc cancel"
		}
		if narrow {
			return "↑/↓ messages  Enter reply  " + escape
		}
		return "↑/↓ messages  Enter reply  " + escape
	}
	if narrow {
		return "↑/↓ messages  Enter compose  Esc quit"
	}
	return "↑/↓ messages  j/k chats  Enter compose  Esc quit"
}

func visibleDraftSpan(composer *composerState, width int) (start, end, cursorCells int) {
	composer.normalize()
	if width <= 0 {
		return composer.cursor, composer.cursor, 0
	}
	data := composer.data[:composer.length]
	position := 0
	state := -1
	for position < composer.cursor {
		cluster, _, clusterWidth, nextState := uniseg.FirstGraphemeCluster(data[position:], state)
		position += len(cluster)
		cursorCells += clusterWidth
		state = nextState
	}
	start = 0
	state = -1
	for cursorCells > width-1 && start < composer.cursor {
		cluster, _, clusterWidth, nextState := uniseg.FirstGraphemeCluster(data[start:], state)
		start += len(cluster)
		cursorCells -= clusterWidth
		state = nextState
	}
	end = start
	used := 0
	for end < len(data) {
		cluster, _, clusterWidth, nextState := uniseg.FirstGraphemeCluster(data[end:], state)
		if used+clusterWidth > width {
			break
		}
		end += len(cluster)
		used += clusterWidth
		state = nextState
	}
	return start, end, cursorCells
}

func drawReplyPreview(screen tcell.Screen, model *viewModel, chat *chatView, x, y, limit, rows int) {
	reference := "original message unavailable"
	if original, ok := findMessageByID(chat, model.replyTarget.id); ok {
		reference = original.time + "  " + original.text
	}
	if rows == 1 {
		putText(screen, x, y, limit, truncateDisplayWidth("Replying to: "+reference, limit-x), tcell.StyleDefault.Bold(true))
		return
	}
	putText(screen, x, y, limit, "Replying to:", tcell.StyleDefault.Bold(true))
	putText(screen, x, y+1, limit, truncateDisplayWidth(reference, limit-x), tcell.StyleDefault)
}

func drawMessages(screen tcell.Screen, model *viewModel, chat *chatView, start, end, x, y, limit, bottom int) {
	for index := start; index < end && y < bottom; index++ {
		selected := model.replySelect.valid && model.replySelect.index == index
		y += drawMessage(screen, chat, chat.messages[index], x, y, limit, bottom, selected)
	}
}

func drawMessage(screen tcell.Screen, chat *chatView, message messageView, x, y, limit, bottom int, selected bool) int {
	if y >= bottom || x >= limit {
		return 0
	}
	style := tcell.StyleDefault.Reverse(selected)
	width := limit - x
	bodyX := x
	if width > prefixWidth {
		bodyX += prefixWidth
	}
	rows := 0
	if message.hasReply && y+rows < bottom {
		fillMessageRow(screen, x, y+rows, limit, style)
		if width > prefixWidth {
			putText(screen, x, y+rows, x+5, message.time, style)
		}
		reference := "↪ original message unavailable"
		if original, ok := findMessageByID(chat, message.replyToID); ok {
			reference = "↪ " + original.time + " " + original.text
		}
		putText(screen, bodyX, y+rows, limit, truncateDisplayWidth(reference, limit-bodyX), style)
		rows++
	}
	remaining := message.text
	for remaining != "" && y+rows < bottom {
		fillMessageRow(screen, x, y+rows, limit, style)
		if rows == 0 && width > prefixWidth {
			putText(screen, x, y+rows, x+5, message.time, style)
		}
		line, rest := nextWrappedLine(remaining, limit-bodyX)
		putText(screen, bodyX, y+rows, limit, line, style)
		remaining = rest
		rows++
	}
	if rows == 0 {
		return 1
	}
	return rows
}

func fillMessageRow(screen tcell.Screen, x, y, limit int, style tcell.Style) {
	for column := x; column < limit; column++ {
		screen.SetContent(column, y, ' ', nil, style)
	}
}

func visibleMessageRange(model *viewModel, width, height int) (int, int) {
	if model.chatCount == 0 || model.selectedChat < 0 || model.selectedChat >= model.chatCount {
		return 0, 0
	}
	chat := &model.chats[model.selectedChat]
	messageWidth, rows := conversationViewport(model, width, height)
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
	messageWidth, rows := conversationViewport(model, width, height)
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
		if message.hasReply {
			return 1
		}
		return 1
	}
	lines := 0
	remaining := message.text
	for remaining != "" {
		_, remaining = nextWrappedLine(remaining, bodyWidth)
		lines++
	}
	if message.hasReply {
		lines++
	}
	return lines
}

func nextWrappedLine(value string, width int) (string, string) {
	if value == "" || width <= 0 {
		return "", ""
	}
	if uniseg.StringWidth(value) <= width {
		return value, ""
	}
	cut := 0
	lastSpace := -1
	used := 0
	graphemes := uniseg.NewGraphemes(value)
	for graphemes.Next() {
		from, to := graphemes.Positions()
		if used+graphemes.Width() > width {
			if cut == 0 {
				cut = to
			}
			break
		}
		used += graphemes.Width()
		cut = to
		if graphemes.Str() == " " {
			lastSpace = from
		}
	}
	if cut >= len(value) {
		return value, ""
	}
	if lastSpace > 0 && lastSpace < cut {
		cut = lastSpace
	}
	rest := strings.TrimLeft(value[cut:], " ")
	return value[:cut], rest
}

func conversationViewport(model *viewModel, width, height int) (int, int) {
	if height < shortHeight || width <= 0 {
		return 0, 0
	}
	if width < narrowWidth {
		rows := height - 7
		if model.replyTarget.valid {
			rows--
		}
		return width, rows
	}
	separator := paneSeparator(width)
	rows := height - 8
	if model.replyTarget.valid {
		rows -= 2
	}
	return width - separator - 4, rows
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
