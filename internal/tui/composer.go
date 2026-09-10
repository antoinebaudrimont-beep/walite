package tui

import (
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

const maxDraftBytes = 16 * 1024

type inputMode uint8

const (
	modeNavigate inputMode = iota
	modeCompose
	modeFile
)

type composerState struct {
	data   [maxDraftBytes]byte
	length int
	cursor int
}

func (composer *composerState) insert(character rune) bool {
	if !unicode.IsPrint(character) || !utf8.ValidRune(character) {
		return false
	}
	var encoded [utf8.UTFMax]byte
	size := utf8.EncodeRune(encoded[:], character)
	return composer.insertText(string(encoded[:size]))
}

func (composer *composerState) insertText(value string) bool {
	composer.normalize()
	if value == "" || !utf8.ValidString(value) || composer.length+len(value) > len(composer.data) {
		return false
	}
	copy(composer.data[composer.cursor+len(value):composer.length+len(value)], composer.data[composer.cursor:composer.length])
	copy(composer.data[composer.cursor:composer.cursor+len(value)], value)
	composer.cursor += len(value)
	composer.length += len(value)
	return true
}

func (composer *composerState) backspace() bool {
	composer.normalize()
	if composer.cursor == 0 {
		return false
	}
	start := previousGraphemeBoundary(composer.data[:composer.length], composer.cursor)
	size := composer.cursor - start
	copy(composer.data[start:], composer.data[composer.cursor:composer.length])
	composer.length -= size
	composer.cursor = start
	composer.zeroTail(size)
	return true
}

func (composer *composerState) delete() bool {
	composer.normalize()
	if composer.cursor == composer.length {
		return false
	}
	end := nextGraphemeBoundary(composer.data[:composer.length], composer.cursor)
	size := end - composer.cursor
	copy(composer.data[composer.cursor:], composer.data[composer.cursor+size:composer.length])
	composer.length -= size
	composer.zeroTail(size)
	return true
}

func (composer *composerState) moveLeft() bool {
	composer.normalize()
	if composer.cursor == 0 {
		return false
	}
	composer.cursor = previousGraphemeBoundary(composer.data[:composer.length], composer.cursor)
	return true
}

func (composer *composerState) moveRight() bool {
	composer.normalize()
	if composer.cursor == composer.length {
		return false
	}
	composer.cursor = nextGraphemeBoundary(composer.data[:composer.length], composer.cursor)
	return true
}

func (composer *composerState) clear() {
	length := composer.length
	if length < 0 {
		length = 0
	}
	if length > len(composer.data) {
		length = len(composer.data)
	}
	clear(composer.data[:length])
	composer.length = 0
	composer.cursor = 0
}

func (composer *composerState) text() string {
	composer.normalize()
	return string(composer.data[:composer.length])
}

func (composer *composerState) normalize() {
	if composer.length < 0 {
		composer.length = 0
	}
	if composer.length > len(composer.data) {
		composer.length = len(composer.data)
	}
	if !utf8.Valid(composer.data[:composer.length]) {
		composer.clear()
		return
	}
	if composer.cursor < 0 {
		composer.cursor = 0
	}
	if composer.cursor > composer.length {
		composer.cursor = composer.length
	}
	if composer.cursor == 0 || composer.cursor == composer.length {
		return
	}
	position := 0
	state := -1
	for position < composer.length {
		cluster, _, _, nextState := uniseg.FirstGraphemeCluster(composer.data[position:composer.length], state)
		end := position + len(cluster)
		if composer.cursor == position {
			return
		}
		if composer.cursor < end {
			composer.cursor = position
			return
		}
		position = end
		state = nextState
	}
}

func (composer *composerState) zeroTail(size int) {
	clear(composer.data[composer.length : composer.length+size])
}

func previousGraphemeBoundary(data []byte, cursor int) int {
	previous := 0
	position := 0
	state := -1
	for position < cursor {
		cluster, _, _, nextState := uniseg.FirstGraphemeCluster(data[position:], state)
		end := position + len(cluster)
		if end >= cursor {
			return position
		}
		previous = position
		position = end
		state = nextState
	}
	return previous
}

func nextGraphemeBoundary(data []byte, cursor int) int {
	position := 0
	state := -1
	for position < len(data) {
		cluster, _, _, nextState := uniseg.FirstGraphemeCluster(data[position:], state)
		end := position + len(cluster)
		if position >= cursor || end > cursor {
			return end
		}
		position = end
		state = nextState
	}
	return len(data)
}
