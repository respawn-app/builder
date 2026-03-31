package app

import (
	"strconv"
	"strings"

	"builder/cli/app/commands"
	"builder/server/runtime"
	"builder/shared/clientui"

	tea "github.com/charmbracelet/bubbletea"
)

func (c uiInputController) applyCommandResult(commandResult commands.Result) (tea.Model, tea.Cmd) {
	m := c.model
	if commandResult.SubmitUser && commandResult.FreshConversation && m.currentConversationFreshness() != clientui.ConversationFreshnessFresh {
		m.nextSessionInitialPrompt = commandResult.User
		m.nextParentSessionID = m.sessionID
		m.exitAction = UIActionNewSession
		return m, tea.Quit
	}
	if commandResult.SubmitUser {
		return m, c.startSubmission(commandResult.User)
	}
	if commandResult.Text != "" {
		m.appendLocalEntry("system", commandResult.Text)
	}

	switch commandResult.Action {
	case commands.ActionExit:
		m.exitAction = UIActionExit
		return m, tea.Quit
	case commands.ActionNew:
		m.nextParentSessionID = m.sessionID
		m.exitAction = UIActionNewSession
		return m, tea.Quit
	case commands.ActionResume:
		m.exitAction = UIActionResume
		return m, tea.Quit
	case commands.ActionBack:
		return c.handleBackCommand()
	case commands.ActionLogout:
		m.exitAction = UIActionLogout
		return m, tea.Quit
	case commands.ActionSetName:
		return c.handleSessionNameCommand(commandResult.SessionName)
	case commands.ActionSetThinking:
		return c.handleThinkingLevelCommand(commandResult.ThinkingLevel)
	case commands.ActionSetFast:
		return c.handleFastModeCommand(commandResult.FastMode)
	case commands.ActionSetSupervisor:
		return c.handleSupervisorModeCommand(commandResult.SupervisorMode)
	case commands.ActionSetAutoCompaction:
		return c.handleAutoCompactionCommand(commandResult.AutoCompactionMode)
	case commands.ActionCompact:
		return m, c.startCompaction(commandResult.Args)
	case commands.ActionStatus:
		return m, c.startStatusFlowCmd()
	case commands.ActionProcesses:
		args := strings.Fields(strings.TrimSpace(commandResult.Args))
		if len(args) == 0 {
			return m, c.startProcessListFlowCmd()
		}
		action := strings.ToLower(strings.TrimSpace(args[0]))
		id := ""
		if len(args) > 1 {
			id = strings.TrimSpace(args[1])
		}
		return c.runProcessAction(action, id)
	}
	return m, nil
}

func (c uiInputController) handleBackCommand() (tea.Model, tea.Cmd) {
	m := c.model
	status := m.runtimeStatus()
	if strings.TrimSpace(status.ParentSessionID) == "" {
		m.appendLocalEntry("system", "No parent session available")
		return m, nil
	}
	m.nextSessionInitialInput = m.backTeleportInput()
	m.nextSessionID = strings.TrimSpace(status.ParentSessionID)
	m.exitAction = UIActionOpenSession
	return m, tea.Quit
}

func (m *uiModel) backTeleportInput() string {
	return m.runtimeStatus().LastCommittedAssistantFinalAnswer
}

func (c uiInputController) handleSessionNameCommand(sessionName string) (tea.Model, tea.Cmd) {
	m := c.model
	if err := m.setRuntimeSessionName(sessionName); err != nil {
		m.appendLocalEntry("error", formatSubmissionError(err))
		return m, nil
	}
	m.sessionName = strings.TrimSpace(sessionName)
	return m, tea.SetWindowTitle(m.windowTitle())
}

func (c uiInputController) handleThinkingLevelCommand(requested string) (tea.Model, tea.Cmd) {
	m := c.model
	requested = strings.TrimSpace(requested)
	if requested == "" {
		current := strings.TrimSpace(m.thinkingLevel)
		if m.hasRuntimeClient() {
			current = m.runtimeStatus().ThinkingLevel
		}
		if current == "" {
			current = "unknown"
		}
		m.appendLocalEntry("system", "Thinking level is "+current)
		return m, nil
	}

	normalized, ok := runtime.NormalizeThinkingLevel(requested)
	if !ok {
		errText := "invalid thinking level " + strconv.Quote(requested) + " (expected low|medium|high|xhigh)"
		m.appendLocalEntry("error", errText)
		return m, nil
	}
	if err := m.setRuntimeThinkingLevel(normalized); err != nil {
		m.appendLocalEntry("error", formatSubmissionError(err))
		return m, nil
	}
	if m.hasRuntimeClient() {
		m.thinkingLevel = m.runtimeStatus().ThinkingLevel
		m.appendLocalEntry("system", "Thinking level set to "+m.thinkingLevel)
		return m, nil
	}
	m.thinkingLevel = normalized
	m.appendLocalEntry("system", "Thinking level set to "+m.thinkingLevel)
	return m, nil
}

