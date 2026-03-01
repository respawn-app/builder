package app

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"builder/internal/config"
	"builder/internal/runtime"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
)

func TestModeTogglesDoNotEnterAltScreenNative(t *testing.T) {
	out := &bytes.Buffer{}
	model := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIScrollMode(config.TUIScrollModeNative),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "history marker"}}),
	).(*uiModel)
	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 30})
	time.Sleep(20 * time.Millisecond)
	program.Send(tea.KeyMsg{Type: tea.KeyShiftTab})
	time.Sleep(10 * time.Millisecond)
	program.Send(tea.KeyMsg{Type: tea.KeyShiftTab})
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
	if strings.Contains(raw, "\x1b[?1049h") || strings.Contains(raw, "\x1b[?1049l") {
		t.Fatalf("did not expect alt-screen enter/leave sequences, got %q", raw)
	}
	plain := strings.Join(strings.Fields(xansi.Strip(raw)), " ")
	if !strings.Contains(plain, "history marker") {
		t.Fatalf("expected history marker to remain in output after mode toggles, got %q", plain)
	}
}

func TestModeTogglesDoNotEnterAltScreenAltMode(t *testing.T) {
	out := &bytes.Buffer{}
	model := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIScrollMode(config.TUIScrollModeAlt),
	).(*uiModel)
	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 30})
	time.Sleep(20 * time.Millisecond)
	program.Send(tea.KeyMsg{Type: tea.KeyShiftTab})
	time.Sleep(10 * time.Millisecond)
	program.Send(tea.KeyMsg{Type: tea.KeyShiftTab})
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
	if strings.Contains(raw, "\x1b[?1049h") || strings.Contains(raw, "\x1b[?1049l") {
		t.Fatalf("did not expect alt-screen enter/leave sequences in alt config mode, got %q", raw)
	}
}

func TestNativeAlwaysPolicyDisablesAltScreenAndShowsReplayAfterWindowSize(t *testing.T) {
	out := &bytes.Buffer{}
	settings := config.Settings{TUIAlternateScreen: config.TUIAlternateScreenAlways, TUIScrollMode: config.TUIScrollModeNative}
	model := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIScrollMode(config.TUIScrollModeNative),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "startup replay marker"}}),
	).(*uiModel)
	program := tea.NewProgram(model, append(mainUIProgramOptions(settings), tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())...)
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	time.Sleep(25 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 100, Height: 28})
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
	if strings.Contains(raw, "\x1b[?1049h") || strings.Contains(raw, "\x1b[?1049l") {
		t.Fatalf("did not expect alt-screen sequences with always+native override, got %q", raw)
	}
	plain := strings.Join(strings.Fields(xansi.Strip(raw)), " ")
	if !strings.Contains(plain, "startup replay marker") {
		t.Fatalf("expected native replay text after first window size, got %q", plain)
	}
}
