package tui

const (
	mediaImage    = "image"
	mediaVideo    = "video"
	mediaDocument = "document"
	mediaAudio    = "audio"
	mediaSticker  = "sticker"

	maxMediaNameBytes = 512
)

func validMediaPresentation(kind, name string) bool {
	if kind == "" {
		return name == ""
	}
	if len(name) > maxMediaNameBytes {
		return false
	}
	switch kind {
	case mediaImage, mediaVideo, mediaDocument, mediaAudio, mediaSticker:
		return true
	default:
		return false
	}
}

func mediaPlaceholder(kind, name string) string {
	switch kind {
	case mediaImage:
		return "[Image]"
	case mediaVideo:
		return "[Video]"
	case mediaDocument:
		if name != "" {
			return "[Document: " + name + "]"
		}
		return "[Document]"
	case mediaAudio:
		return "[Audio]"
	case mediaSticker:
		return "[Sticker]"
	case "":
		return ""
	default:
		return "[Media]"
	}
}

func messageDisplayText(message messageView) string {
	placeholder := mediaPlaceholder(message.mediaKind, message.mediaName)
	if placeholder == "" {
		return message.text
	}
	if message.text == "" {
		return placeholder
	}
	return placeholder + " " + message.text
}
