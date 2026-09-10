package service

import (
	"encoding/binary"
	"errors"
	"io"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

const (
	StickerWidth              = model.StickerWidth
	StickerHeight             = model.StickerHeight
	MaxStaticStickerBytes     = model.MaxStaticStickerBytes
	MaxAnimatedStickerBytes   = model.MaxAnimatedStickerBytes
	minAnimatedFrameMillis    = 8
	maxAnimatedDurationMillis = 10_000
	maxAnimatedStickerFrames  = maxAnimatedDurationMillis / minAnimatedFrameMillis
)

type StickerMetadata = model.StickerSendMetadata

func inspectStickerWebP(reader io.ReaderAt, size uint64) (StickerMetadata, error) {
	if reader == nil || size < 20 || size > MaxAnimatedStickerBytes {
		return StickerMetadata{}, ErrStickerRejected
	}
	var riff [12]byte
	if !readExactlyAt(reader, riff[:], 0) || string(riff[:4]) != "RIFF" || string(riff[8:]) != "WEBP" || uint64(binary.LittleEndian.Uint32(riff[4:8]))+8 != size {
		return StickerMetadata{}, ErrStickerRejected
	}

	var width, height uint32
	var extended, animationFlag, animationHeader, topLevelImage bool
	frames, totalDuration := 0, uint32(0)
	for offset := uint64(12); offset < size; {
		chunkType, payloadOffset, payloadSize, next, ok := webPChunk(reader, offset, size)
		if !ok {
			return StickerMetadata{}, ErrStickerRejected
		}
		switch chunkType {
		case "VP8X":
			if extended || payloadSize != 10 {
				return StickerMetadata{}, ErrStickerRejected
			}
			var payload [10]byte
			if !readExactlyAt(reader, payload[:], int64(payloadOffset)) {
				return StickerMetadata{}, ErrStickerRejected
			}
			extended = true
			animationFlag = payload[0]&0x02 != 0
			width = littleEndian24(payload[4:7]) + 1
			height = littleEndian24(payload[7:10]) + 1
		case "VP8 ":
			chunkWidth, chunkHeight, ok := lossyWebPDimensions(reader, payloadOffset, payloadSize)
			if !ok || topLevelImage || animationFlag {
				return StickerMetadata{}, ErrStickerRejected
			}
			topLevelImage = true
			if width == 0 {
				width, height = chunkWidth, chunkHeight
			} else if width != chunkWidth || height != chunkHeight {
				return StickerMetadata{}, ErrStickerRejected
			}
		case "VP8L":
			chunkWidth, chunkHeight, ok := losslessWebPDimensions(reader, payloadOffset, payloadSize)
			if !ok || topLevelImage || animationFlag {
				return StickerMetadata{}, ErrStickerRejected
			}
			topLevelImage = true
			if width == 0 {
				width, height = chunkWidth, chunkHeight
			} else if width != chunkWidth || height != chunkHeight {
				return StickerMetadata{}, ErrStickerRejected
			}
		case "ANIM":
			if animationHeader || payloadSize != 6 {
				return StickerMetadata{}, ErrStickerRejected
			}
			animationHeader = true
		case "ANMF":
			if !animationFlag || !animationHeader {
				return StickerMetadata{}, ErrStickerRejected
			}
			duration, ok := inspectAnimationFrame(reader, payloadOffset, payloadSize, width, height)
			if !ok || duration < minAnimatedFrameMillis || frames >= maxAnimatedStickerFrames || totalDuration+duration > maxAnimatedDurationMillis {
				return StickerMetadata{}, ErrStickerRejected
			}
			frames++
			totalDuration += duration
		}
		offset = next
	}

	animated := animationFlag && animationHeader && frames > 0
	if width != StickerWidth || height != StickerHeight || animated != (animationHeader || frames > 0) || animated == topLevelImage {
		return StickerMetadata{}, ErrStickerRejected
	}
	metadata, valid := model.NewStickerSendMetadata(width, height, animated, size)
	if !valid {
		return StickerMetadata{}, ErrStickerRejected
	}
	return metadata, nil
}

func webPChunk(reader io.ReaderAt, offset, limit uint64) (string, uint64, uint64, uint64, bool) {
	if offset > limit || limit-offset < 8 {
		return "", 0, 0, 0, false
	}
	var header [8]byte
	if !readExactlyAt(reader, header[:], int64(offset)) {
		return "", 0, 0, 0, false
	}
	payloadSize := uint64(binary.LittleEndian.Uint32(header[4:]))
	payloadOffset := offset + 8
	next := payloadOffset + payloadSize + payloadSize%2
	if next < payloadOffset || next > limit {
		return "", 0, 0, 0, false
	}
	return string(header[:4]), payloadOffset, payloadSize, next, true
}

func lossyWebPDimensions(reader io.ReaderAt, offset, size uint64) (uint32, uint32, bool) {
	if size < 10 {
		return 0, 0, false
	}
	var header [10]byte
	if !readExactlyAt(reader, header[:], int64(offset)) || header[3] != 0x9d || header[4] != 0x01 || header[5] != 0x2a {
		return 0, 0, false
	}
	return uint32(binary.LittleEndian.Uint16(header[6:8]) & 0x3fff), uint32(binary.LittleEndian.Uint16(header[8:10]) & 0x3fff), true
}

func losslessWebPDimensions(reader io.ReaderAt, offset, size uint64) (uint32, uint32, bool) {
	if size < 5 {
		return 0, 0, false
	}
	var header [5]byte
	if !readExactlyAt(reader, header[:], int64(offset)) || header[0] != 0x2f {
		return 0, 0, false
	}
	bits := binary.LittleEndian.Uint32(header[1:5])
	return bits&0x3fff + 1, bits>>14&0x3fff + 1, true
}

func inspectAnimationFrame(reader io.ReaderAt, offset, size uint64, canvasWidth, canvasHeight uint32) (uint32, bool) {
	if size < 24 || canvasWidth == 0 || canvasHeight == 0 {
		return 0, false
	}
	var header [16]byte
	if !readExactlyAt(reader, header[:], int64(offset)) {
		return 0, false
	}
	x, y := 2*littleEndian24(header[0:3]), 2*littleEndian24(header[3:6])
	width, height := littleEndian24(header[6:9])+1, littleEndian24(header[9:12])+1
	if x+width > canvasWidth || y+height > canvasHeight {
		return 0, false
	}
	frameEnd := offset + size
	imageChunks := 0
	for nested := offset + 16; nested < frameEnd; {
		chunkType, payloadOffset, payloadSize, next, ok := webPChunk(reader, nested, frameEnd)
		if !ok {
			return 0, false
		}
		switch chunkType {
		case "VP8 ":
			var chunkWidth, chunkHeight uint32
			if chunkWidth, chunkHeight, ok = lossyWebPDimensions(reader, payloadOffset, payloadSize); !ok || chunkWidth != width || chunkHeight != height {
				return 0, false
			}
			imageChunks++
		case "VP8L":
			var chunkWidth, chunkHeight uint32
			if chunkWidth, chunkHeight, ok = losslessWebPDimensions(reader, payloadOffset, payloadSize); !ok || chunkWidth != width || chunkHeight != height {
				return 0, false
			}
			imageChunks++
		}
		nested = next
	}
	return littleEndian24(header[12:15]), imageChunks == 1
}

func littleEndian24(value []byte) uint32 {
	return uint32(value[0]) | uint32(value[1])<<8 | uint32(value[2])<<16
}

func readExactlyAt(reader io.ReaderAt, destination []byte, offset int64) bool {
	read, err := reader.ReadAt(destination, offset)
	return read == len(destination) && (err == nil || errors.Is(err, io.EOF))
}
