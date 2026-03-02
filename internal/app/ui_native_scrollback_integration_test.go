package app

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"builder/internal/config"
	"builder/internal/runtime"
	"builder/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
)

func closedRuntimeEvents() <-chan runtime.Event {
	ch := make(chan runtime.Event)
	close(ch)
	return ch
}

func closedAskEvents() <-chan askEvent {
	ch := make(chan askEvent)
	close(ch)
	return ch
}

func normalizedOutput(v string) string {
	return strings.Join(strings.Fields(xansi.Strip(v)), " ")
}

func TestNativeScrollbackProgramOutputContract(t *testing.T) {
	out := &bytes.Buffer{}
	model := NewUIModel(
		nil,
		closedRuntimeEvents(),
		closedAskEvents(),
		WithUIScrollMode(config.TUIScrollModeNative),
		WithUIInitialTranscript([]UITranscriptEntry{
			{Role: "user", Text: "first replay line"},
			{Role: "assistant", Text: "second replay line"},
		}),
	).(*uiModel)

	program := tea.NewProgram(
		model,
		tea.WithInput(strings.NewReader("")),
		tea.WithOutput(out),
		tea.WithoutSignals(),
	)

	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()

	time.Sleep(40 * time.Millisecond)
	program.Send(nativeHistoryFlushMsg{Text: "delta replay line"})
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 32})
	time.Sleep(20 * time.Millisecond)
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
	normalized := normalizedOutput(raw)
	if strings.Contains(raw, "\x1b[2J") {
		t.Fatalf("did not expect clear-screen sequence in native mode output")
	}
	if strings.Contains(raw, "\x1b[?1049h") || strings.Contains(raw, "\x1b[?1049l") {
		t.Fatalf("did not expect alt-screen enter/leave sequences in native mode output")
	}
	if strings.Contains(raw, "\x1b[?1000h") || strings.Contains(raw, "\x1b[?1002h") || strings.Contains(raw, "\x1b[?1003h") || strings.Contains(raw, "\x1b[?1006h") {
		t.Fatalf("did not expect mouse-capture enable sequences in native mode output")
	}
	if strings.Count(normalized, "first replay line") != 1 {
		t.Fatalf("expected startup replay line exactly once, got %d", strings.Count(normalized, "first replay line"))
	}
	if strings.Count(normalized, "delta replay line") != 1 {
		t.Fatalf("expected delta replay exactly once, got %d", strings.Count(normalized, "delta replay line"))
	}
	if strings.Contains(raw, strings.Repeat(" ", 400)) {
		t.Fatalf("expected native mode to avoid frame-sized whitespace rewrites")
	}
	plain := xansi.Strip(raw)
	if occurrences := strings.Count(plain, "ongoing | "); occurrences > 12 {
		t.Fatalf("expected bounded status redraw output, got %d occurrences", occurrences)
	}
}

func TestNativeSubmitAndFlushDoesNotDuplicateStatusLines(t *testing.T) {
	out := &bytes.Buffer{}
	model := NewUIModel(
		nil,
		closedRuntimeEvents(),
		closedAskEvents(),
		WithUIScrollMode(config.TUIScrollModeNative),
	).(*uiModel)

	program := tea.NewProgram(
		model,
		tea.WithInput(strings.NewReader("")),
		tea.WithOutput(out),
		tea.WithoutSignals(),
	)

	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()

	time.Sleep(40 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 32})
	program.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line one")})
	program.Send(tea.KeyMsg{Type: tea.KeyCtrlJ})
	program.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line two")})
	program.Send(tea.KeyMsg{Type: tea.KeyEnter})
	time.Sleep(50 * time.Millisecond)
	program.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("next input")})
	time.Sleep(20 * time.Millisecond)
	program.Send(nativeHistoryFlushMsg{Text: "post-submit replay\n"})
	time.Sleep(20 * time.Millisecond)
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
	normalized := normalizedOutput(raw)
	plain := xansi.Strip(raw)
	if strings.Count(normalized, "post-submit replay") != 1 {
		t.Fatalf("expected post-submit flush exactly once, got %d", strings.Count(normalized, "post-submit replay"))
	}
	for _, line := range strings.Split(plain, "\n") {
		if strings.Count(line, "ongoing | ") > 1 {
			t.Fatalf("expected no duplicated status segment in a single rendered line, got %q", line)
		}
	}
	if occurrences := strings.Count(plain, "ongoing | "); occurrences > 16 {
		t.Fatalf("expected bounded status redraw count after submit+flush, got %d", occurrences)
	}
}

func TestNativeReplayOutputContainsMarkdownStyling(t *testing.T) {
	out := &bytes.Buffer{}
	model := NewUIModel(
		nil,
		closedRuntimeEvents(),
		closedAskEvents(),
		WithUIScrollMode(config.TUIScrollModeNative),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "**bold** and `code`"}}),
	).(*uiModel)
	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 32})
	time.Sleep(40 * time.Millisecond)
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
	if !strings.Contains(raw, "\x1b[") {
		t.Fatalf("expected ansi styling sequences in native replay output, got %q", raw)
	}
	if strings.Contains(raw, "**bold**") {
		t.Fatalf("expected markdown transformed in replay output, got literal markdown: %q", raw)
	}
	plain := normalizedOutput(raw)
	if !strings.Contains(plain, "bold") || !strings.Contains(plain, "code") {
		t.Fatalf("expected styled replay to include content, got %q", plain)
	}
}

func TestNativeStreamingPreviewOutputCoherentUnderControlChars(t *testing.T) {
	out := &bytes.Buffer{}
	model := NewUIModel(
		nil,
		closedRuntimeEvents(),
		closedAskEvents(),
		WithUIScrollMode(config.TUIScrollModeNative),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "prompt once"}}),
	).(*uiModel)
	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 32})
	program.Send(tui.StreamAssistantMsg{Delta: "line1\r\nline2\x1b[2K\nline3\x1b[?25l"})
	time.Sleep(40 * time.Millisecond)
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
	plain := xansi.Strip(raw)
	if strings.Count(normalizedOutput(raw), "prompt once") != 1 {
		t.Fatalf("expected committed prompt once in output, got %d", strings.Count(normalizedOutput(raw), "prompt once"))
	}
	if strings.Contains(plain, "[2K") || strings.Contains(plain, "[?25l") {
		t.Fatalf("expected no ansi escape remnants in streaming preview, got %q", plain)
	}
	if !strings.Contains(plain, "line1") || !strings.Contains(plain, "line2") || !strings.Contains(plain, "line3") {
		t.Fatalf("expected coherent streaming preview content, got %q", plain)
	}
}
