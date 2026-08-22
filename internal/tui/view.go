package tui

import "github.com/gdamore/tcell/v2"

const (
	narrowWidth = 70
	shortHeight = 8
)

type messageView struct {
	time string
	text string
}

type viewModel struct {
	chats        [4]string
	chatCount    int
	selectedChat int
	conversation string
	messages     [3]messageView
	messageCount int
}

func defaultDemoView() viewModel {
	return viewModel{
		chats:        [4]string{"Demo Chat", "Project Room", "Family Demo", "Test Contact"},
		chatCount:    4,
		selectedChat: 0,
		conversation: "Demo Chat",
		messages: [3]messageView{
			{time: "09:42", text: "Synthetic message one"},
			{time: "09:45", text: "Synthetic reply"},
			{time: "09:47", text: "Synthetic terminal preview"},
		},
		messageCount: 3,
	}
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
	putText(screen, 0, 0, width, title, tcell.StyleDefault.Bold(true))
	putText(screen, 0, 2, width, model.conversation, tcell.StyleDefault.Bold(true))
	y := 4
	for index := 0; index < model.messageCount && y < height-2; index++ {
		putText(screen, 0, y, width, model.messages[index].text, tcell.StyleDefault)
		y += 2
	}
	putText(screen, 0, height-1, width, "Esc quit", tcell.StyleDefault.Dim(true))
}

func drawTwoPane(screen tcell.Screen, model viewModel, width, height int) {
	footerTop := height - 3
	separator := width / 3
	if separator < 22 {
		separator = 22
	}
	if maximum := width - 42; separator > maximum {
		separator = maximum
	}

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

	putText(screen, 2, 1, separator-2, title, tcell.StyleDefault.Bold(true))
	putText(screen, separator+2, 1, width-2, model.conversation, tcell.StyleDefault.Bold(true))

	chatY := 3
	for index := 0; index < model.chatCount && chatY+index < footerTop; index++ {
		y := chatY + index
		style := tcell.StyleDefault
		if index == model.selectedChat {
			style = style.Reverse(true)
			for x := 1; x < separator; x++ {
				screen.SetContent(x, y, ' ', nil, style)
			}
			putText(screen, 2, y, separator-1, "> "+model.chats[index], style)
			continue
		}
		putText(screen, 3, y, separator-1, model.chats[index], style)
	}

	messageY := 3
	for index := 0; index < model.messageCount && messageY < footerTop; index++ {
		message := model.messages[index]
		putText(screen, separator+2, messageY, width-2, message.time+"  "+message.text, tcell.StyleDefault)
		messageY += 2
	}
	putText(screen, 2, height-2, width-2, "Esc quit", tcell.StyleDefault.Dim(true))
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
