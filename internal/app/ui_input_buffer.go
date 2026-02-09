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
	m.refreshSlashCommandFilterFromInput()
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
	m.refreshSlashCommandFilterFromInput()
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
	m.refreshSlashCommandFilterFromInput()
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

func (m *uiModel) moveCursorUpLine() bool {
	runes := m.inputRunes()
	cursor := clampCursor(m.inputCursor, len(runes))
	currentStart := lineStart(runes, cursor)
	currentCol := cursor - currentStart
	if currentStart == 0 {
		m.inputCursor = 0
		return cursor != 0
	}
	prevEnd := currentStart - 1
	prevStart := lineStart(runes, prevEnd)
	prevLen := prevEnd - prevStart
	newCursor := prevStart + min(currentCol, prevLen)
	m.inputCursor = newCursor
	return newCursor != cursor
}

func (m *uiModel) moveCursorDownLine() bool {
	runes := m.inputRunes()
	cursor := clampCursor(m.inputCursor, len(runes))
	currentStart := lineStart(runes, cursor)
	currentCol := cursor - currentStart
	currentEnd := lineEnd(runes, cursor)
	if currentEnd >= len(runes) {
		m.inputCursor = len(runes)
		return cursor != len(runes)
	}
	nextStart := currentEnd + 1
	nextEnd := lineEnd(runes, nextStart)
	nextLen := nextEnd - nextStart
	newCursor := nextStart + min(currentCol, nextLen)
	m.inputCursor = newCursor
	return newCursor != cursor
}

func (m *uiModel) deleteCurrentInputLine() bool {
	runes := m.inputRunes()
	if len(runes) == 0 {
		return false
	}
	cursor := clampCursor(m.inputCursor, len(runes))
	start := lineStart(runes, cursor)
	end := lineEnd(runes, cursor)

	deleteStart := start
	deleteEnd := end
	if end < len(runes) && runes[end] == '\n' {
		deleteEnd = end + 1
	} else if start > 0 && runes[start-1] == '\n' {
		deleteStart = start - 1
	}

	if deleteStart >= deleteEnd {
		return false
	}

	updated := make([]rune, 0, len(runes)-(deleteEnd-deleteStart))
	updated = append(updated, runes[:deleteStart]...)
	updated = append(updated, runes[deleteEnd:]...)
	m.input = string(updated)
	m.inputCursor = deleteStart
	m.refreshSlashCommandFilterFromInput()
	return true
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

func lineStart(runes []rune, cursor int) int {
	i := clampCursor(cursor, len(runes))
	for i > 0 && runes[i-1] != '\n' {
		i--
	}
	return i
}

func lineEnd(runes []rune, cursor int) int {
	i := clampCursor(cursor, len(runes))
	for i < len(runes) && runes[i] != '\n' {
		i++
	}
	return i
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
