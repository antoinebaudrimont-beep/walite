package model

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxMediaNameBytes       = 512
	MaxMediaMIMEBytes       = 256
	MaxMediaDirectPathBytes = 3072
	MaxMediaKeyBytes        = 64
	MaxMediaHashBytes       = 64
	// MaxOutgoingMediaBytes matches walite's existing explicit-save ceiling.
	// It bounds one user-selected upload without implying an upstream limit.
	MaxOutgoingMediaBytes = 100 << 20
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

// Media contains bounded presentation metadata and, when available, the
// minimum neutral inputs for an explicit later download. Protocol messages,
// URLs, thumbnails, and media bytes are deliberately excluded.
type Media struct {
	kind          MediaKind
	name          string
	mimeType      string
	directPath    string
	mediaKey      string
	fileSHA256    string
	fileEncSHA256 string
	declaredBytes uint64
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

// NewDownloadableMedia constructs bounded presentation metadata plus the
// transport-neutral inputs required to download and verify the encrypted
// payload later. It never owns a protocol message, URL, thumbnail, or bytes.
func NewDownloadableMedia(kind MediaKind, name, mimeType, directPath string, mediaKey, fileSHA256, fileEncSHA256 []byte, declaredBytes uint64) (Media, error) {
	media, err := NewMedia(kind, name, mimeType)
	if err != nil {
		return Media{}, err
	}
	if !validDownloadMetadata(directPath, mediaKey, fileSHA256, fileEncSHA256) {
		return Media{}, newValidationError(InvalidValue, "media_download", 0)
	}
	media.directPath = strings.Clone(directPath)
	media.mediaKey = string(bytes.Clone(mediaKey))
	media.fileSHA256 = string(bytes.Clone(fileSHA256))
	media.fileEncSHA256 = string(bytes.Clone(fileEncSHA256))
	media.declaredBytes = declaredBytes
	return media, nil
}

func (media Media) Kind() MediaKind { return media.kind }
func (media Media) Name() string    { return media.name }
func (media Media) MIMEType() string {
	return media.mimeType
}

// MediaDownload contains the bounded neutral download inputs. Byte accessors
// return copies so callers cannot mutate an immutable Media value.
type MediaDownload struct{ media Media }

func (media Media) Download() (MediaDownload, bool) {
	if media.directPath == "" {
		return MediaDownload{}, false
	}
	return MediaDownload{media: media}, true
}

func (download MediaDownload) DirectPath() string    { return download.media.directPath }
func (download MediaDownload) MediaKey() []byte      { return []byte(download.media.mediaKey) }
func (download MediaDownload) FileSHA256() []byte    { return []byte(download.media.fileSHA256) }
func (download MediaDownload) FileEncSHA256() []byte { return []byte(download.media.fileEncSHA256) }
func (download MediaDownload) DeclaredBytes() uint64 { return download.media.declaredBytes }

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
	if media.directPath != "" && !validDownloadMetadata(media.directPath, []byte(media.mediaKey), []byte(media.fileSHA256), []byte(media.fileEncSHA256)) {
		return newValidationError(InvalidValue, "media_download", 0)
	}
	if media.directPath == "" && (media.mediaKey != "" || media.fileSHA256 != "" || media.fileEncSHA256 != "" || media.declaredBytes != 0) {
		return newValidationError(InvalidValue, "media_download", 0)
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
	return Media{kind: media.kind, name: strings.Clone(media.name), mimeType: strings.Clone(media.mimeType), directPath: strings.Clone(media.directPath), mediaKey: strings.Clone(media.mediaKey), fileSHA256: strings.Clone(media.fileSHA256), fileEncSHA256: strings.Clone(media.fileEncSHA256), declaredBytes: media.declaredBytes}, nil
}

func validDownloadMetadata(path string, key, hash, encHash []byte) bool {
	if path == "" || path[0] != '/' || len(path) > MaxMediaDirectPathBytes || !utf8.ValidString(path) ||
		len(key) == 0 || len(key) > MaxMediaKeyBytes || len(hash) == 0 || len(hash) > MaxMediaHashBytes ||
		len(encHash) == 0 || len(encHash) > MaxMediaHashBytes {
		return false
	}
	for _, r := range path {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
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
