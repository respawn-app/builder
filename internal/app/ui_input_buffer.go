package app

import "unicode"

func (m *uiModel) inputRunes() []rune {
	return []rune(m.input)
}

func (m *uiModel) cursorIndex() int {
	runes := m.inputRunes()
	return clampCursor(m.inputCursor, len(runes))
}

func (m *uiModel) clearInput() {
	m.input = ""
	m.inputCursor = -1
}

func (m *uiModel) insertInputRunes(chars []rune) {
	if len(chars) == 0 {
		return
	}
	runes := m.inputRunes()
	cursor := clampCursor(m.inputCursor, len(runes))
	updated := make([]rune, 0, len(runes)+len(chars))
	updated = append(updated, runes[:cursor]...)
	updated = append(updated, chars...)
	updated = append(updated, runes[cursor:]...)
	m.input = string(updated)
	m.inputCursor = cursor + len(chars)
}

func (m *uiModel) backspaceInput() bool {
	runes := m.inputRunes()
	cursor := clampCursor(m.inputCursor, len(runes))
	if cursor == 0 {
		return false
	}
	updated := make([]rune, 0, len(runes)-1)
	updated = append(updated, runes[:cursor-1]...)
	updated = append(updated, runes[cursor:]...)
	m.input = string(updated)
	m.inputCursor = cursor - 1
	return true
}

func (m *uiModel) moveCursorLeft() {
	cursor := m.cursorIndex()
	if cursor > 0 {
		m.inputCursor = cursor - 1
	}
}

func (m *uiModel) moveCursorRight() {
	runes := m.inputRunes()
	cursor := clampCursor(m.inputCursor, len(runes))
	if cursor < len(runes) {
		m.inputCursor = cursor + 1
	}
}

func (m *uiModel) moveCursorStart() {
	m.inputCursor = 0
}

func (m *uiModel) moveCursorEnd() {
	m.inputCursor = -1
}

func (m *uiModel) moveCursorWordLeft() {
	runes := m.inputRunes()
	m.inputCursor = prevWordBoundary(runes, clampCursor(m.inputCursor, len(runes)))
}

func (m *uiModel) moveCursorWordRight() {
	runes := m.inputRunes()
	m.inputCursor = nextWordBoundary(runes, clampCursor(m.inputCursor, len(runes)))
}

func prevWordBoundary(runes []rune, cursor int) int {
	i := clampCursor(cursor, len(runes))
	for i > 0 && unicode.IsSpace(runes[i-1]) {
		i--
	}
	if i == 0 {
		return 0
	}
	class := runeClass(runes[i-1])
	for i > 0 && runeClass(runes[i-1]) == class {
		i--
	}
	return i
}

func nextWordBoundary(runes []rune, cursor int) int {
	i := clampCursor(cursor, len(runes))
	for i < len(runes) && unicode.IsSpace(runes[i]) {
		i++
	}
	if i >= len(runes) {
		return len(runes)
	}
	class := runeClass(runes[i])
	for i < len(runes) && runeClass(runes[i]) == class {
		i++
	}
	return i
}

func clampCursor(cursor, size int) int {
	if cursor < 0 {
		return size
	}
	if cursor > size {
		return size
	}
	return cursor
}

const (
	runeClassSpace = iota
	runeClassWord
	runeClassOther
)

func runeClass(r rune) int {
	if unicode.IsSpace(r) {
		return runeClassSpace
	}
	if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
		return runeClassWord
	}
	return runeClassOther
}
