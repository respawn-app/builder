package app

import (
	"strings"

	"builder/cli/tui"
	"builder/shared/clientui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const noGoalHint = "No goal to manage yet. First, start a goal with /goal <objective>"

func (c uiInputController) handleGoalCommand(mode string, objective string) (tea.Model, tea.Cmd) {
	m := c.model
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "show", "":
		if m.busy {
			errText := busyGoalCommandMessage("show")
			return m, c.appendErrorFeedbackWithStatus(errText, c.showErrorStatus(errText))
		}
		return m, c.startGoalFlowCmd()
	case "set":
		if m.busy {
			errText := busyGoalCommandMessage("set")
			return m, c.appendErrorFeedbackWithStatus(errText, c.showErrorStatus(errText))
		}
		current, err := m.showRuntimeGoal()
		if err != nil {
			detailErr := formatSubmissionError(err)
			return m, c.appendErrorFeedbackWithStatus(detailErr, c.showErrorStatus(detailErr))
		}
		if goalIsActive(current) {
			m.openGoalConfirmOverlay("replace", current, objective, nil)
			if overlayCmd := m.pushGoalOverlayIfNeeded(); overlayCmd != nil {
				return m, overlayCmd
			}
			return m, nil
		}
		goal, err := m.setRuntimeGoal(objective)
		if err != nil {
			detailErr := formatSubmissionError(err)
			return m, c.appendErrorFeedbackWithStatus(detailErr, c.showErrorStatus(detailErr))
		}
		return m, c.appendSystemFeedbackWithStatus(goalStatusNotice("Goal set", goal), c.showSuccessStatus("Goal set"))
	case "pause":
		goal, err := m.pauseRuntimeGoal()
		if err != nil {
			detailErr := formatSubmissionError(err)
			return m, c.appendErrorFeedbackWithStatus(detailErr, c.showErrorStatus(detailErr))
		}
		return m, c.appendSystemFeedbackWithStatus(goalStatusNotice("Goal paused", goal), c.showSuccessStatus("Goal paused"))
	case "resume":
		if m.busy {
			errText := busyGoalCommandMessage("resume")
			return m, c.appendErrorFeedbackWithStatus(errText, c.showErrorStatus(errText))
		}
		goal, err := m.resumeRuntimeGoal()
		if err != nil {
			detailErr := formatSubmissionError(err)
			return m, c.appendErrorFeedbackWithStatus(detailErr, c.showErrorStatus(detailErr))
		}
		return m, c.appendSystemFeedbackWithStatus(goalStatusNotice("Goal resumed", goal), c.showSuccessStatus("Goal resumed"))
	case "clear":
		current, err := m.showRuntimeGoal()
		if err != nil {
			detailErr := formatSubmissionError(err)
			return m, c.appendErrorFeedbackWithStatus(detailErr, c.showErrorStatus(detailErr))
		}
		if goalRequiresClearConfirmation(current) {
			m.openGoalConfirmOverlay("clear", current, "", nil)
			if overlayCmd := m.pushGoalOverlayIfNeeded(); overlayCmd != nil {
				return m, overlayCmd
			}
			return m, nil
		}
		_, err = m.clearRuntimeGoal()
		if err != nil {
			detailErr := formatSubmissionError(err)
			return m, c.appendErrorFeedbackWithStatus(detailErr, c.showErrorStatus(detailErr))
		}
		return m, c.appendSystemFeedbackWithStatus("Goal cleared", c.showSuccessStatus("Goal cleared"))
	default:
		errText := "Usage: /goal [pause|resume|clear|<objective>]"
		return m, c.appendErrorFeedbackWithStatus(errText, c.showErrorStatus(errText))
	}
}

func busyGoalCommandMessage(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "show":
		return "cannot show /goal while model is working"
	case "resume":
		return "cannot resume /goal while model is working"
	default:
		return "cannot set /goal while model is working"
	}
}

func goalIsActive(goal *clientui.RuntimeGoal) bool {
	return goal != nil && strings.TrimSpace(goal.Status) == "active"
}

func goalRequiresClearConfirmation(goal *clientui.RuntimeGoal) bool {
	return goalIsActive(goal) && !goal.Suspended
}

func goalStatusNotice(prefix string, goal *clientui.RuntimeGoal) string {
	if goal == nil || strings.TrimSpace(goal.Status) == "" {
		return prefix
	}
	return prefix + " (" + strings.TrimSpace(goal.Status) + ")"
}

func (c uiInputController) startGoalFlowCmd() tea.Cmd {
	m := c.model
	goal, err := m.showRuntimeGoal()
	m.openGoalOverlay(goal, err)
	if overlayCmd := m.pushGoalOverlayIfNeeded(); overlayCmd != nil {
		return overlayCmd
	}
	return nil
}

func (c uiInputController) stopGoalFlowCmd() tea.Cmd {
	m := c.model
	overlayCmd := m.popGoalOverlayIfNeeded()
	m.closeGoalOverlay()
	return overlayCmd
}

