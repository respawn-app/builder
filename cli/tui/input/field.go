package input

import "strings"

type Field struct {
	Editor      Editor
	Prefix      string
	Placeholder string
	MaxLines    int
	Framed      bool
	Mask        rune
	Cursor      bool
}

type RenderResult struct {
	Lines  []string
	Width  int
	Cursor FieldCursor
}

type FieldCursor struct {
	Visible bool
	Row     int
	Col     int
}

func NewField() Field {
	return Field{Cursor: true}
}

func (f *Field) Render(width int) RenderResult {
	if width < 1 {
		width = 1
	}
	contentWidth := width
	text, displayCursor := f.displayTextAndCursorOffset()
	renderText := f.Prefix + text
	cursorOffset := len(f.Prefix) + displayCursor
	if f.Editor.Text() == "" && f.Placeholder != "" {
		renderText = f.Prefix + f.Placeholder
		cursorOffset = len(f.Prefix)
	}

	renderEditor := NewEditor()
	renderEditor.Replace(renderText)
	renderEditor.SetCursor(cursorOffset)
	wrapped := renderEditor.WrappedLines(contentWidth)
	cursorPos := renderEditor.CursorPosition(contentWidth)

	visibleStart := visibleLineStart(len(wrapped), f.maxContentLines(), cursorPos.Line)
	visibleEnd := visibleStart + f.maxContentLines()
	if visibleEnd > len(wrapped) {
		visibleEnd = len(wrapped)
	}
	lines := make([]string, 0, visibleEnd-visibleStart+2)
	for _, line := range wrapped[visibleStart:visibleEnd] {
		lines = append(lines, padDisplayRight(renderText[line.Start:line.End], contentWidth))
	}
	cursor := FieldCursor{}
	if f.Cursor {
		cursorLine := cursorPos.Line - visibleStart
		if cursorLine >= 0 && cursorLine < len(lines) {
			cursor.Visible = true
			cursor.Row = cursorLine
			cursor.Col = normalizeCursorCol(cursorPos.Col, contentWidth)
		}
	}

	if f.Framed {
		border := strings.Repeat("-", width)
		lines = append([]string{border}, append(lines, border)...)
		if cursor.Visible {
			cursor.Row++
		}
	}

	return RenderResult{Lines: lines, Width: width, Cursor: cursor}
}

func (f Field) displayTextAndCursorOffset() (string, int) {
	text := f.Editor.Text()
	cursor := f.Editor.Cursor()
	if f.Mask == 0 {
		return text, cursor
	}
	var out strings.Builder
	cursorOffset := 0
	for _, cluster := range graphemes(text) {
		start := out.Len()
		if cluster.text == "\n" {
			out.WriteByte('\n')
		} else {
			out.WriteRune(f.Mask)
		}
		if cluster.end <= cursor {
			cursorOffset += out.Len() - start
		}
	}
	return out.String(), cursorOffset
}

func (f Field) maxContentLines() int {
	if f.MaxLines < 1 {
		return 1 << 30
	}
	return f.MaxLines
}

func visibleLineStart(totalLines int, maxLines int, cursorLine int) int {
	if maxLines < 1 || totalLines <= maxLines {
		return 0
	}
	start := cursorLine - maxLines + 1
	if start < 0 {
		return 0
	}
	maxStart := totalLines - maxLines
	if start > maxStart {
		return maxStart
	}
	return start
}

func normalizeCursorCol(col int, width int) int {
	if width < 1 {
		return 0
	}
	if col < 0 {
		return 0
	}
	if col >= width {
		return width - 1
	}
	return col
}

func padDisplayRight(text string, width int) string {
	current := displayWidth(text)
	if current >= width {
		return text
	}
	return text + strings.Repeat(" ", width-current)
}
