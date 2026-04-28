package app

import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"time"

	"builder/server/tools/askquestion"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
)

func TestTerminalCursorSequencesUseExplicitPlacement(t *testing.T) {
	normal := uiTerminalCursorPlacement{Visible: true, CursorRow: 3, CursorCol: 5, AnchorRow: 9}
	if got, want := terminalCursorRestoreSequence(normal), xansi.CursorDown(6)+"\r"; got != want {
		t.Fatalf("normal restore sequence = %q, want %q", got, want)
	}
	if got, want := terminalCursorPlaceSequence(normal), xansi.ShowCursor+xansi.CursorUp(6)+xansi.CursorRight(5); got != want {
		t.Fatalf("normal place sequence = %q, want %q", got, want)
	}

	alt := uiTerminalCursorPlacement{Visible: true, CursorRow: 4, CursorCol: 7, AnchorRow: 12, AltScreen: true}
	if got, want := terminalCursorRestoreSequence(alt), xansi.CursorPosition(1, 13); got != want {
		t.Fatalf("alt restore sequence = %q, want %q", got, want)
	}
	if got, want := terminalCursorPlaceSequence(alt), xansi.ShowCursor+xansi.CursorPosition(8, 5); got != want {
		t.Fatalf("alt place sequence = %q, want %q", got, want)
	}
}

func TestTerminalCursorWriterRestoresAnchorAroundWrites(t *testing.T) {
	state := newUITerminalCursorState()
	state.Set(uiTerminalCursorPlacement{Visible: true, CursorRow: 2, CursorCol: 4, AnchorRow: 5})

	var out bytes.Buffer
	writer := newUITerminalCursorWriter(&out, state)
	if _, err := writer.Write([]byte("frame")); err != nil {
		t.Fatalf("write: %v", err)
	}
	first := out.String()
	if !strings.HasPrefix(first, "frame") {
		t.Fatalf("first write should not need anchor restore, got %q", first)
	}
	if !strings.HasSuffix(first, xansi.ShowCursor+xansi.CursorUp(3)+xansi.CursorRight(4)) {
		t.Fatalf("first write did not place cursor, got %q", first)
	}

	out.Reset()
	if _, err := writer.Write([]byte("next")); err != nil {
		t.Fatalf("write next: %v", err)
	}
	next := out.String()
	if !strings.HasPrefix(next, xansi.CursorDown(3)+"\rnext") {
		t.Fatalf("next write should restore anchor before payload, got %q", next)
	}
	if !strings.HasSuffix(next, xansi.ShowCursor+xansi.CursorUp(3)+xansi.CursorRight(4)) {
		t.Fatalf("next write did not replace cursor, got %q", next)
	}
}

func TestTerminalCursorWriterPreservesCursorAroundControlWrites(t *testing.T) {
	state := newUITerminalCursorState()
	state.Set(uiTerminalCursorPlacement{Visible: true, CursorRow: 4, CursorCol: 6, AnchorRow: 9})

	var out bytes.Buffer
	writer := newUITerminalCursorWriter(&out, state)
	if _, err := writer.Write([]byte("frame")); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	out.Reset()
	if _, err := writer.Write([]byte("\x1b[?1049h")); err != nil {
		t.Fatalf("write control sequence: %v", err)
	}
	got := out.String()
	if !strings.HasPrefix(got, xansi.CursorDown(5)+"\r\x1b[?1049h") {
		t.Fatalf("control write should restore renderer anchor before payload, got %q", got)
	}
	if !strings.HasSuffix(got, xansi.ShowCursor+xansi.CursorUp(5)+xansi.CursorRight(6)) {
		t.Fatalf("control write should restore terminal cursor after payload, got %q", got)
	}
}

func TestTerminalCursorWriterDoesNotRepositionAfterStop(t *testing.T) {
	state := newUITerminalCursorState()
	state.Set(uiTerminalCursorPlacement{Visible: true, CursorRow: 4, CursorCol: 6, AnchorRow: 9})

	var out bytes.Buffer
	writer := newUITerminalCursorWriter(&out, state)
	if _, err := writer.Write([]byte("frame")); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	state.Stop()
	out.Reset()
	payload := "\x1b[?2004l" + xansi.ShowCursor
	if _, err := writer.Write([]byte(payload)); err != nil {
		t.Fatalf("write cleanup: %v", err)
	}
	if got := out.String(); got != payload {
		t.Fatalf("cleanup write should pass through after cursor stop, got %q want %q", got, payload)
	}
}

