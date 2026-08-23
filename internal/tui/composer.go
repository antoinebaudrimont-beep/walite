package tui

import (
	"unicode"
	"unicode/utf8"
)

const maxDraftBytes = 16 * 1024

type inputMode uint8

const (
	modeNavigate inputMode = iota
	modeCompose
)

type composerState struct {
	data   [maxDraftBytes]byte
	length int
	cursor int
}

func (composer *composerState) insert(character rune) bool {
	composer.normalize()
	if !unicode.IsPrint(character) || !utf8.ValidRune(character) {
		return false
	}
	var encoded [utf8.UTFMax]byte
	size := utf8.EncodeRune(encoded[:], character)
	if composer.length+size > len(composer.data) {
		return false
	}
	copy(composer.data[composer.cursor+size:composer.length+size], composer.data[composer.cursor:composer.length])
	copy(composer.data[composer.cursor:composer.cursor+size], encoded[:size])
	composer.cursor += size
	composer.length += size
	return true
}

func (composer *composerState) backspace() bool {
	composer.normalize()
	if composer.cursor == 0 {
		return false
	}
	_, size := utf8.DecodeLastRune(composer.data[:composer.cursor])
	start := composer.cursor - size
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
	_, size := utf8.DecodeRune(composer.data[composer.cursor:composer.length])
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
	_, size := utf8.DecodeLastRune(composer.data[:composer.cursor])
	composer.cursor -= size
	return true
}

func (composer *composerState) moveRight() bool {
	composer.normalize()
	if composer.cursor == composer.length {
		return false
	}
	_, size := utf8.DecodeRune(composer.data[composer.cursor:composer.length])
	composer.cursor += size
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
	for composer.cursor > 0 && composer.cursor < composer.length && !utf8.RuneStart(composer.data[composer.cursor]) {
		composer.cursor--
	}
}

func (composer *composerState) zeroTail(size int) {
	clear(composer.data[composer.length : composer.length+size])
}