func (c uiInputController) handleGoalOverlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m := c.model
	if strings.TrimSpace(m.goal.confirmMode) != "" {
		return c.handleGoalConfirmKey(msg)
	}
	switch strings.ToLower(msg.String()) {
	case "ctrl+c":
		if m.busy {
			c.interruptBusyRuntime()
			return m, nil
		}
		m.exitAction = UIActionExit
		if overlayCmd := m.popGoalOverlayIfNeeded(); overlayCmd != nil {
			m.closeGoalOverlay()
			return m, tea.Sequence(overlayCmd, tea.Quit)
		}
		return m, tea.Quit
	case "esc", "q":
		return m, c.stopGoalFlowCmd()
	case "up":
		m.moveGoalScroll(-1)
	case "down":
		m.moveGoalScroll(1)
	case "pgup":
		m.moveGoalScrollPage(-1)
	case "pgdown":
		m.moveGoalScrollPage(1)
	case "home":
		m.goal.scroll = 0
	case "end":
		m.goal.scroll = 1 << 30
	}
	return m, nil
}

func (c uiInputController) handleGoalConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m := c.model
	switch strings.ToLower(msg.String()) {
	case "ctrl+c":
		if m.busy {
			c.interruptBusyRuntime()
			return m, nil
		}
		m.exitAction = UIActionExit
		if overlayCmd := m.popGoalOverlayIfNeeded(); overlayCmd != nil {
			m.closeGoalOverlay()
			return m, tea.Sequence(overlayCmd, tea.Quit)
		}
		return m, tea.Quit
	case "esc", "q", "n":
		return m, c.stopGoalFlowCmd()
	case "tab", "left", "right", "up", "down":
		m.toggleGoalConfirmSelection()
		return m, nil
	case "enter", "y":
		if strings.ToLower(msg.String()) == "y" {
			m.goal.confirmSelection = goalConfirmSelectionConfirm
		}
		if m.goal.confirmSelection != goalConfirmSelectionConfirm {
			return m, c.stopGoalFlowCmd()
		}
		mode := strings.TrimSpace(m.goal.confirmMode)
		objective := m.goal.pendingObjective
		switch mode {
		case "replace":
			goal, err := m.setRuntimeGoal(objective)
			if err != nil {
				m.goal.error = err.Error()
				return m, nil
			}
			overlayCmd := c.stopGoalFlowCmd()
			return m, sequenceCmds(overlayCmd, c.appendSystemFeedbackWithStatus(goalStatusNotice("Goal set", goal), c.showSuccessStatus("Goal set")))
		case "clear":
			if _, err := m.clearRuntimeGoal(); err != nil {
				m.goal.error = err.Error()
				return m, nil
			}
			overlayCmd := c.stopGoalFlowCmd()
			return m, sequenceCmds(overlayCmd, c.appendSystemFeedbackWithStatus("Goal cleared", c.showSuccessStatus("Goal cleared")))
		}
	}
	return m, nil
}

func (m *uiModel) openGoalOverlay(goal *clientui.RuntimeGoal, err error) {
	m.goal.open = true
	m.goal.scroll = 0
	m.goal.goal = cloneRuntimeGoal(goal)
	m.goal.error = ""
	if err != nil {
		m.goal.error = err.Error()
	}
	m.setInputMode(uiInputModeGoal)
}

func (m *uiModel) openGoalConfirmOverlay(mode string, goal *clientui.RuntimeGoal, pendingObjective string, err error) {
	m.openGoalOverlay(goal, err)
	m.goal.confirmMode = strings.TrimSpace(mode)
	m.goal.confirmSelection = goalConfirmSelectionCancel
	m.goal.pendingObjective = strings.TrimSpace(pendingObjective)
}

func (m *uiModel) closeGoalOverlay() {
	m.goal = uiGoalOverlayState{}
	m.restorePrimaryInputMode()
}

func (m *uiModel) pushGoalOverlayIfNeeded() tea.Cmd {
	if m.goal.ownsTranscriptMode {
		return nil
	}
	if m.view.Mode() != tui.ModeOngoing {
		return nil
	}
	m.goal.ownsTranscriptMode = true
	if transitionCmd := m.transitionTranscriptMode(tui.ModeDetail, true, true); transitionCmd != nil {
		return transitionCmd
	}
	return tea.ClearScreen
}

func (m *uiModel) popGoalOverlayIfNeeded() tea.Cmd {
	if !m.goal.ownsTranscriptMode {
		return nil
	}
	m.goal.ownsTranscriptMode = false
	if m.view.Mode() != tui.ModeDetail {
		return nil
	}
	if transitionCmd := m.transitionTranscriptMode(tui.ModeOngoing, false, true); transitionCmd != nil {
		return transitionCmd
	}
	return tea.ClearScreen
}

func (m *uiModel) moveGoalScroll(delta int) {
	m.goal.scroll += delta
	if m.goal.scroll < 0 {
		m.goal.scroll = 0
	}
}

func (m *uiModel) moveGoalScrollPage(deltaPages int) {
	height := m.termHeight
	if height < 1 {
		height = 1
	}
	m.moveGoalScroll(deltaPages * max(1, height-4))
}

const (
	goalConfirmSelectionCancel = iota
	goalConfirmSelectionConfirm
)

