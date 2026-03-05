package app

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *uiModel) reviewerInvocationState() (bool, string) {
	if m.engine != nil {
		mode := m.engine.ReviewerFrequency()
		return mode != "off", mode
	}
	mode := strings.ToLower(strings.TrimSpace(m.reviewerMode))
	if mode == "" {
		mode = "off"
	}
	return mode != "off", mode
}

func (m *uiModel) autoCompactionState() bool {
	if m.engine != nil {
		return m.engine.AutoCompactionEnabled()
	}
	return m.autoCompactionEnabled
}

func reviewerToggleStatusMessage(enabled bool, mode string, changed bool) string {
	modeText := strings.ToLower(strings.TrimSpace(mode))
	if modeText == "" {
		modeText = "off"
	}
	if enabled {
		detail := ""
		switch modeText {
		case "all", "edits":
			detail = " (frequency: " + modeText + ")"
		}
		if changed {
			return "Supervisor invocation enabled" + detail
		}
		return "Supervisor invocation already enabled" + detail
	}
	if changed {
		return "Supervisor invocation disabled"
	}
	return "Supervisor invocation already disabled"
}

func autoCompactionToggleStatusMessage(enabled bool, changed bool, compactionMode string) string {
	modeNote := ""
	if strings.EqualFold(strings.TrimSpace(compactionMode), "none") {
		modeNote = " (compaction_mode=none; manual/auto compaction disabled)"
	}
	if enabled {
		if changed {
			return "Auto-compaction enabled" + modeNote
		}
		return "Auto-compaction already enabled" + modeNote
	}
	if changed {
		return "Auto-compaction disabled" + modeNote
	}
	return "Auto-compaction already disabled" + modeNote
}

func (c uiInputController) showTransientStatus(message string) tea.Cmd {
	m := c.model
	m.transientStatusToken++
	token := m.transientStatusToken
	m.transientStatus = strings.TrimSpace(message)
	return tea.Tick(transientStatusDuration, func(time.Time) tea.Msg {
		return clearTransientStatusMsg{token: token}
	})
}
