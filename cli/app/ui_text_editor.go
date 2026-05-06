package app

import (
	"strings"

	tuiinput "builder/cli/tui/input"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

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

func newSingleLineEditor(value string) tuiinput.Editor {
	editor := tuiinput.NewEditor()
	editor.Replace(singleLineText(value))
	return editor
}

func singleLineEditorValue(editor tuiinput.Editor) string {
	return editor.Text()
}

func setSingleLineEditorValue(editor *tuiinput.Editor, value string) {
	editor.Replace(singleLineText(value))
}

func singleLineEditorPosition(editor tuiinput.Editor) int {
	return runeOffsetForByteCursor(editor.Text(), editor.Cursor())
}

func setSingleLineEditorPosition(editor *tuiinput.Editor, cursor int) {
	editor.SetCursor(byteOffsetForRuneCursor(editor.Text(), cursor))
}

func moveSingleLineEditorEnd(editor *tuiinput.Editor) {
	editor.SetCursor(len(editor.Text()))
}

func updateSingleLineEditorWithAppKeys(editor *tuiinput.Editor, msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	text := editor.Text()
	cursor := singleLineEditorPosition(*editor)
	killBuffer := editor.KillBuffer()
	apply := func(updated string, nextCursor int, nextKill string) {
		editor.Replace(singleLineText(updated))
		editor.SetCursor(byteOffsetForRuneCursor(editor.Text(), nextCursor))
		editor.SetKillBuffer(nextKill)
	}
	if handleSharedInputEditKey(key, uiSharedInputEditActions{
		Backspace: func() bool {
			updated, nextCursor, changed := backspaceBuffer(text, cursor)
			if changed {
				apply(updated, nextCursor, killBuffer)
			}
			return changed
		},
		DeleteForward: func() bool {
			updated, nextCursor, changed := deleteForwardBuffer(text, cursor)
			if changed {
				apply(updated, nextCursor, killBuffer)
			}
			return changed
		},
		DeleteBackwardWord: func() bool {
			updated, nextCursor, nextKill, changed := deleteBackwardWordBuffer(text, cursor, killBuffer)
			if changed {
				apply(updated, nextCursor, nextKill)
			}
			return changed
		},
		DeleteForwardWord: func() bool {
			updated, nextCursor, nextKill, changed := deleteForwardWordBuffer(text, cursor, killBuffer)
			if changed {
				apply(updated, nextCursor, nextKill)
			}
			return changed
		},
		KillToLineStart: func() bool {
			updated, nextCursor, nextKill, changed := killToLineStartBuffer(text, cursor, killBuffer)
			if changed {
				apply(updated, nextCursor, nextKill)
			}
			return changed
		},
		KillToLineEnd: func() bool {
			updated, nextCursor, nextKill, changed := killToLineEndBuffer(text, cursor, killBuffer)
			if changed {
				apply(updated, nextCursor, nextKill)
			}
			return changed
		},
		Yank: func() bool {
			updated, nextCursor, changed := yankBuffer(text, cursor, killBuffer)
			if changed {
				apply(updated, nextCursor, killBuffer)
			}
			return changed
		},
		DeleteCurrentLine: func() bool {
			updated, nextCursor, changed := deleteCurrentBufferLine(text, cursor)
			if changed {
				apply(updated, nextCursor, killBuffer)
			}
			return changed
		},
	}) {
		return nil
	}
	switch key.Type {
	case tea.KeySpace:
		updated, nextCursor, changed := insertBufferRunes(text, cursor, []rune{' '})
		if changed {
			apply(updated, nextCursor, killBuffer)
		}
	case tea.KeyLeft:
		if key.Alt {
			setSingleLineEditorPosition(editor, moveBufferCursorWordLeft(text, cursor))
		} else {
			setSingleLineEditorPosition(editor, moveBufferCursorLeft(text, cursor))
		}
	case tea.KeyRight:
		if key.Alt {
			setSingleLineEditorPosition(editor, moveBufferCursorWordRight(text, cursor))
		} else {
			setSingleLineEditorPosition(editor, moveBufferCursorRight(text, cursor))
		}
	case tea.KeyHome, tea.KeyCtrlA:
		setSingleLineEditorPosition(editor, moveBufferCursorStart())
	case tea.KeyEnd, tea.KeyCtrlE, tea.KeyCtrlEnd:
		moveSingleLineEditorEnd(editor)
	case tea.KeyCtrlLeft:
		setSingleLineEditorPosition(editor, moveBufferCursorWordLeft(text, cursor))
	case tea.KeyCtrlRight:
		setSingleLineEditorPosition(editor, moveBufferCursorWordRight(text, cursor))
	case tea.KeyRunes:
		updated, nextCursor, changed := insertBufferRunes(text, cursor, singleLineRunes(key.Runes))
		if changed {
			apply(updated, nextCursor, killBuffer)
		}
	}
	return nil
}

func renderSingleLineEditor(width int, maxContentLines int, editor tuiinput.Editor, prefix string, renderCursor bool, mask rune, placeholder string) tuiinput.RenderResult {
	field := tuiinput.NewField()
	field.Editor = editor
	field.Prefix = prefix
	field.MaxLines = maxContentLines
	field.Cursor = renderCursor
	field.Mask = mask
	field.Placeholder = placeholder
	return field.Render(width)
}

func renderSingleLineEditorSoftCursorLines(width int, maxContentLines int, editor tuiinput.Editor, prefix string, renderCursor bool, lineStyle lipgloss.Style, mask rune, placeholder string) []string {
	return renderEditableInputSoftCursorLines(width, renderSingleLineEditor(width, maxContentLines, editor, prefix, renderCursor, mask, placeholder), lineStyle)
}

func renderSingleLineEditorFramedSoftCursorLines(width int, maxContentLines int, editor tuiinput.Editor, prefix string, renderCursor bool, lineStyle lipgloss.Style, borderStyle lipgloss.Style, mask rune, placeholder string) []string {
	border := borderStyle.Render(strings.Repeat("─", max(0, width)))
	lines := renderSingleLineEditorSoftCursorLines(width, maxContentLines, editor, prefix, renderCursor, lineStyle, mask, placeholder)
	out := make([]string, 0, len(lines)+2)
	out = append(out, border)
	out = append(out, lines...)
	out = append(out, border)
	return out
}
