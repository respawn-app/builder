package app

import "unicode"

func (m *uiModel) inputRunes() []rune {
	return []rune(m.input)
}

func (m *uiModel) cursorIndex() int {
	return bufferCursorIndex(m.input, m.inputCursor)
}

func (m *uiModel) clearInput() {
	m.input = ""
	m.inputCursor = -1
	m.resetPromptHistoryNavigation()
	m.refreshSlashCommandFilterFromInput()
}

func (m *uiModel) insertInputRunes(chars []rune) {
	updated, nextCursor, ok := insertBufferRunes(m.input, m.inputCursor, chars)
	if !ok {
		return
	}
	m.input = updated
	m.inputCursor = nextCursor
	m.syncPromptHistorySelectionToInput()
	m.refreshSlashCommandFilterFromInput()
}

func (m *uiModel) backspaceInput() bool {
	updated, nextCursor, ok := backspaceBuffer(m.input, m.inputCursor)
	if !ok {
		return false
	}
	m.input = updated
	m.inputCursor = nextCursor
	m.syncPromptHistorySelectionToInput()
	m.refreshSlashCommandFilterFromInput()
	return true
}

func (m *uiModel) moveCursorLeft() {
	m.inputCursor = moveBufferCursorLeft(m.input, m.inputCursor)
}

func (m *uiModel) moveCursorRight() {
	m.inputCursor = moveBufferCursorRight(m.input, m.inputCursor)
}

func (m *uiModel) moveCursorStart() {
	m.inputCursor = moveBufferCursorStart()
}

func (m *uiModel) moveCursorEnd() {
	m.inputCursor = moveBufferCursorEnd()
}

func (m *uiModel) moveCursorWordLeft() {
	m.inputCursor = moveBufferCursorWordLeft(m.input, m.inputCursor)
}

func (m *uiModel) moveCursorWordRight() {
	m.inputCursor = moveBufferCursorWordRight(m.input, m.inputCursor)
}

func (m *uiModel) moveCursorUpLine() bool {
	nextCursor, moved := moveBufferCursorUpLine(m.input, m.inputCursor)
	m.inputCursor = nextCursor
	return moved
}

func (m *uiModel) moveCursorDownLine() bool {
	nextCursor, moved := moveBufferCursorDownLine(m.input, m.inputCursor)
	m.inputCursor = nextCursor
	return moved
}

func (m *uiModel) deleteCurrentInputLine() bool {
	updated, nextCursor, ok := deleteCurrentBufferLine(m.input, m.inputCursor)
	if !ok {
		return false
	}
	m.input = updated
	m.inputCursor = nextCursor
	m.syncPromptHistorySelectionToInput()
	m.refreshSlashCommandFilterFromInput()
	return true
}

func (m *uiModel) askCursorIndex() int {
	return bufferCursorIndex(m.ask.input, m.ask.inputCursor)
}

func (m *uiModel) clearAskInput() {
	m.ask.input = ""
	m.ask.inputCursor = -1
}

func (m *uiModel) insertAskInputRunes(chars []rune) {
	updated, nextCursor, ok := insertBufferRunes(m.ask.input, m.ask.inputCursor, chars)
	if !ok {
		return
	}
	m.ask.input = updated
	m.ask.inputCursor = nextCursor
}

func (m *uiModel) backspaceAskInput() bool {
	updated, nextCursor, ok := backspaceBuffer(m.ask.input, m.ask.inputCursor)
	if !ok {
		return false
	}
	m.ask.input = updated
	m.ask.inputCursor = nextCursor
	return true
}

func (m *uiModel) moveAskCursorLeft() {
	m.ask.inputCursor = moveBufferCursorLeft(m.ask.input, m.ask.inputCursor)
}

func (m *uiModel) moveAskCursorRight() {
	m.ask.inputCursor = moveBufferCursorRight(m.ask.input, m.ask.inputCursor)
}

func (m *uiModel) moveAskCursorStart() {
	m.ask.inputCursor = moveBufferCursorStart()
}

func (m *uiModel) moveAskCursorEnd() {
	m.ask.inputCursor = moveBufferCursorEnd()
}

func (m *uiModel) moveAskCursorWordLeft() {
	m.ask.inputCursor = moveBufferCursorWordLeft(m.ask.input, m.ask.inputCursor)
}

func (m *uiModel) moveAskCursorWordRight() {
	m.ask.inputCursor = moveBufferCursorWordRight(m.ask.input, m.ask.inputCursor)
}

func (m *uiModel) moveAskCursorUpLine() bool {
	nextCursor, moved := moveBufferCursorUpLine(m.ask.input, m.ask.inputCursor)
	m.ask.inputCursor = nextCursor
	return moved
}

