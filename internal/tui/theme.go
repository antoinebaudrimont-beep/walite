package tui

import "github.com/gdamore/tcell/v2"

// semanticStyles is the only place where a theme assigns presentation roles.
// Renderers ask for meaning, never for a named palette.
type semanticStyles struct {
	normal, incoming, outgoing        tcell.Style
	selectedChat, unreadChat          tcell.Style
	border, popup, popupSelected      tcell.Style
	dateSeparator, unreadSeparator    tcell.Style
	replyQuote, groupSender, composer tcell.Style
	status, warning                   tcell.Style
}

func stylesFor(theme string) semanticStyles {
	switch theme {
	case ThemeDark:
		base := tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorBlack)
		return semanticStyles{
			normal: base, incoming: base, outgoing: base.Foreground(tcell.ColorAqua),
			selectedChat: base.Foreground(tcell.ColorBlack).Background(tcell.ColorAqua).Bold(true),
			unreadChat:   base.Foreground(tcell.ColorYellow).Bold(true), border: base.Foreground(tcell.ColorDarkCyan),
			popup:         base.Foreground(tcell.ColorWhite).Background(tcell.ColorNavy),
			popupSelected: base.Foreground(tcell.ColorBlack).Background(tcell.ColorYellow).Bold(true),
			dateSeparator: base.Foreground(tcell.ColorAqua).Dim(true), unreadSeparator: base.Foreground(tcell.ColorYellow).Bold(true),
			replyQuote: base.Foreground(tcell.ColorSilver), groupSender: base.Foreground(tcell.ColorGreen).Bold(true),
			composer: base.Foreground(tcell.ColorWhite), status: base.Foreground(tcell.ColorYellow), warning: base.Foreground(tcell.ColorRed).Bold(true),
		}
	case ThemeLight:
		base := tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.ColorWhite)
		return semanticStyles{
			normal: base, incoming: base, outgoing: base.Foreground(tcell.ColorBlue),
			selectedChat: base.Foreground(tcell.ColorWhite).Background(tcell.ColorBlue).Bold(true),
			unreadChat:   base.Foreground(tcell.ColorMaroon).Bold(true), border: base.Foreground(tcell.ColorBlue),
			popup: base, popupSelected: base.Foreground(tcell.ColorWhite).Background(tcell.ColorBlue).Bold(true),
			dateSeparator: base.Foreground(tcell.ColorBlue).Dim(true), unreadSeparator: base.Foreground(tcell.ColorMaroon).Bold(true),
			replyQuote: base.Foreground(tcell.ColorDarkCyan), groupSender: base.Foreground(tcell.ColorGreen).Bold(true),
			composer: base, status: base.Foreground(tcell.ColorBlue), warning: base.Foreground(tcell.ColorRed).Bold(true),
		}
	case ThemeHighContrast:
		base := tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorBlack)
		return semanticStyles{
			normal: base, incoming: base, outgoing: base.Foreground(tcell.ColorYellow).Bold(true),
			selectedChat: base.Foreground(tcell.ColorBlack).Background(tcell.ColorWhite).Bold(true),
			unreadChat:   base.Foreground(tcell.ColorYellow).Bold(true), border: base.Bold(true),
			popup: base, popupSelected: base.Foreground(tcell.ColorBlack).Background(tcell.ColorYellow).Bold(true),
			dateSeparator: base.Bold(true), unreadSeparator: base.Foreground(tcell.ColorYellow).Bold(true),
			replyQuote: base.Foreground(tcell.ColorAqua).Bold(true), groupSender: base.Foreground(tcell.ColorGreen).Bold(true),
			composer: base.Bold(true), status: base.Foreground(tcell.ColorYellow).Bold(true), warning: base.Foreground(tcell.ColorRed).Bold(true),
		}
	default:
		base := tcell.StyleDefault
		return semanticStyles{
			normal: base, incoming: base, outgoing: base,
			selectedChat: base.Reverse(true), unreadChat: base.Bold(true), border: base,
			popup: base, popupSelected: base.Reverse(true).Bold(true),
			dateSeparator: base.Dim(true), unreadSeparator: base.Bold(true),
			replyQuote: base, groupSender: base.Bold(true), composer: base,
			status: base, warning: base.Bold(true),
		}
	}
}

func (model *viewModel) styles() semanticStyles {
	if model == nil {
		return stylesFor(ThemeTerminal)
	}
	return stylesFor(model.options.Theme)
}
