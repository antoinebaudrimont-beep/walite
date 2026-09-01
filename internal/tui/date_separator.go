package tui

import (
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

const messageDateFormat = "Mon 2 Jan 2006"

func localMessageTime(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.In(time.Local).Format("15:04")
}

func sameLocalMessageDay(left, right time.Time) bool {
	if left.IsZero() || right.IsZero() {
		return false
	}
	left, right = left.In(time.Local), right.In(time.Local)
	leftYear, leftMonth, leftDay := left.Date()
	rightYear, rightMonth, rightDay := right.Date()
	return leftYear == rightYear && leftMonth == rightMonth && leftDay == rightDay
}

func messageDateLabel(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.In(time.Local).Format(messageDateFormat)
}

func drawDateSeparator(screen tcell.Screen, x, y, limit int, at time.Time) {
	label := messageDateLabel(at)
	if x >= limit || label == "" {
		return
	}
	lineStyle := tcell.StyleDefault.Dim(true)
	for column := x; column < limit; column++ {
		screen.SetContent(column, y, '─', nil, lineStyle)
	}
	label = truncateDisplayWidth(" "+label+" ", limit-x)
	labelWidth := uniseg.StringWidth(label)
	labelX := x
	if labelWidth < limit-x {
		labelX += (limit - x - labelWidth) / 2
	}
	putText(screen, labelX, y, limit, label, tcell.StyleDefault.Bold(true))
}
