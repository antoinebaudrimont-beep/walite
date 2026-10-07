package tui

import (
	"strings"
	"unicode"

	"github.com/rivo/uniseg"
)

const (
	notificationTitleLimit = 100
	notificationBodyLimit  = 200
)

func notificationForLiveMessage(model *viewModel, event LiveMessage, mutation liveMessageMutation) (Notification, bool) {
	if model == nil || !mutation.inserted || !event.NotificationEligible || event.FromMe || !model.options.DesktopNotifications {
		return Notification{}, false
	}
	if !model.options.NotificationPreviews {
		return Notification{Title: "walite", Body: "New WhatsApp message"}, true
	}
	chatIndex, ok := model.chats.chatIndexByID(event.ChatID)
	if !ok {
		return Notification{}, false
	}
	messageIndex, ok := model.chats.messageIndexByID(chatIndex, messageID(event.MessageID))
	if !ok {
		return Notification{}, false
	}
	chat := &model.chats.chats[chatIndex]
	message := chat.messages[messageIndex]
	title := chat.title
	if title == "" {
		title = event.ChatID
	}
	if chat.isGroup || message.isGroup {
		title = senderLabel(message) + " — " + title
	}
	body := messageDisplayText(message)
	if body == "" {
		body = "[Message]"
	}
	return Notification{
		Title: sanitizeNotificationText(title, notificationTitleLimit),
		Body:  sanitizeNotificationText(body, notificationBodyLimit),
	}, true
}

func sanitizeNotificationText(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	var cleaned strings.Builder
	space := false
	for _, r := range strings.ToValidUTF8(value, "�") {
		if notificationControl(r) || unicode.IsSpace(r) {
			space = cleaned.Len() > 0
			continue
		}
		if space {
			cleaned.WriteByte(' ')
			space = false
		}
		cleaned.WriteRune(r)
	}
	text := strings.TrimSpace(cleaned.String())
	graphemes := uniseg.NewGraphemes(text)
	var bounded strings.Builder
	count := 0
	for graphemes.Next() {
		if count == limit {
			break
		}
		bounded.WriteString(graphemes.Str())
		count++
	}
	if graphemes.Next() {
		result := bounded.String()
		if limit == 1 {
			return "…"
		}
		clusters := uniseg.NewGraphemes(result)
		bounded.Reset()
		for index := 0; index < limit-1 && clusters.Next(); index++ {
			bounded.WriteString(clusters.Str())
		}
		bounded.WriteRune('…')
	}
	return bounded.String()
}

func notificationControl(r rune) bool {
	return r < 0x20 || r >= 0x7f && r <= 0x9f ||
		r == 0x061c || r == 0x200e || r == 0x200f ||
		r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069
}