func (c uiInputController) handleFastModeCommand(requested string) (tea.Model, tea.Cmd) {
	m := c.model
	available, currentEnabled := m.fastModeState()
	if !available {
		errText := "Fast mode is only available for OpenAI-based Responses providers"
		m.appendLocalEntry("error", errText)
		return m, c.showErrorStatus(errText)
	}

	requested = strings.ToLower(strings.TrimSpace(requested))
	switch requested {
	case "status":
		status := "off"
		if currentEnabled {
			status = "on"
		}
		m.appendLocalEntry("system", "Fast mode is "+status)
		return m, nil
	case "", "on", "off":
		// supported
	default:
		errText := "Usage: /fast [on|off|status]"
		m.appendLocalEntry("error", errText)
		return m, c.showErrorStatus(errText)
	}

	targetEnabled := currentEnabled
	switch requested {
	case "":
		targetEnabled = !currentEnabled
	case "on":
		targetEnabled = true
	case "off":
		targetEnabled = false
	}

	changed := currentEnabled != targetEnabled
	if m.hasRuntimeClient() {
		var err error
		changed, err = m.setRuntimeFastModeEnabled(targetEnabled)
		if err != nil {
			detailErr := formatSubmissionError(err)
			m.appendLocalEntry("error", detailErr)
			return m, c.showErrorStatus(detailErr)
		}
		m.fastModeEnabled = m.runtimeStatus().FastModeEnabled
	} else {
		m.fastModeEnabled = targetEnabled
	}

	status := fastModeToggleStatusMessage(m.fastModeEnabled, changed)
	m.appendLocalEntry("system", status)
	return m, c.showSuccessStatus(status)
}

func (c uiInputController) handleSupervisorModeCommand(requested string) (tea.Model, tea.Cmd) {
	m := c.model
	requested = strings.ToLower(strings.TrimSpace(requested))
	currentEnabled, currentMode := m.reviewerInvocationState()
	targetEnabled := currentEnabled
	switch requested {
	case "":
		targetEnabled = !currentEnabled
	case "on":
		targetEnabled = true
	case "off":
		targetEnabled = false
	default:
		errText := "invalid supervisor mode " + strconv.Quote(requested) + " (expected on|off)"
		m.appendLocalEntry("error", errText)
		return m, nil
	}

	changed := false
	nextMode := currentMode
	if m.hasRuntimeClient() {
		var err error
		changed, nextMode, err = m.setRuntimeReviewerEnabled(targetEnabled)
		if err != nil {
			m.appendLocalEntry("error", formatSubmissionError(err))
			return m, nil
		}
	} else {
		nextMode = "off"
		if targetEnabled {
			nextMode = "edits"
		}
		changed = currentEnabled != targetEnabled
	}
	m.reviewerMode = nextMode
	m.reviewerEnabled = nextMode != "off"
	status := reviewerToggleStatusMessage(m.reviewerEnabled, nextMode, changed)
	m.appendLocalEntry("system", status)
	return m, c.showTransientStatus(status)
}

func (c uiInputController) handleAutoCompactionCommand(requested string) (tea.Model, tea.Cmd) {
	m := c.model
	requested = strings.ToLower(strings.TrimSpace(requested))
	currentEnabled := m.autoCompactionState()
	currentCompactionMode := "native"
	if m.hasRuntimeClient() {
		currentCompactionMode = m.runtimeStatus().CompactionMode
	}
	targetEnabled := currentEnabled
	switch requested {
	case "":
		targetEnabled = !currentEnabled
	case "on":
		targetEnabled = true
	case "off":
		targetEnabled = false
	default:
		errText := "invalid autocompaction mode " + strconv.Quote(requested) + " (expected on|off)"
		m.appendLocalEntry("error", errText)
		return m, nil
	}

	changed := false
	nextEnabled := currentEnabled
	if m.hasRuntimeClient() {
		changed, nextEnabled = m.setRuntimeAutoCompactionEnabled(targetEnabled)
	} else {
		nextEnabled = targetEnabled
		changed = currentEnabled != targetEnabled
	}
	m.autoCompactionEnabled = nextEnabled
	status := autoCompactionToggleStatusMessage(nextEnabled, changed, currentCompactionMode)
	m.appendLocalEntry("system", status)
	return m, c.showTransientStatus(status)
}