func TestUITerminalCursorPlacementTracksWrappedOngoingInputAcrossWidthChanges(t *testing.T) {
	state := newUITerminalCursorState()
	m := newProjectedStaticUIModel(WithUITerminalCursorState(state))
	m.termWidth = 24
	m.termHeight = 12
	m.windowSizeKnown = true
	m.input = "alpha beta gamma delta epsilon"
	m.inputCursor = -1
	m.syncViewport()

	view := m.View()
	assertRenderedLinesFitWidth(t, view, m.termWidth)
	placement, ok := state.Snapshot()
	if !ok {
		t.Fatal("expected visible terminal cursor placement")
	}
	if placement.AltScreen {
		t.Fatal("expected ongoing placement to use normal buffer coordinates")
	}
	if placement.CursorCol >= m.termWidth {
		t.Fatalf("cursor col %d outside width %d", placement.CursorCol, m.termWidth)
	}
	if placement.CursorRow < 0 || placement.CursorRow > placement.AnchorRow {
		t.Fatalf("cursor row should be inside rendered frame, got %+v", placement)
	}

	m.termWidth = 16
	m.syncViewport()
	view = m.View()
	assertRenderedLinesFitWidth(t, view, m.termWidth)
	narrow, ok := state.Snapshot()
	if !ok {
		t.Fatal("expected visible terminal cursor placement after width change")
	}
	if narrow.CursorCol >= m.termWidth {
		t.Fatalf("narrow cursor col %d outside width %d", narrow.CursorCol, m.termWidth)
	}
	if narrow == placement {
		t.Fatalf("expected width change to update cursor placement, before=%+v after=%+v", placement, narrow)
	}
}

func TestUITerminalCursorPlacementTracksWrappedAltScreenInputAcrossWidthChanges(t *testing.T) {
	state := newUITerminalCursorState()
	m := newProjectedStaticUIModel(WithUITerminalCursorState(state))
	m.termWidth = 26
	m.termHeight = 12
	m.windowSizeKnown = true
	m.altScreenActive = true
	m.input = "one two three four five six"
	m.inputCursor = -1
	m.syncViewport()

	view := m.View()
	assertRenderedLinesFitWidth(t, view, m.termWidth)
	placement, ok := state.Snapshot()
	if !ok {
		t.Fatal("expected visible terminal cursor placement")
	}
	if !placement.AltScreen {
		t.Fatal("expected alt-screen placement to use absolute coordinates")
	}

	m.termWidth = 18
	m.syncViewport()
	view = m.View()
	assertRenderedLinesFitWidth(t, view, m.termWidth)
	narrow, ok := state.Snapshot()
	if !ok {
		t.Fatal("expected visible terminal cursor placement after alt-screen width change")
	}
	if !narrow.AltScreen {
		t.Fatal("expected alt-screen placement to remain absolute after width change")
	}
	if narrow.CursorCol >= m.termWidth {
		t.Fatalf("narrow cursor col %d outside width %d", narrow.CursorCol, m.termWidth)
	}
	if narrow == placement {
		t.Fatalf("expected alt-screen width change to update cursor placement, before=%+v after=%+v", placement, narrow)
	}
}

func TestMainInputCursorUsesSharedFieldDisplayWidth(t *testing.T) {
	state := newUITerminalCursorState()
	m := newProjectedStaticUIModel(WithUITerminalCursorState(state))
	m.termWidth = 12
	m.termHeight = 10
	m.windowSizeKnown = true
	m.input = "ab👍cd"
	m.inputCursor = 3
	m.syncViewport()

	cursor := m.layout().inputPaneCursor(m.termWidth)
	if !cursor.Visible {
		t.Fatal("expected main input cursor")
	}
	if cursor.Row != 1 || cursor.Col != 6 {
		t.Fatalf("cursor = %+v, want row 1 col 6", cursor)
	}
	view := m.View()
	assertRenderedLinesFitWidth(t, view, m.termWidth)
	if !strings.Contains(xansi.Strip(view), "› ab👍cd") {
		t.Fatalf("expected input text rendered through shared field, got %q", view)
	}
}