func (m *uiModel) moveAskCursorDownLine() bool {
	nextCursor, moved := moveBufferCursorDownLine(m.ask.input, m.ask.inputCursor)
	m.ask.inputCursor = nextCursor
	return moved
}

func (m *uiModel) deleteCurrentAskInputLine() bool {
	updated, nextCursor, ok := deleteCurrentBufferLine(m.ask.input, m.ask.inputCursor)
	if !ok {
		return false
	}
	m.ask.input = updated
	m.ask.inputCursor = nextCursor
	return true
}

func bufferCursorIndex(text string, cursor int) int {
	return clampCursor(cursor, len([]rune(text)))
}

func insertBufferRunes(text string, cursor int, chars []rune) (string, int, bool) {
	if len(chars) == 0 {
		return text, cursor, false
	}
	filtered, _ := stripMouseSGRRunes(chars)
	if len(filtered) == 0 {
		return text, cursor, false
	}
	runes := []rune(text)
	cursor = clampCursor(cursor, len(runes))
	updated := make([]rune, 0, len(runes)+len(filtered))
	updated = append(updated, runes[:cursor]...)
	updated = append(updated, filtered...)
	updated = append(updated, runes[cursor:]...)
	nextCursor := cursor + len(filtered)
	cleaned, cleanedCursor, _ := stripMouseSGRRunesWithCursor(updated, nextCursor)
	return string(cleaned), cleanedCursor, true
}

func backspaceBuffer(text string, cursor int) (string, int, bool) {
	runes := []rune(text)
	cursor = clampCursor(cursor, len(runes))
	if cursor == 0 {
		return text, cursor, false
	}
	updated := make([]rune, 0, len(runes)-1)
	updated = append(updated, runes[:cursor-1]...)
	updated = append(updated, runes[cursor:]...)
	return string(updated), cursor - 1, true
}

func moveBufferCursorLeft(text string, cursor int) int {
	cursor = bufferCursorIndex(text, cursor)
	if cursor > 0 {
		return cursor - 1
	}
	return cursor
}

func moveBufferCursorRight(text string, cursor int) int {
	runes := []rune(text)
	cursor = clampCursor(cursor, len(runes))
	if cursor < len(runes) {
		return cursor + 1
	}
	return cursor
}

func moveBufferCursorStart() int {
	return 0
}

func moveBufferCursorEnd() int {
	return -1
}

func moveBufferCursorWordLeft(text string, cursor int) int {
	runes := []rune(text)
	return prevWordBoundary(runes, clampCursor(cursor, len(runes)))
}

func moveBufferCursorWordRight(text string, cursor int) int {
	runes := []rune(text)
	return nextWordBoundary(runes, clampCursor(cursor, len(runes)))
}

func moveBufferCursorUpLine(text string, cursor int) (int, bool) {
	runes := []rune(text)
	cursor = clampCursor(cursor, len(runes))
	currentStart := lineStart(runes, cursor)
	currentCol := cursor - currentStart
	if currentStart == 0 {
		return 0, cursor != 0
	}
	prevEnd := currentStart - 1
	prevStart := lineStart(runes, prevEnd)
	prevLen := prevEnd - prevStart
	newCursor := prevStart + min(currentCol, prevLen)
	return newCursor, newCursor != cursor
}

func moveBufferCursorDownLine(text string, cursor int) (int, bool) {
	runes := []rune(text)
	cursor = clampCursor(cursor, len(runes))
	currentStart := lineStart(runes, cursor)
	currentCol := cursor - currentStart
	currentEnd := lineEnd(runes, cursor)
	if currentEnd >= len(runes) {
		return len(runes), cursor != len(runes)
	}
	nextStart := currentEnd + 1
	nextEnd := lineEnd(runes, nextStart)
	nextLen := nextEnd - nextStart
	newCursor := nextStart + min(currentCol, nextLen)
	return newCursor, newCursor != cursor
}

func deleteCurrentBufferLine(text string, cursor int) (string, int, bool) {
	runes := []rune(text)
	if len(runes) == 0 {
		return "", 0, false
	}
	cursorIndex := clampCursor(cursor, len(runes))
	start := lineStart(runes, cursorIndex)
	end := lineEnd(runes, cursorIndex)

	deleteStart := start
	deleteEnd := end
	if end < len(runes) && runes[end] == '\n' {
		deleteEnd = end + 1
	} else if start > 0 && runes[start-1] == '\n' {
		deleteStart = start - 1
	}

	if deleteStart >= deleteEnd {
		return text, cursorIndex, false
	}

	updated := make([]rune, 0, len(runes)-(deleteEnd-deleteStart))
	updated = append(updated, runes[:deleteStart]...)
	updated = append(updated, runes[deleteEnd:]...)
	return string(updated), deleteStart, true
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
