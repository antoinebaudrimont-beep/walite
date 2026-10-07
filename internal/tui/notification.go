package tui

import (
	"strings"
	"unicode"

	"github.com/rivo/uniseg"
)

const (
	notificationTitleLimit    = 80
	notificationBodyLimit     = 200
	notificationFilenameLimit = 80
	// Three graphemes are reserved for " — ". Bounding both parts preserves
	// sender and group identity instead of allowing either to consume the title.
	notificationGroupPartLimit = (notificationTitleLimit - 3) / 2
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
	chatTitle := sanitizeNotificationText(chat.title, notificationTitleLimit)
	if chatTitle == "" {
		chatTitle = sanitizeNotificationText(event.ChatID, notificationTitleLimit)
	}
	if chatTitle == "" {
		chatTitle = "walite"
	}
	title := chatTitle
	if chat.isGroup || message.isGroup {
		sender := sanitizeNotificationText(senderLabel(message), notificationGroupPartLimit)
		if sender == "" {
			sender = "Unknown sender"
		}
		group := sanitizeNotificationText(chatTitle, notificationGroupPartLimit)
		if group == "" {
			group = "Unknown group"
		}
		title = sender + " — " + group
	}
	body := notificationMessageText(message)
	if body == "" {
		body = "New message"
	}
	return Notification{
		Title: sanitizeNotificationText(title, notificationTitleLimit),
		Body:  body,
	}, true
}

func notificationMessageText(message messageView) string {
	caption := sanitizeNotificationText(message.text, notificationBodyLimit)
	placeholder := ""
	switch message.mediaKind {
	case mediaImage:
		placeholder = "[Image]"
	case mediaVideo:
		placeholder = "[Video]"
	case mediaAudio:
		placeholder = "[Audio]"
	case mediaSticker:
		placeholder = "[Sticker]"
	case mediaDocument:
		name := notificationFilename(message.mediaName)
		if name == "" {
			placeholder = "[Document]"
		} else {
			placeholder = "[Document: " + name + "]"
		}
	}
	if placeholder == "" {
		return caption
	}
	if caption == "" {
		return placeholder
	}
	return sanitizeNotificationText(placeholder+" "+caption, notificationBodyLimit)
}

func notificationFilename(value string) string {
	// Remote filenames are presentation data, not local paths. Use only the
	// final path-like component and handle both common separator forms without
	// consulting the host filesystem.
	value = strings.ReplaceAll(value, "\\", "/")
	if slash := strings.LastIndexByte(value, '/'); slash >= 0 {
		value = value[slash+1:]
	}
	return sanitizeNotificationText(value, notificationFilenameLimit)
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
	if text == "" {
		return ""
	}
	graphemes := uniseg.NewGraphemes(text)
	var bounded strings.Builder
	count := 0
	truncated := false
	for graphemes.Next() {
		if count == limit {
			truncated = true
			break
		}
		bounded.WriteString(graphemes.Str())
		count++
	}
	if truncated {
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