func TestAskInputCursorUsesSharedFieldDisplayWidth(t *testing.T) {
	state := newUITerminalCursorState()
	m := newProjectedStaticUIModel(WithUITerminalCursorState(state))
	m.termWidth = 12
	m.termHeight = 10
	m.windowSizeKnown = true
	reply := make(chan askReply, 1)
	testSetActiveAsk(m, &askEvent{req: askquestion.Request{Question: "Question?"}, reply: reply})
	m.ask.input = "ab👍cd"
	m.ask.inputCursor = 3
	m.syncViewport()

	cursor := m.layout().inputPaneCursor(m.termWidth)
	if !cursor.Visible {
		t.Fatal("expected ask input cursor")
	}
	if cursor.Row != 2 || cursor.Col != 6 {
		t.Fatalf("cursor = %+v, want row 2 col 6", cursor)
	}
	view := m.View()
	assertRenderedLinesFitWidth(t, view, m.termWidth)
	if !strings.Contains(xansi.Strip(view), "› ab👍cd") {
		t.Fatalf("expected ask input text rendered through shared field, got %q", view)
	}
}

func TestTerminalCursorHiddenWhenInputLocked(t *testing.T) {
	state := newUITerminalCursorState()
	m := newProjectedStaticUIModel(WithUITerminalCursorState(state))
	m.termWidth = 24
	m.termHeight = 10
	m.windowSizeKnown = true
	m.inputSubmitLocked = true
	m.input = "locked"
	m.syncViewport()

	view := m.View()
	assertRenderedLinesFitWidth(t, view, m.termWidth)
	if _, ok := state.Snapshot(); ok {
		t.Fatal("did not expect real terminal cursor placement while input is locked")
	}
}

func TestViewDoesNotAppendHideCursorWhenRealTerminalCursorVisible(t *testing.T) {
	state := newUITerminalCursorState()
	m := newProjectedStaticUIModel(WithUITerminalCursorState(state))
	m.termWidth = 24
	m.termHeight = 10
	m.windowSizeKnown = true
	m.input = "visible cursor"
	m.syncViewport()

	view := m.View()
	assertRenderedLinesFitWidth(t, view, m.termWidth)
	if strings.Contains(view, ansiHideCursor) {
		t.Fatalf("did not expect view to hide terminal cursor when real cursor is active: %q", view)
	}
	if _, ok := state.Snapshot(); !ok {
		t.Fatal("expected real cursor placement")
	}
}

func TestTerminalCursorPlacementAccountsForTailTrimmedStatusLine(t *testing.T) {
	state := newUITerminalCursorState()
	m := newProjectedStaticUIModel(WithUITerminalCursorState(state))
	layout := m.layout()
	frame := uiRenderFrame{
		width:      12,
		height:     3,
		chatPanel:  []string{"chat 1", "chat 2", "chat 3"},
		inputPane:  []string{"input 1", "input 2"},
		statusLine: "status",
		tailOnly:   true,
		inputCursor: uiInputFieldCursor{
			Visible: true,
			Row:     0,
			Col:     4,
		},
	}

	view := layout.renderFrame(frame)
	if strings.Contains(view, ansiHideCursor) {
		t.Fatalf("did not expect hidden cursor in real-cursor frame: %q", view)
	}
	placement, ok := state.Snapshot()
	if !ok {
		t.Fatal("expected visible terminal cursor placement")
	}
	if placement.CursorRow != 0 {
		t.Fatalf("cursor row = %d, want 0 after tail trim with status line", placement.CursorRow)
	}
	if placement.AnchorRow != 2 {
		t.Fatalf("anchor row = %d, want 2", placement.AnchorRow)
	}
	if placement.CursorCol != 4 {
		t.Fatalf("cursor col = %d, want 4", placement.CursorCol)
	}
	lines := strings.Split(view, "\n")
	if got, want := lines, []string{"input 1", "input 2", "status"}; !slices.Equal(got, want) {
		t.Fatalf("rendered lines = %#v, want %#v", got, want)
	}
}

