package app

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"builder/cli/tui"
	"builder/server/llm"

	bubblespinner "github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

type uiInputController struct {
	model *uiModel
}

var pendingToolSpinner = bubblespinner.Dot
var spinnerTickInterval = pendingToolSpinner.FPS
var transientStatusDuration = 8 * time.Second
var scheduleTransientStatusClear = func(token uint64) tea.Cmd {
	return tea.Tick(transientStatusDuration, func(time.Time) tea.Msg {
		return clearTransientStatusMsg{token: token}
	})
}
var processListRefreshInterval = 1500 * time.Millisecond
var errSubmissionInterrupted = errors.New("interrupted")
var rollbackDoubleEscWindow = 500 * time.Millisecond
var csiShiftEnterDedupWindow = 120 * time.Millisecond

func waitProcessListRefresh() tea.Cmd {
	return tea.Tick(processListRefreshInterval, func(time.Time) tea.Msg {
		return processListRefreshTickMsg{}
	})
}

func tickSpinner(token uint64) tea.Cmd {
	return tea.Tick(spinnerTickInterval, func(time.Time) tea.Msg {
		return spinnerTickMsg{token: token}
	})
}

func (m *uiModel) shouldAnimateSpinner() bool {
	if m == nil {
		return false
	}
	return m.busy || m.processListHasRunningEntries()
}

func (m *uiModel) ensureSpinnerTicking() tea.Cmd {
	if m == nil {
		return nil
	}
	if !m.shouldAnimateSpinner() {
		m.stopSpinnerTicking()
		return nil
	}
	if m.spinnerTickToken != 0 {
		return nil
	}
	m.spinnerGeneration++
	m.spinnerTickToken = m.spinnerGeneration
	if m.spinnerTickToken == 0 {
		m.spinnerGeneration++
		m.spinnerTickToken = m.spinnerGeneration
	}
	return tickSpinner(m.spinnerTickToken)
}

func (m *uiModel) stopSpinnerTicking() {
	if m == nil {
		return
	}
	m.spinnerTickToken = 0
}

func formatSubmissionError(err error) string {
	if err == nil {
		return ""
	}
	if formatted := llm.UserFacingError(err); strings.TrimSpace(formatted) != "" {
		return formatted
	}
	var statusErr *llm.APIStatusError
	if errors.As(err, &statusErr) {
		body := statusErr.Body
		if strings.TrimSpace(body) == "" {
			body = "<empty error body>"
		}
		return fmt.Sprintf("openai status %d\nresponse body:\n%s", statusErr.StatusCode, body)
	}
	return err.Error()
}

func parseUserShellCommand(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "$") {
		return "", false
	}
	command := strings.TrimSpace(strings.TrimPrefix(trimmed, "$"))
	if command == "" {
		return "", false
	}
	return command, true
}

func (m *uiModel) appendLocalEntry(role, text string) {
	if text == "" {
		return
	}
	if m.hasRuntimeClient() {
		_ = m.appendRuntimeLocalEntry(role, text)
		return
	}
	m.forwardToView(tui.AppendTranscriptMsg{Role: role, Text: text})
}