func (m *uiModel) toggleGoalConfirmSelection() {
	if m.goal.confirmSelection == goalConfirmSelectionConfirm {
		m.goal.confirmSelection = goalConfirmSelectionCancel
		return
	}
	m.goal.confirmSelection = goalConfirmSelectionConfirm
}

func (l uiViewLayout) renderGoalOverlay(width, height int, _ uiStyles) []string {
	if width < 1 || height < 1 {
		return []string{padRight("", width)}
	}
	content := l.goalOverlayContentLines(width)
	maxScroll := max(0, len(content)-height)
	if l.model.goal.scroll > maxScroll {
		l.model.goal.scroll = maxScroll
	}
	if l.model.goal.scroll < 0 {
		l.model.goal.scroll = 0
	}
	start := l.model.goal.scroll
	end := min(len(content), start+height)
	visible := append([]string(nil), content[start:end]...)
	for len(visible) < height {
		visible = append(visible, padRight("", width))
	}
	return visible
}

func (l uiViewLayout) goalOverlayContentLines(width int) []string {
	m := l.model
	palette := uiPalette(m.theme)
	titleStyle := lipgloss.NewStyle().Foreground(palette.primary).Bold(true)
	boldStyle := lipgloss.NewStyle().Bold(true)
	subtleStyle := lipgloss.NewStyle().Foreground(palette.muted).Faint(true)
	warningStyle := lipgloss.NewStyle().Foreground(statusAmberColor()).Bold(true)
	lines := make([]string, 0, 24)
	appendWrapped := func(text string, lineStyle lipgloss.Style) {
		wrapped := wrapLine(strings.TrimRight(text, " \t"), width)
		if len(wrapped) == 0 {
			lines = append(lines, padRight("", width))
			return
		}
		for _, line := range wrapped {
			lines = append(lines, padANSIRight(lineStyle.Render(line), width))
		}
	}
	appendGap := func() {
		if len(lines) > 0 {
			lines = append(lines, padRight("", width))
		}
	}

	appendWrapped("Goal", titleStyle)
	if strings.TrimSpace(m.goal.confirmMode) != "" {
		return l.goalConfirmContentLines(width, titleStyle, boldStyle, subtleStyle, warningStyle)
	}
	if strings.TrimSpace(m.goal.error) != "" {
		appendGap()
		appendWrapped("Could not load goal: "+m.goal.error, warningStyle)
		return lines
	}
	if m.goal.goal == nil {
		appendGap()
		appendWrapped(noGoalHint, subtleStyle)
		return lines
	}
	goal := m.goal.goal
	appendGap()
	appendWrapped("Status: "+strings.TrimSpace(goal.Status), boldStyle)
	if strings.TrimSpace(goal.ID) != "" {
		appendWrapped("ID: "+strings.TrimSpace(goal.ID), subtleStyle)
	}
	appendGap()
	appendWrapped("Objective", titleStyle)
	appendWrapped(goal.Objective, lipgloss.Style{})
	appendGap()
	appendWrapped("Esc/q closes. /goal pause, /goal resume, /goal clear manage lifecycle.", subtleStyle)
	return lines
}

func (l uiViewLayout) goalConfirmContentLines(width int, titleStyle, boldStyle, subtleStyle, warningStyle lipgloss.Style) []string {
	m := l.model
	lines := make([]string, 0, 24)
	appendWrapped := func(text string, lineStyle lipgloss.Style) {
		wrapped := wrapLine(strings.TrimRight(text, " \t"), width)
		if len(wrapped) == 0 {
			lines = append(lines, padRight("", width))
			return
		}
		for _, line := range wrapped {
			lines = append(lines, padANSIRight(lineStyle.Render(line), width))
		}
	}
	appendGap := func() {
		if len(lines) > 0 {
			lines = append(lines, padRight("", width))
		}
	}

	appendWrapped("Goal", titleStyle)
	appendGap()
	if strings.TrimSpace(m.goal.error) != "" {
		appendWrapped("Could not update goal: "+m.goal.error, warningStyle)
		appendGap()
	}
	switch strings.TrimSpace(m.goal.confirmMode) {
	case "replace":
		appendWrapped("Replace active goal?", boldStyle)
		if m.goal.goal != nil {
			appendWrapped("Current: "+m.goal.goal.Objective, lipgloss.Style{})
		}
		appendWrapped("New: "+m.goal.pendingObjective, lipgloss.Style{})
	case "clear":
		appendWrapped("Clear active goal?", boldStyle)
		if m.goal.goal != nil {
			appendWrapped(m.goal.goal.Objective, lipgloss.Style{})
		}
	default:
		appendWrapped("Confirm goal action?", boldStyle)
	}
	appendGap()
	cancel := "Cancel"
	confirm := "Confirm"
	if m.goal.confirmSelection == goalConfirmSelectionCancel {
		cancel = "> " + cancel
		confirm = "  " + confirm
	} else {
		cancel = "  " + cancel
		confirm = "> " + confirm
	}
	appendWrapped(cancel+"    "+confirm, subtleStyle)
	appendWrapped("Tab/arrows toggle. Enter selects. Esc cancels.", subtleStyle)
	return lines
}
