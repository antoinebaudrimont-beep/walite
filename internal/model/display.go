package model

import (
	"strings"
	"unicode"
)

const DisplayMetadataCapacity = 128

// DisplayQuality ranks advisory names, never identity. Equal quality is
// last-observation-wins; empty or lower-quality observations cannot erase names.
type DisplayQuality uint8

const (
	DisplayOpaque DisplayQuality = iota
	DisplayPhone
	DisplayPush
	DisplayBusiness
	DisplaySaved
	DisplayGroup
)

// DisplayMetadata describes one opaque person or group, not a message insert.
type DisplayMetadata struct {
	id      ChatID
	name    string
	quality DisplayQuality
	group   bool
}

func NewDisplayMetadata(id, name string, quality DisplayQuality, group bool) (DisplayMetadata, error) {
	owned, err := NewChatID(id)
	if err != nil {
		return DisplayMetadata{}, err
	}
	if quality > DisplayGroup {
		return DisplayMetadata{}, newValidationError(InvalidValue, "display_quality", 0)
	}
	text, _ := normalizeBoundedUTF8(name, MaxChatDisplayNameBytes)
	text = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text))
	if text == "" {
		quality = DisplayOpaque
	}
	return DisplayMetadata{id: owned, name: text, quality: quality, group: group}, nil
}

func (metadata DisplayMetadata) ID() ChatID              { return metadata.id }
func (metadata DisplayMetadata) Name() string            { return metadata.name }
func (metadata DisplayMetadata) Quality() DisplayQuality { return metadata.quality }
func (metadata DisplayMetadata) IsGroup() bool           { return metadata.group }
func (metadata DisplayMetadata) ByteSize() int           { return len(metadata.id.value) + len(metadata.name) }

func (metadata DisplayMetadata) Merge(next DisplayMetadata) DisplayMetadata {
	if metadata.id.value == "" {
		return next
	}
	if metadata.id != next.id {
		return metadata
	}
	metadata.group = metadata.group || next.group
	if next.name != "" && next.quality >= metadata.quality {
		metadata.name, metadata.quality = next.name, next.quality
	}
	return metadata
}

// WithDisplayMetadata changes presentation only. The caller retains quality
// ranking; identity, messages, unread/activity and other chat metadata survive.
func (chat Chat) WithDisplayMetadata(metadata DisplayMetadata) Chat {
	if chat.id != metadata.id {
		return chat
	}
	chat.isGroup = chat.isGroup || metadata.group
	if metadata.name != "" {
		chat.displayName, chat.nameTruncated = metadata.name, false
		chat.placeholder = false
	}
	return chat
}
