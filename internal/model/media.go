package model

import (
	"strings"
	"unicode"
)

const (
	MaxMediaNameBytes = 512
	MaxMediaMIMEBytes = 256
)

// MediaKind is a closed transport-neutral media classification. Its zero
// value means that a message is not a supported media message.
type MediaKind uint8

const (
	MediaImage MediaKind = iota + 1
	MediaVideo
	MediaDocument
	MediaAudio
	MediaSticker
)

// Media contains only bounded presentation metadata. Download URLs, keys,
// hashes, thumbnails, protocol messages, and media bytes are deliberately
// excluded from this milestone.
type Media struct {
	kind     MediaKind
	name     string
	mimeType string
}

func NewMedia(kind MediaKind, name, mimeType string) (Media, error) {
	if !validMediaKind(kind) {
		return Media{}, newValidationError(InvalidValue, "media_kind", 0)
	}
	return Media{
		kind:     kind,
		name:     normalizeMediaField(name, MaxMediaNameBytes),
		mimeType: normalizeMediaField(mimeType, MaxMediaMIMEBytes),
	}, nil
}

func (media Media) Kind() MediaKind { return media.kind }
func (media Media) Name() string    { return media.name }
func (media Media) MIMEType() string {
	return media.mimeType
}

func (kind MediaKind) String() string {
	switch kind {
	case MediaImage:
		return "image"
	case MediaVideo:
		return "video"
	case MediaDocument:
		return "document"
	case MediaAudio:
		return "audio"
	case MediaSticker:
		return "sticker"
	default:
		return ""
	}
}

func validMediaKind(kind MediaKind) bool {
	return kind >= MediaImage && kind <= MediaSticker
}

func validateMedia(media Media) error {
	if media == (Media{}) {
		return nil
	}
	if !validMediaKind(media.kind) {
		return newValidationError(InvalidValue, "media_kind", 0)
	}
	if len(media.name) > MaxMediaNameBytes {
		return newValidationError(TooLong, "media_name", MaxMediaNameBytes)
	}
	if len(media.mimeType) > MaxMediaMIMEBytes {
		return newValidationError(TooLong, "media_mime_type", MaxMediaMIMEBytes)
	}
	return nil
}

func cloneMedia(media Media) (Media, error) {
	if err := validateMedia(media); err != nil {
		return Media{}, err
	}
	if media == (Media{}) {
		return Media{}, nil
	}
	return Media{kind: media.kind, name: strings.Clone(media.name), mimeType: strings.Clone(media.mimeType)}, nil
}

func normalizeMediaField(value string, limit int) string {
	value, _ = normalizeBoundedUTF8(value, limit)
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
}
