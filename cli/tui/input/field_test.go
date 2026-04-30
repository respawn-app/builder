package input

import "testing"

func TestFieldRenderReturnsWidthSafeLinesAndCursor(t *testing.T) {
	field := NewField()
	field.Prefix = "› "
	field.Editor.Replace("alpha beta gamma")

	result := field.Render(8)
	if result.Width != 8 {
		t.Fatalf("width = %d, want 8", result.Width)
	}
	if len(result.Lines) != 3 {
		t.Fatalf("lines = %+v, want 3 lines", result.Lines)
	}
	for index, line := range result.Lines {
		if got := displayWidth(line); got != 8 {
			t.Fatalf("line %d width = %d, want 8: %q", index, got, line)
		}
	}
	if !result.Cursor.Visible {
		t.Fatal("expected cursor visible")
	}
	if result.Cursor.Row != 2 || result.Cursor.Col != 2 {
		t.Fatalf("cursor = %+v, want row 2 col 2", result.Cursor)
	}
}

func TestFieldRenderTracksCursorViewport(t *testing.T) {
	field := NewField()
	field.Prefix = "› "
	field.MaxLines = 2
	field.Editor.Replace("one two three four")

	result := field.Render(8)
	if len(result.Lines) != 2 {
		t.Fatalf("lines = %+v, want 2 visible lines", result.Lines)
	}
	if result.Cursor.Row != 1 {
		t.Fatalf("cursor row = %d, want last visible row", result.Cursor.Row)
	}
	if result.Lines[0] == "› one   " {
		t.Fatalf("expected viewport to follow cursor, got %+v", result.Lines)
	}
}

func TestFieldRenderFrameOffsetsCursor(t *testing.T) {
	field := NewField()
	field.Framed = true
	field.Prefix = "› "
	field.Editor.Replace("hello")

	result := field.Render(10)
	if len(result.Lines) != 3 {
		t.Fatalf("framed lines = %+v, want 3", result.Lines)
	}
	if result.Cursor.Row != 1 || result.Cursor.Col != displayWidth("› hello") {
		t.Fatalf("framed cursor = %+v", result.Cursor)
	}
}

func TestFieldRenderMasksTextAndPreservesCursor(t *testing.T) {
	field := NewField()
	field.Prefix = "key: "
	field.Mask = '*'
	field.Editor.Replace("secret")

	result := field.Render(12)
	if got := result.Lines[0]; got != "key: ****** " {
		t.Fatalf("masked line = %q", got)
	}
	if result.Cursor.Col != len("key: ******") {
		t.Fatalf("cursor col = %d", result.Cursor.Col)
	}
}

func TestFieldRenderMasksGraphemesAndMapsCursorToMaskedText(t *testing.T) {
	field := NewField()
	field.Prefix = "key: "
	field.Mask = '*'
	field.Editor.Replace("a👍e\u0301")
	field.Editor.SetCursor(len("a👍"))

	result := field.Render(10)
	if got := result.Lines[0]; got != "key: ***  " {
		t.Fatalf("masked line = %q", got)
	}
	if result.Cursor.Col != len("key: **") {
		t.Fatalf("cursor col = %d", result.Cursor.Col)
	}
}

func TestFieldRenderPlaceholderDoesNotMoveCursorPastPrompt(t *testing.T) {
	field := NewField()
	field.Prefix = "› "
	field.Placeholder = "type here"

	result := field.Render(12)
	if got := result.Lines[0]; got != "› type here " {
		t.Fatalf("placeholder line = %q", got)
	}
	if result.Cursor.Col != displayWidth("› ") {
		t.Fatalf("placeholder cursor col = %d, want %d", result.Cursor.Col, displayWidth("› "))
	}
}
