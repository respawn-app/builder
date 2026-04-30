package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type uiSharedTextInput struct {
	text        string
	cursor      int
	focused     bool
	mask        rune
	placeholder string
	killBuffer  string
}

func newUISharedTextInput(value string) uiSharedTextInput {
	input := uiSharedTextInput{cursor: -1}
	input.SetValue(value)
	return input
}

func (i uiSharedTextInput) Value() string {
	return i.text
}

func (i *uiSharedTextInput) SetValue(value string) {
	i.text = singleLineText(value)
	i.cursor = -1
}

func (i *uiSharedTextInput) SetPlaceholder(placeholder string) {
	i.placeholder = placeholder
}

func (i uiSharedTextInput) Position() int {
	return bufferCursorIndex(i.text, i.cursor)
}

func (i *uiSharedTextInput) SetPosition(cursor int) {
	i.cursor = clampCursor(cursor, len([]rune(i.text)))
}

func (i *uiSharedTextInput) CursorEnd() {
	i.cursor = -1
}

func (i *uiSharedTextInput) Focus() {
	i.focused = true
}

func (i *uiSharedTextInput) Blur() {
	i.focused = false
}

func (i uiSharedTextInput) Focused() bool {
	return i.focused
}

func (i *uiSharedTextInput) SetPasswordMode(enabled bool) {
	if enabled {
		i.mask = '*'
		return
	}
	i.mask = 0
}

func (i *uiSharedTextInput) Update(msg tea.Msg) tea.Cmd {
	if !i.focused {
		return nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if handleSharedInputEditKey(key, uiSharedInputEditActions{
		Backspace:          func() bool { return i.applyEdit(backspaceBuffer) },
		DeleteForward:      i.deleteForward,
		DeleteBackwardWord: i.deleteBackwardWord,
		DeleteForwardWord:  i.deleteForwardWord,
		KillToLineStart:    i.killToLineStart,
		KillToLineEnd:      i.killToLineEnd,
		Yank:               i.yank,
		DeleteCurrentLine:  i.deleteCurrentLine,
	}) {
		return nil
	}
	switch key.Type {
	case tea.KeySpace:
		i.insertRunes([]rune{' '})
	case tea.KeyLeft:
		if key.Alt {
			i.cursor = moveBufferCursorWordLeft(i.text, i.cursor)
		} else {
			i.cursor = moveBufferCursorLeft(i.text, i.cursor)
		}
	case tea.KeyRight:
		if key.Alt {
			i.cursor = moveBufferCursorWordRight(i.text, i.cursor)
		} else {
			i.cursor = moveBufferCursorRight(i.text, i.cursor)
		}
	case tea.KeyHome, tea.KeyCtrlA:
		i.cursor = moveBufferCursorStart()
	case tea.KeyEnd, tea.KeyCtrlE, tea.KeyCtrlEnd:
		i.cursor = moveBufferCursorEnd()
	case tea.KeyCtrlLeft:
		i.cursor = moveBufferCursorWordLeft(i.text, i.cursor)
	case tea.KeyCtrlRight:
		i.cursor = moveBufferCursorWordRight(i.text, i.cursor)
	case tea.KeyRunes:
		i.insertRunes(key.Runes)
	}
	return nil
}

func (i *uiSharedTextInput) insertRunes(runes []rune) {
	updated, cursor, ok := insertBufferRunes(i.text, i.cursor, singleLineRunes(runes))
	if !ok {
		return
	}
	i.text = singleLineText(updated)
	i.cursor = clampCursor(cursor, len([]rune(i.text)))
}

func (i *uiSharedTextInput) applyEdit(edit func(string, int) (string, int, bool)) bool {
	updated, cursor, ok := edit(i.text, i.cursor)
	if !ok {
		return false
	}
	i.text = singleLineText(updated)
	i.cursor = clampCursor(cursor, len([]rune(i.text)))
	return true
}

func (i *uiSharedTextInput) deleteForward() bool {
	editor := bufferEditor(i.text, i.cursor)
	if !editor.DeleteForward() {
		return false
	}
	i.text = singleLineText(editor.Text())
	i.cursor = runeOffsetForByteCursor(i.text, editor.Cursor())
	return true
}

func (i *uiSharedTextInput) deleteBackwardWord() bool {
	updated, cursor, killBuffer, ok := deleteBackwardWordBuffer(i.text, i.cursor, i.killBuffer)
	if !ok {
		return false
	}
	i.text = singleLineText(updated)
	i.cursor = clampCursor(cursor, len([]rune(i.text)))
	i.killBuffer = killBuffer
	return true
}

func (i *uiSharedTextInput) deleteForwardWord() bool {
	updated, cursor, killBuffer, ok := deleteForwardWordBuffer(i.text, i.cursor, i.killBuffer)
	if !ok {
		return false
	}
	i.text = singleLineText(updated)
	i.cursor = clampCursor(cursor, len([]rune(i.text)))
	i.killBuffer = killBuffer
	return true
}

func (i *uiSharedTextInput) killToLineStart() bool {
	updated, cursor, killBuffer, ok := killToLineStartBuffer(i.text, i.cursor, i.killBuffer)
	if !ok {
		return false
	}
	i.text = singleLineText(updated)
	i.cursor = clampCursor(cursor, len([]rune(i.text)))
	i.killBuffer = killBuffer
	return true
}

func (i *uiSharedTextInput) killToLineEnd() bool {
	updated, cursor, killBuffer, ok := killToLineEndBuffer(i.text, i.cursor, i.killBuffer)
	if !ok {
		return false
	}
	i.text = singleLineText(updated)
	i.cursor = clampCursor(cursor, len([]rune(i.text)))
	i.killBuffer = killBuffer
	return true
}

func (i *uiSharedTextInput) yank() bool {
	updated, cursor, ok := yankBuffer(i.text, i.cursor, i.killBuffer)
	if !ok {
		return false
	}
	i.text = singleLineText(updated)
	i.cursor = clampCursor(cursor, len([]rune(i.text)))
	return true
}

func (i *uiSharedTextInput) deleteCurrentLine() bool {
	updated, cursor, ok := deleteCurrentBufferLine(i.text, i.cursor)
	if !ok {
		return false
	}
	i.text = singleLineText(updated)
	i.cursor = clampCursor(cursor, len([]rune(i.text)))
	return true
}

func (i uiSharedTextInput) renderSoftCursorLines(width int, maxContentLines int, prefix string, renderCursor bool, lineStyle lipgloss.Style) []string {
	return renderEditableInputSoftCursorFieldLines(width, maxContentLines, i.renderSpec(prefix, renderCursor), lineStyle)
}

func (i uiSharedTextInput) renderFramedSoftCursorLines(width int, maxContentLines int, prefix string, renderCursor bool, lineStyle lipgloss.Style, borderStyle lipgloss.Style) []string {
	return renderFramedLines(width, i.renderSoftCursorLines(width, maxContentLines, prefix, renderCursor, lineStyle), borderStyle)
}

func (i uiSharedTextInput) renderSpec(prefix string, renderCursor bool) uiEditableInputRenderSpec {
	return uiEditableInputRenderSpec{
		Prefix:       prefix,
		Text:         i.text,
		CursorIndex:  i.cursor,
		RenderCursor: renderCursor,
		Mask:         i.mask,
		Placeholder:  i.placeholder,
	}
}

func singleLineText(text string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(text)
}

func singleLineRunes(runes []rune) []rune {
	out := make([]rune, 0, len(runes))
	for _, r := range runes {
		if r == '\n' || r == '\r' {
			continue
		}
		out = append(out, r)
	}
	return out
}
