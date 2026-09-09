package tui

import (
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

const (
	narrowWidth        = 70
	shortHeight        = 8
	prefixWidth        = 7
	timestampTextWidth = 5
)

type viewModel struct {
	chats                *chatState
	display              displayState
	chatView             chatViewState
	mode                 inputMode
	composer             composerState
	emojiPicker          emojiPickerState
	replySelect          replySelectionState
	replyTarget          replyTarget
	options              Options
	settingsOpen         bool
	settings             settingsState
	quitConfirm          bool
	terminalWidth        int
	terminalHeight       int
	preferencesPath      string
	send                 func(SendRequest) error
	asyncSend            bool
	sendPending          bool
	sendUncertain        bool
	sendStatus           string
	localReadRequest     LocalReadRequest
	readRequest          ReadReceiptRequest
	readIntent           readReceiptIntent
	mediaTarget          mediaTargetState
	media                func(MediaRequest) bool
	closeMedia           func()
	closeExternalPreview func() bool
	loadOlder            func(OlderHistoryRequest) bool
	olderHistory         olderHistoryState
	selectionEpoch       uint64
}

func draw(screen tcell.Screen, model *viewModel) {
	width, height := screen.Size()
	model.terminalWidth = width
	model.terminalHeight = height
	styles := model.styles()
	screen.SetStyle(styles.normal)
	screen.Clear()
	// Clear may retain the previous style for already-blank cells. Refill the
	// bounded terminal grid so a live theme switch repaints the whole frame.
	screen.Fill(' ', styles.normal)
	screen.HideCursor()
	if width <= 0 || height <= 0 {
		return
	}
	clearMessagePane(screen, width, height, styles.normal)
	drawBase(screen, model, width, height)
	if model.sendStatus != "" {
		y := height - 1
		if height < shortHeight || width >= narrowWidth {
			y = height - 2
		}
		if y >= 0 {
			fillMessageRow(screen, 0, y, width, styles.status)
			putText(screen, 0, y, width, model.sendStatus, styles.status)
		}
	}
	if model.emojiPicker.open {
		drawEmojiPicker(screen, model, width, height)
	}
	if model.settingsOpen {
		screen.HideCursor()
		drawSettingsPopup(screen, model, width, height)
	}
	if model.quitConfirm {
		screen.HideCursor()
		drawQuitConfirmation(screen, model, width, height)
	}
}

// clearMessagePane resets the entire conversation interior, including header,
// unused rows, line tails, reply preview, and composer. Clear alone only resets
// tcell's logical contents: Show can skip a physically stale cell that its
// buffer already considers blank. In tcell v2.13.10 unlocking a region also
// marks every cell dirty, forcing those blanks into the same changed-frame
// flush. Walite does not use locked terminal graphics in this region.
func clearMessagePane(screen tcell.Screen, width, height int, style tcell.Style) {
	left, top, right, bottom := 0, 2, width, height-1
	if height < shortHeight {
		left, top, right, bottom = 0, 0, width, height
	} else if width >= narrowWidth {
		left, top, right, bottom = paneSeparator(width)+1, 1, width-1, height-3
	}
	for y := top; y < bottom; y++ {
		fillMessageRow(screen, left, y, right, style)
	}
	screen.LockRegion(left, top, right-left, bottom-top, false)
}

func drawBase(screen tcell.Screen, model *viewModel, width, height int) {
	if height < shortHeight {
		drawCompact(screen, model, width, height)
	} else if width < narrowWidth {
		drawNarrow(screen, model, width, height)
	} else {
		drawTwoPane(screen, model, width, height)
	}
}

func drawCompact(screen tcell.Screen, model *viewModel, width, height int) {
	styles := model.styles()
	putText(screen, 0, 0, width, title, styles.normal.Bold(true))
	if height > 2 {
		putText(screen, 0, 2, width, "terminal too small", styles.warning)
	}
	if height > 4 {
		escapeAction := "Esc quit"
		if model.settingsOpen || model.emojiPicker.open {
			escapeAction = "Esc close"
		} else if model.mode == modeCompose || model.replySelect.valid {
			escapeAction = "Esc cancel"
		}
		putText(screen, 0, height-2, width, escapeAction, styles.status.Dim(true))
	}
}

func drawNarrow(screen tcell.Screen, model *viewModel, width, height int) {
	styles := model.styles()
	putText(screen, 0, 0, width, title, styles.normal.Bold(true))
	selectedIndex := model.chats.selectedIndex()
	chat, ok := model.chats.selectedChat()
	if !ok {
		putText(screen, 0, 2, width, "No chats — syncing…", styles.status.Dim(true))
		putText(screen, 0, height-1, width, narrowNavigationFooter(model, width), styles.status.Dim(true))
		return
	}
	replyRows := 0
	if model.replyTarget.valid {
		replyRows = 1
	}
	composeSeparator := height - 3 - replyRows
	putText(screen, 0, 2, width, chat.title, styles.normal.Bold(true))
	drawNewerMessagesIndicator(screen, model, width, height, 0, 3, width)
	start, end := visibleMessageRange(model, width, height)
	drawMessages(screen, model, selectedIndex, chat, start, end, 0, 4, width, composeSeparator)
	drawHorizontalStyle(screen, 0, width-1, composeSeparator, '─', styles.border)
	if replyRows > 0 {
		drawReplyPreview(screen, model, selectedIndex, 0, composeSeparator+1, width, replyRows)
	}
	drawComposer(screen, model, 0, height-2, width)
	footer := narrowNavigationFooter(model, width)
	if model.replySelect.valid {
		footer = narrowNavigationFooter(model, width)
	} else if model.mode == modeCompose {
		footer = narrowComposeFooter(width)
	}
	putText(screen, 0, height-1, width, footer, styles.status.Dim(true))
}

func drawTwoPane(screen tcell.Screen, model *viewModel, width, height int) {
	styles := model.styles()
	footerTop := height - 3
	replyRows := 0
	if model.replyTarget.valid {
		replyRows = 2
	}
	composeSeparator := footerTop - 2 - replyRows
	composeRow := footerTop - 1
	separator := paneSeparator(width)

	drawHorizontalStyle(screen, 1, width-2, 0, '─', styles.border)
	drawHorizontalStyle(screen, 1, width-2, footerTop, '─', styles.border)
	drawHorizontalStyle(screen, 1, width-2, height-1, '─', styles.border)
	drawVerticalStyle(screen, 1, footerTop-1, 0, '│', styles.border)
	drawVerticalStyle(screen, 1, footerTop-1, separator, '│', styles.border)
	drawVerticalStyle(screen, 1, footerTop-1, width-1, '│', styles.border)
	drawVerticalStyle(screen, footerTop+1, height-2, 0, '│', styles.border)
	drawVerticalStyle(screen, footerTop+1, height-2, width-1, '│', styles.border)

	setRuneStyle(screen, 0, 0, '┌', styles.border)
	setRuneStyle(screen, separator, 0, '┬', styles.border)
	setRuneStyle(screen, width-1, 0, '┐', styles.border)
	setRuneStyle(screen, 0, footerTop, '├', styles.border)
	setRuneStyle(screen, separator, footerTop, '┴', styles.border)
	setRuneStyle(screen, width-1, footerTop, '┤', styles.border)
	drawHorizontalStyle(screen, separator+1, width-2, composeSeparator, '─', styles.border)
	setRuneStyle(screen, separator, composeSeparator, '├', styles.border)
	setRuneStyle(screen, width-1, composeSeparator, '┤', styles.border)
	setRuneStyle(screen, 0, height-1, '└', styles.border)
	setRuneStyle(screen, width-1, height-1, '┘', styles.border)

	selectedIndex := model.chats.selectedIndex()
	chat, ok := model.chats.selectedChat()
	putText(screen, 2, 1, separator-2, title, styles.normal.Bold(true))
	if !ok {
		putText(screen, 2, 3, separator-2, "No chats — syncing…", styles.status.Dim(true))
		putText(screen, 2, footerTop+1, width-3, navigationFooter(model, false), styles.status.Dim(true))
		return
	}
	putText(screen, separator+2, 1, width-2, chat.title, styles.normal.Bold(true))
	drawNewerMessagesIndicator(screen, model, width, height, separator+2, 2, width-2)

	chatY := 3
	visibleChats := footerTop - chatY
	startChat := 0
	if visibleChats > 0 && selectedIndex >= visibleChats {
		startChat = selectedIndex - visibleChats + 1
	}
	for index := startChat; index < model.chats.count() && chatY+index-startChat < footerTop; index++ {
		y := chatY + index - startChat
		chat, ok := model.chats.chatAt(index)
		if !ok {
			break
		}
		selected := index == selectedIndex
		style := styles.normal
		if chat.unreadCount > 0 {
			style = styles.unreadChat
		}
		if selected {
			style = styles.selectedChat
			if chat.unreadCount > 0 {
				style = style.Bold(true)
			}
		}
		if selected {
			for x := 1; x < separator; x++ {
				screen.SetContent(x, y, ' ', nil, style)
			}
		}
		label := formatChatRow(chat.title, chat.unreadCount, selected, separator-3)
		putText(screen, 2, y, separator-1, label, style)
	}

	start, end := visibleMessageRange(model, width, height)
	drawMessages(screen, model, selectedIndex, chat, start, end, separator+2, 3, width-2, composeSeparator)
	if replyRows > 0 {
		drawReplyPreview(screen, model, selectedIndex, separator+2, composeSeparator+1, width-2, replyRows)
	}
	drawComposer(screen, model, separator+2, composeRow, width-2)
	footer := navigationFooter(model, false)
	if model.replySelect.valid {
		footer = navigationFooter(model, false)
	} else if model.mode == modeCompose {
		footer = "↑/↓ scroll  Enter send  Ctrl-R reply  Ctrl-E emoji  Ctrl-P settings  Esc cancel"
	}
	putText(screen, 2, height-2, width-2, footer, styles.status.Dim(true))
}

func drawComposer(screen tcell.Screen, model *viewModel, x, y, limit int) {
	styles := model.styles()
	if x >= limit {
		return
	}
	putText(screen, x, y, limit, "> ", styles.composer)
	inputX := x + 2
	if model.mode != modeCompose {
		putText(screen, inputX, y, limit, "Write a message…", styles.composer.Dim(true))
		return
	}
	model.composer.normalize()
	available := limit - inputX
	start, end, cursorOffset := visibleDraftSpan(&model.composer, available)
	putText(screen, inputX, y, limit, string(model.composer.data[start:end]), styles.composer)
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
		if narrow {
			return "↑/↓ msg  P/S media  Enter reply  Esc cancel"
		}
		return "↑/↓ message  P preview  S save  Enter reply  Esc cancel"
	}
	if narrow {
		return "↑/↓ scroll  j/k chat  O older  P/S media  Enter  Ctrl-P  Esc quit"
	}
	return "↑/↓ scroll  j/k chats  O older  P preview  S save  Enter compose  Ctrl-P settings  Esc quit"
}

