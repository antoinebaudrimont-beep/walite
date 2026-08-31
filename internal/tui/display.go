package tui

import "unicode/utf8"

const displayMetadataCapacity = 128

// DisplayMetadata contains only advisory presentation values. IDs are opaque;
// quality ranks are supplied by the application, not inferred from names.
type DisplayMetadata struct {
	ID, Name string
	Quality  uint8
	IsGroup  bool
}
type displayState struct {
	entries [displayMetadataCapacity]DisplayMetadata
	next    int
}

func (state *displayState) lookup(id string) DisplayMetadata {
	if id != "" {
		for _, value := range state.entries {
			if value.ID == id {
				return value
			}
		}
	}
	return DisplayMetadata{}
}

func applyDisplayMetadata(view *viewModel, value DisplayMetadata) bool {
	if view == nil || view.chats == nil || value.ID == "" || len(value.ID) > 512 || len(value.Name) > 1024 || value.Quality > 5 || !utf8.ValidString(value.ID) || !utf8.ValidString(value.Name) {
		return false
	}
	index := -1
	for i, old := range view.display.entries {
		if old.ID == value.ID {
			index = i
			break
		}
	}
	if index < 0 {
		index = view.display.next
		view.display.next = (index + 1) % len(view.display.entries)
		view.display.entries[index] = DisplayMetadata{}
	}
	old := view.display.entries[index]
	value.IsGroup = value.IsGroup || old.IsGroup
	if old.ID != "" && (value.Name == "" || value.Quality < old.Quality) {
		value.Name, value.Quality = old.Name, old.Quality
	}
	view.display.entries[index] = value
	changed := false
	for i := 0; i < view.chats.chatCount; i++ {
		if enrichChatDisplay(&view.chats.chats[i], &view.display) {
			changed = true
		}
	}
	return changed
}

func enrichChatDisplay(chat *chatView, display *displayState) bool {
	changed := false
	value := display.lookup(chat.id)
	if value.Name != "" && value.Quality >= chat.titleQuality {
		if chat.title != value.Name {
			chat.title = value.Name
			changed = true
		}
		chat.titleQuality = value.Quality
	}
	chat.isGroup = chat.isGroup || value.IsGroup
	for i := 0; i < chat.messageCount; i++ {
		message := &chat.messages[i]
		value = display.lookup(message.senderID)
		if value.Name != "" && value.Quality >= message.senderQuality {
			if message.senderName != value.Name {
				message.senderName = value.Name
				if message.isGroup && !message.fromMe {
					changed = true
				}
			}
			message.senderQuality = value.Quality
		}
	}
	return changed
}

func senderLabel(message messageView) string {
	if message.senderName != "" {
		return message.senderName
	}
	if message.senderID != "" {
		return message.senderID
	}
	return "Unknown sender"
}