func TestSoftCursorOverlayPreservesColumnAfterTrimmedTrailingSpaces(t *testing.T) {
	rendered := overlayCursorOnLine("› abc", 7, 10, lipgloss.NewStyle().Reverse(true))
	if !strings.HasPrefix(rendered, "› abc  ") {
		t.Fatalf("expected cursor overlay to preserve target column, got %q", rendered)
	}
}

func TestSharedFieldRenderingPreservesExplicitTrailingSpaces(t *testing.T) {
	rendered := renderEditableInputField(10, 1, uiEditableInputRenderSpec{
		Prefix:       "› ",
		Text:         "abc  ",
		CursorIndex:  -1,
		RenderCursor: true,
	})
	if got, want := rendered.Lines[0], "› abc     "; got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
	if rendered.Cursor.Col != 7 {
		t.Fatalf("cursor col = %d, want 7", rendered.Cursor.Col)
	}
}

func TestTerminalCursorProgramTracksWrappedInputAndResize(t *testing.T) {
	state := newUITerminalCursorState()
	model := newProjectedStaticUIModel(WithUITerminalCursorState(state))
	model.input = "alpha beta gamma delta epsilon zeta"
	model.inputCursor = -1

	var out bytes.Buffer
	program := tea.NewProgram(
		model,
		tea.WithInput(strings.NewReader("")),
		tea.WithOutput(newUITerminalCursorWriter(&out, state)),
		tea.WithoutSignals(),
	)
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	defer program.Quit()

	program.Send(tea.WindowSizeMsg{Width: 30, Height: 14})
	waitForTestCondition(t, 2*time.Second, "initial cursor placement", func() bool {
		placement, ok := state.Snapshot()
		return ok && placement.CursorCol < 30 && !placement.AltScreen
	})
	first, _ := state.Snapshot()

	program.Send(tea.WindowSizeMsg{Width: 18, Height: 14})
	waitForTestCondition(t, 2*time.Second, "resized cursor placement", func() bool {
		placement, ok := state.Snapshot()
		return ok && placement.CursorCol < 18 && placement != first
	})
	program.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}

	if !strings.Contains(out.String(), xansi.ShowCursor) {
		t.Fatalf("expected program output to show native cursor, got %q", out.String())
	}
}

func TestTerminalCursorProgramSurvivesAltScreenTransitionAfterPlacement(t *testing.T) {
	state := newUITerminalCursorState()
	model := newProjectedStaticUIModel(
		WithUITerminalCursorState(state),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "history marker"}}),
	)
	model.input = "wrapped input before alt transition"
	model.inputCursor = -1

	var out bytes.Buffer
	program := tea.NewProgram(
		model,
		tea.WithInput(strings.NewReader("")),
		tea.WithOutput(newUITerminalCursorWriter(&out, state)),
		tea.WithoutSignals(),
	)
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	defer program.Quit()

	program.Send(tea.WindowSizeMsg{Width: 28, Height: 14})
	waitForTestCondition(t, 2*time.Second, "cursor placement before alt transition", func() bool {
		_, ok := state.Snapshot()
		return ok
	})
	program.Send(tea.KeyMsg{Type: tea.KeyShiftTab})
	waitForTestCondition(t, 2*time.Second, "detail alt-screen active", func() bool {
		return model.view.Mode() == "detail" && model.altScreenActive
	})
	program.Send(tea.KeyMsg{Type: tea.KeyShiftTab})
	waitForTestCondition(t, 2*time.Second, "ongoing mode restored", func() bool {
		return model.view.Mode() == "ongoing" && !model.altScreenActive
	})
	program.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}

	raw := out.String()
	if !strings.Contains(raw, "\x1b[?1049h") || !strings.Contains(raw, "\x1b[?1049l") {
		t.Fatalf("expected alt-screen enter/exit in output, got %q", raw)
	}
	if !strings.Contains(strings.Join(strings.Fields(xansi.Strip(raw)), " "), "history marker") {
		t.Fatalf("expected output to remain coherent across alt-screen transition, got %q", raw)
	}
}

func assertRenderedLinesFitWidth(t *testing.T, view string, width int) {
	t.Helper()
	for index, line := range strings.Split(strings.TrimSuffix(view, ansiHideCursor), "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("rendered line %d width = %d, want <= %d: %q", index, got, width, line)
		}
	}
}
