package tui

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
)

const (
	MaxMessageLinks = 16
	MaxLinkBytes    = 4096
)

type LinkAction uint8

const (
	LinkOpen LinkAction = iota + 1
	LinkCopy
)

type LinkRequest struct {
	Action LinkAction
	URL    string
}

type LinkResult struct{ Status string }

type linkPickerState struct {
	open     bool
	selected int
	count    int
	urls     [MaxMessageLinks]string
}

func extractMessageLinks(text string) []string {
	result := make([]string, 0, MaxMessageLinks)
	for offset := 0; offset < len(text) && len(result) < MaxMessageLinks; {
		relative := strings.Index(text[offset:], "http://")
		httpsRelative := strings.Index(text[offset:], "https://")
		if relative < 0 || httpsRelative >= 0 && httpsRelative < relative {
			relative = httpsRelative
		}
		if relative < 0 {
			break
		}
		start := offset + relative
		if start > 0 {
			previous, _ := utf8.DecodeLastRuneInString(text[:start])
			if unicode.IsLetter(previous) || unicode.IsDigit(previous) || previous == '_' {
				offset = start + 1
				continue
			}
		}
		end := start
		for end < len(text) && end-start <= MaxLinkBytes {
			r, size := utf8.DecodeRuneInString(text[end:])
			if r == utf8.RuneError && size == 1 || unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune("<>\"'`", r) {
				break
			}
			end += size
		}
		candidate := trimLinkPunctuation(text[start:end])
		if len(candidate) <= MaxLinkBytes && validMessageLink(candidate) {
			result = append(result, strings.Clone(candidate))
		}
		if end <= start {
			offset = start + 1
		} else {
			offset = end
		}
	}
	return result
}

func trimLinkPunctuation(value string) string {
	value = strings.TrimRight(value, ".,!?;:")
	pairs := [][2]byte{{'(', ')'}, {'[', ']'}, {'{', '}'}}
	for changed := true; changed; {
		changed = false
		for _, pair := range pairs {
			if strings.HasSuffix(value, string(pair[1])) && strings.Count(value, string(pair[1])) > strings.Count(value, string(pair[0])) {
				value = value[:len(value)-1]
				changed = true
			}
		}
	}
	return value
}

func validMessageLink(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func openFocusedLinks(model *viewModel) bool {
	if model == nil || !model.replySelect.valid {
		return false
	}
	chat, ok := model.chats.selectedChat()
	if !ok || model.replySelect.index < 0 || model.replySelect.index >= chat.messageCount {
		return false
	}
	links := extractMessageLinks(chat.messages[model.replySelect.index].text)
	if len(links) == 0 {
		model.sendStatus = "No HTTP(S) links in message"
		return true
	}
	if len(links) == 1 {
		return requestLink(model, LinkOpen, links[0])
	}
	model.linkPicker = linkPickerState{open: true, count: len(links)}
	copy(model.linkPicker.urls[:], links)
	model.sendStatus = ""
	closeMedia(model)
	return true
}

func requestLink(model *viewModel, action LinkAction, value string) bool {
	if model.links == nil || !model.links(LinkRequest{Action: action, URL: strings.Clone(value)}) {
		model.sendStatus = "Link helper unavailable"
		return true
	}
	model.linkPicker = linkPickerState{}
	if action == LinkCopy {
		model.sendStatus = "Copying link…"
	} else {
		model.sendStatus = "Opening link…"
	}
	return true
}

func handleLinkPickerKey(model *viewModel, event *tcell.EventKey) bool {
	if event == nil {
		return false
	}
	switch {
	case event.Key() == tcell.KeyEscape:
		model.linkPicker = linkPickerState{}
		return true
	case event.Key() == tcell.KeyUp || event.Key() == tcell.KeyRune && event.Rune() == 'k':
		if model.linkPicker.selected > 0 {
			model.linkPicker.selected--
			return true
		}
	case event.Key() == tcell.KeyDown || event.Key() == tcell.KeyRune && event.Rune() == 'j':
		if model.linkPicker.selected+1 < model.linkPicker.count {
			model.linkPicker.selected++
			return true
		}
	case event.Key() == tcell.KeyEnter:
		return requestLink(model, LinkOpen, model.linkPicker.urls[model.linkPicker.selected])
	case event.Key() == tcell.KeyRune && (event.Rune() == 'C' || event.Rune() == 'c'):
		return requestLink(model, LinkCopy, model.linkPicker.urls[model.linkPicker.selected])
	}
	return false
}

func drawLinkPicker(screen tcell.Screen, model *viewModel, width, height int) {
	if width < 8 || height < 5 {
		return
	}
	boxWidth := width - 4
	if boxWidth > 64 {
		boxWidth = 64
	}
	boxHeight := model.linkPicker.count + 4
	if boxHeight > height-2 {
		boxHeight = height - 2
	}
	x, y := (width-boxWidth)/2, (height-boxHeight)/2
	styles := model.styles()
	for row := 0; row < boxHeight; row++ {
		fillMessageRow(screen, x, y+row, x+boxWidth, styles.normal)
	}
	drawHorizontalStyle(screen, x, x+boxWidth-1, y, '─', styles.border)
	drawHorizontalStyle(screen, x, x+boxWidth-1, y+boxHeight-1, '─', styles.border)
	putText(screen, x+2, y, x+boxWidth-2, " Links ", styles.normal.Bold(true))
	visible := boxHeight - 3
	start := 0
	if model.linkPicker.selected >= visible {
		start = model.linkPicker.selected - visible + 1
	}
	for index := start; index < model.linkPicker.count && index-start < visible; index++ {
		style, prefix := styles.normal, "  "
		if index == model.linkPicker.selected {
			style, prefix = styles.selectedChat, "> "
		}
		putText(screen, x+1, y+1+index-start, x+boxWidth-1, prefix+model.linkPicker.urls[index], style)
	}
	putText(screen, x+2, y+boxHeight-2, x+boxWidth-2, "Enter open  C copy  Esc cancel", styles.status.Dim(true))
}
