package app

import (
	"fmt"

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