func narrowNavigationFooter(model *viewModel, width int) string {
	footer := navigationFooter(model, true)
	if uniseg.StringWidth(footer) <= width {
		return footer
	}
	if model.replySelect.valid {
		return "↑/↓  Enter  Esc cancel"
	}
	return "↑/↓  Enter  Esc quit"
}

func narrowComposeFooter(width int) string {
	footer := "↑/↓ scroll  Enter send  Ctrl-R  Ctrl-E  Ctrl-P  Esc cancel"
	if uniseg.StringWidth(footer) <= width {
		return footer
	}
	return "↑/↓  Enter send  Ctrl-R  Ctrl-E  Esc"
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

func drawReplyPreview(screen tcell.Screen, model *viewModel, chatIndex, x, y, limit, rows int) {
	style := model.styles().replyQuote
	reference := "original message unavailable"
	if original, ok := model.chats.findMessageByID(chatIndex, model.replyTarget.id); ok {
		reference = messageDisplayText(original)
		if model.options.ShowTimestamps {
			reference = timestampText(original.time) + "  " + reference
		}
	}
	if rows == 1 {
		putText(screen, x, y, limit, truncateDisplayWidth("Replying to: "+reference, limit-x), style.Bold(true))
		return
	}
	putText(screen, x, y, limit, "Replying to:", style.Bold(true))
	putText(screen, x, y+1, limit, truncateDisplayWidth(reference, limit-x), style)
}

func drawMessages(screen tcell.Screen, model *viewModel, chatIndex int, chat *chatView, start, end, x, y, limit, bottom int) {
	for index := start; index < end && y < bottom; index++ {
		if messageStartsVisibleDay(chat, index, start) {
			drawDateSeparatorStyle(screen, x, y, limit, chat.messages[index].sentAt, model.styles())
			y++
			if y >= bottom {
				break
			}
		}
		if model.chatView.hasUnreadBoundary(chat.messages[index]) && y+1 < bottom {
			drawUnreadSeparatorStyle(screen, x, y, limit, model.chatView.unreadBoundary.count, model.styles())
			y++
			if y >= bottom {
				break
			}
		}
		selected := model.replySelect.valid && model.replySelect.index == index || model.mediaTarget.active && model.mediaTarget.messageID == chat.messages[index].id
		y += drawMessage(screen, model, chatIndex, chat.messages[index], x, y, limit, bottom, selected)
	}
}

func drawMessage(screen tcell.Screen, model *viewModel, chatIndex int, message messageView, x, y, limit, bottom int, selected bool) int {
	if y >= bottom || x >= limit {
		return 0
	}
	styles := model.styles()
	style := styles.incoming
	if message.fromMe {
		style = styles.outgoing
	}
	if selected {
		style = styles.popupSelected
	}
	paneLeft := x
	bodyWidth, timestampPrefix := messageWrapWidth(message, limit-x, model.options.ShowTimestamps)
	showTimestamp := timestampPrefix > 0
	quoteLine := ""
	if message.hasReply {
		quoteLine = messageQuoteLine(model, chatIndex, message, bodyWidth)
	}
	x = messageBlockLeft(message, x, limit, bodyWidth, timestampPrefix, quoteLine)
	bodyX := x + timestampPrefix
	rows := 0
	if message.isGroup && !message.fromMe {
		fillMessageRow(screen, paneLeft, y, limit, style)
		if showTimestamp {
			putText(screen, x, y, x+timestampTextWidth, timestampText(message.time), style)
		}
		senderStyle := styles.groupSender
		if selected {
			senderStyle = style.Bold(true)
		}
		putText(screen, bodyX, y, limit, truncateDisplayWidth(senderLabel(message), limit-bodyX), senderStyle)
		rows++
	}
	if message.hasReply && y+rows < bottom {
		fillMessageRow(screen, paneLeft, y+rows, limit, style)
		if showTimestamp && rows == 0 {
			putText(screen, x, y+rows, x+timestampTextWidth, timestampText(message.time), style)
		}
		quoteStyle := styles.replyQuote
		if selected {
			quoteStyle = style
		}
		putText(screen, bodyX, y+rows, limit, quoteLine, quoteStyle)
		rows++
	}
	remaining := messageDisplayText(message)
	for remaining != "" && y+rows < bottom {
		fillMessageRow(screen, paneLeft, y+rows, limit, style)
		if rows == 0 && showTimestamp {
			putText(screen, x, y+rows, x+timestampTextWidth, timestampText(message.time), style)
		}
		line, rest := nextWrappedLine(remaining, bodyWidth)
		// A whole two-cell grapheme cannot fit in a one-cell fallback pane.
		// Use the existing ellipsis fallback instead of overwriting the border.
		putText(screen, bodyX, y+rows, limit, truncateDisplayWidth(line, limit-bodyX), style)
		remaining = rest
		rows++
	}
	if rows == 0 {
		return 1
	}
	return rows
}

func messageQuoteLine(model *viewModel, chatIndex int, message messageView, width int) string {
	reference := "original message unavailable"
	if message.replyText != "" || message.replyMediaKind != "" {
		reference = mediaPlaceholder(message.replyMediaKind, message.replyMediaName)
		if reference == "" {
			reference = message.replyText
		} else if message.replyText != "" {
			reference += " " + message.replyText
		}
	}
	if original, ok := model.chats.findMessageByID(chatIndex, message.replyToID); ok {
		if message.replyText == "" && message.replyMediaKind == "" {
			reference = messageDisplayText(original)
		}
		if model.options.ShowTimestamps {
			reference = timestampText(original.time) + " " + reference
		}
	}
	// Quotes remain one-row excerpts; line separators are presentation spaces,
	// not terminal control characters. The stored quote itself is unchanged.
	reference = strings.ReplaceAll(reference, "\r\n", " ")
	reference = strings.ReplaceAll(reference, "\n", " ")
	reference = strings.ReplaceAll(reference, "\r", " ")
	return truncateDisplayWidth("↪ "+reference, width)
}

func timestampText(value string) string {
	return truncateDisplayWidth(value, timestampTextWidth)
}

func drawUnreadSeparator(screen tcell.Screen, x, y, limit int, count uint32) {
	drawUnreadSeparatorStyle(screen, x, y, limit, count, stylesFor(ThemeTerminal))
}

func drawUnreadSeparatorStyle(screen tcell.Screen, x, y, limit int, count uint32, styles semanticStyles) {
	if x >= limit || count == 0 {
		return
	}
	lineStyle := styles.unreadSeparator.Dim(true)
	for column := x; column < limit; column++ {
		screen.SetContent(column, y, '─', nil, lineStyle)
	}
	label := " " + strconv.Itoa(int(count)) + " new message"
	if count != 1 {
		label += "s"
	}
	label += " "
	label = truncateDisplayWidth(label, limit-x)
	labelWidth := uniseg.StringWidth(label)
	labelX := x
	if labelWidth < limit-x {
		labelX += (limit - x - labelWidth) / 2
	}
	putText(screen, labelX, y, limit, label, styles.unreadSeparator)
}

func fillMessageRow(screen tcell.Screen, x, y, limit int, style tcell.Style) {
	for column := x; column < limit; column++ {
		screen.SetContent(column, y, ' ', nil, style)
	}
}

func visibleMessageRange(model *viewModel, width, height int) (int, int) {
	chat, ok := model.chats.selectedChat()
	if !ok {
		return 0, 0
	}
	messageWidth, rows := conversationViewport(model, width, height)
	if chat.messageCount == 0 || messageWidth <= 0 || rows <= 0 {
		return 0, 0
	}
	offset := model.chatView.scrollOffset
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
		candidate := chat.messages[start-1]
		lines := renderedMessageLines(model, candidate, messageWidth)
		if !candidate.sentAt.IsZero() && (start == end || !sameLocalMessageDay(candidate.sentAt, chat.messages[start].sentAt)) {
			lines++
		}
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
	chat, ok := model.chats.selectedChat()
	if !ok {
		return 0
	}
	messageWidth, rows := conversationViewport(model, width, height)
	if chat.messageCount == 0 || messageWidth <= 0 || rows <= 0 {
		return 0
	}
	end := 0
	used := 0
	for end < chat.messageCount {
		lines := renderedMessageLines(model, chat.messages[end], messageWidth)
		if !chat.messages[end].sentAt.IsZero() && (end == 0 || !sameLocalMessageDay(chat.messages[end-1].sentAt, chat.messages[end].sentAt)) {
			lines++
		}
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

func messageStartsVisibleDay(chat *chatView, index, visibleStart int) bool {
	if chat == nil || index < 0 || index >= chat.messageCount || chat.messages[index].sentAt.IsZero() {
		return false
	}
	return index == visibleStart || index == 0 || !sameLocalMessageDay(chat.messages[index-1].sentAt, chat.messages[index].sentAt)
}

func renderedMessageLines(model *viewModel, message messageView, width int) int {
	lines := wrappedMessageLines(message, width, model.options.ShowTimestamps)
	if model.chatView.hasUnreadBoundary(message) {
		lines++
	}
	return lines
}

func wrappedMessageLines(message messageView, width int, showTimestamps bool) int {
	if width <= 0 {
		return 0
	}
	bodyWidth, _ := messageWrapWidth(message, width, showTimestamps)
	if bodyWidth <= 0 {
		bodyWidth = 1
	}
	extra := 0
	if message.isGroup && !message.fromMe {
		extra = 1
	}
	displayText := messageDisplayText(message)
	if displayText == "" {
		if message.hasReply {
			return 1 + extra
		}
		return 1
	}
	lines := 0
	remaining := displayText
	for remaining != "" {
		_, remaining = nextWrappedLine(remaining, bodyWidth)
		lines++
	}
	if message.hasReply {
		lines++
	}
	return lines + extra
}

func nextWrappedLine(value string, width int) (string, string) {
	if value == "" || width <= 0 {
		return "", ""
	}
	// Wrap one explicit line at a time. Keep indentation after a newline;
	// trimming spaces is only for a soft wrap within the same line.
	line, following := value, ""
	if newline := strings.IndexByte(value, '\n'); newline >= 0 {
		line, following = strings.TrimSuffix(value[:newline], "\r"), value[newline+1:]
	}
	if uniseg.StringWidth(line) <= width {
		return line, following
	}
	cut := 0
	lastSpace := -1
	used := 0
	graphemes := uniseg.NewGraphemes(line)
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
	if cut >= len(line) {
		return line, following
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
	drawHorizontalStyle(screen, from, to, y, character, tcell.StyleDefault)
}

func drawHorizontalStyle(screen tcell.Screen, from, to, y int, character rune, style tcell.Style) {
	for x := from; x <= to; x++ {
		setRuneStyle(screen, x, y, character, style)
	}
}

func drawVertical(screen tcell.Screen, from, to, x int, character rune) {
	drawVerticalStyle(screen, from, to, x, character, tcell.StyleDefault)
}

func drawVerticalStyle(screen tcell.Screen, from, to, x int, character rune, style tcell.Style) {
	for y := from; y <= to; y++ {
		setRuneStyle(screen, x, y, character, style)
	}
}

func setRune(screen tcell.Screen, x, y int, character rune) {
	setRuneStyle(screen, x, y, character, tcell.StyleDefault)
}

func setRuneStyle(screen tcell.Screen, x, y int, character rune, style tcell.Style) {
	screen.SetContent(x, y, character, nil, style)
}
