package model

const (
	StickerWidth            = 512
	StickerHeight           = 512
	MaxStaticStickerBytes   = 100 << 10
	MaxAnimatedStickerBytes = 500 << 10
)

// StickerSendMetadata is the small transport-neutral descriptor needed to
// construct an outgoing sticker message. It deliberately contains no media
// bytes or transport-specific objects.
type StickerSendMetadata struct {
	width, height uint32
	animated      bool
}

func NewStickerSendMetadata(width, height uint32, animated bool, size uint64) (StickerSendMetadata, bool) {
	metadata := StickerSendMetadata{width: width, height: height, animated: animated}
	return metadata, metadata.ValidFor(size)
}

func (metadata StickerSendMetadata) Width() uint32  { return metadata.width }
func (metadata StickerSendMetadata) Height() uint32 { return metadata.height }
func (metadata StickerSendMetadata) Animated() bool { return metadata.animated }
func (metadata StickerSendMetadata) IsZero() bool   { return metadata == (StickerSendMetadata{}) }

func (metadata StickerSendMetadata) ValidFor(size uint64) bool {
	if metadata.width != StickerWidth || metadata.height != StickerHeight || size == 0 {
		return false
	}
	if metadata.animated {
		return size <= MaxAnimatedStickerBytes
	}
	return size <= MaxStaticStickerBytes
}
