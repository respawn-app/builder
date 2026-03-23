package app

import (
	"fmt"
	"strings"

	"builder/internal/app/commands"

	tea "github.com/charmbracelet/bubbletea"
)

func (c uiInputController) handleQueuedSlashCommandInput(text string) (bool, tea.Model, tea.Cmd) {
	m := c.model
	selection := m.resolveSlashCommandSelection(text)
	if selection.shouldAutocomplete() {
		m.input = selection.autocompleteText()
		m.inputCursor = -1
		m.refreshSlashCommandFilterFromInput()
		return true, m, nil
	}
	if !selection.hasCommand || selection.commandText() == "" {
		return false, m, nil
	}
	if errText, blocked := m.blockedDeferredSlashCommand(selection.commandText()); blocked {
		m.appendLocalEntry("error", errText)
		return true, m, c.showErrorStatus(errText)
	}
	next, cmd := c.queueOrStartSubmission(selection.commandText())
	return true, next, cmd
}

func (c uiInputController) handleEnteredSlashCommandInput(text string) (bool, tea.Model, tea.Cmd) {
	m := c.model
	selection := m.resolveSlashCommandSelection(text)
	if !selection.hasCommand {
		return false, m, nil
	}
	commandText := selection.commandText()
	if commandText == "" {
		return false, m, nil
	}
	command := selection.command
	if m.busy && !command.RunWhileBusy {
		m.clearInput()
		return true, m, c.showErrorStatus(fmt.Sprintf("cannot run /%s while model is working", command.Name))
	}
	if commandResult := m.commandRegistry.Execute(commandText); commandResult.Handled {
		m.clearInput()
		next, cmd := c.applyCommandResult(commandResult)
		return true, next, cmd
	}
	return false, m, nil
}

func (m *uiModel) blockedDeferredSlashCommand(commandText string) (string, bool) {
	if m.commandRegistry == nil {
		return "", false
	}
	commandResult := m.commandRegistry.Execute(commandText)
	if !commandResult.Handled {
		return "", false
	}
	switch commandResult.Action {
	case commands.ActionBack:
		if !m.hasParentSession() {
			return "No parent session available", true
		}
	case commands.ActionSetFast:
		available, _ := m.fastModeState()
		if !available {
			return "Fast mode is only available for OpenAI-based Responses providers", true
		}
	case commands.ActionProcesses:
		args := strings.Fields(strings.TrimSpace(commandResult.Args))
		if len(args) > 0 && m.backgroundManager == nil {
			return "background process manager is unavailable", true
		}
	}
	return "", false
}
