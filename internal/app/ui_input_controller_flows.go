package app

import (
	"strings"
	"time"

	"builder/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

func (c uiInputController) rollbackTransitionCmd() tea.Cmd {
	if !c.model.altScreenActive {
		return nil
	}
	return tea.ClearScreen
}

func (c uiInputController) startRollbackSelectionFlowCmd() tea.Cmd {
	m := c.model
	if !m.startRollbackSelectionMode() {
		return nil
	}
	overlayCmd := m.pushRollbackOverlayIfNeeded()
	if overlayCmd != nil {
		m.focusRollbackSelection()
		return overlayCmd
	}
	return c.rollbackTransitionCmd()
}

func (c uiInputController) stopRollbackSelectionFlowCmd() tea.Cmd {
	m := c.model
	overlayCmd := m.popRollbackOverlayIfNeeded()
	m.stopRollbackSelectionMode()
	if overlayCmd != nil {
		return overlayCmd
	}
	return c.rollbackTransitionCmd()
}

func (c uiInputController) beginRollbackEditingFlowCmd() tea.Cmd {
	m := c.model
	targetEntry, ok := m.beginRollbackEditing()
	if !ok {
		return nil
	}
	overlayCmd := m.popRollbackOverlayWithNativeReplay(false)
	m.forwardToView(tui.FocusTranscriptEntryMsg{EntryIndex: targetEntry, Bottom: true})
	if !m.usesNativeScrollback() {
		if overlayCmd != nil {
			return overlayCmd
		}
		return c.rollbackTransitionCmd()
	}
	anchorCmd := m.replayNativeTranscriptThroughEntry(targetEntry)
	return sequenceCmds(overlayCmd, anchorCmd)
}

func (c uiInputController) cancelRollbackEditingToSelectionFlowCmd() tea.Cmd {
	m := c.model
	if !m.cancelRollbackEditingBackToSelection() {
		return nil
	}
	overlayCmd := m.pushRollbackOverlayIfNeeded()
	if overlayCmd != nil {
		m.focusRollbackSelection()
		return overlayCmd
	}
	return c.rollbackTransitionCmd()
}

func (c uiInputController) startRollbackFork(text string) (tea.Model, tea.Cmd) {
	m := c.model
	m.nextForkUserMessageIndex = m.rollbackSelectedUserMessageIndex
	m.nextSessionInitialPrompt = text
	m.exitAction = UIActionForkRollback
	m.rollbackEditing = false
	return m, tea.Quit
}

func (c uiInputController) startProcessListFlowCmd() tea.Cmd {
	m := c.model
	m.openProcessList()
	refreshCmd := waitProcessListRefresh()
	if overlayCmd := m.pushProcessOverlayIfNeeded(); overlayCmd != nil {
		return tea.Batch(overlayCmd, refreshCmd)
	}
	return refreshCmd
}

func (c uiInputController) stopProcessListFlowCmd() tea.Cmd {
	m := c.model
	overlayCmd := m.popProcessOverlayIfNeeded()
	m.closeProcessList()
	if overlayCmd != nil {
		return overlayCmd
	}
	return nil
}

func (c uiInputController) markPendingCSIShiftEnter() {
	m := c.model
	m.pendingCSIShiftEnter = true
	m.pendingCSIShiftEnterAt = time.Now()
}

func (c uiInputController) clearPendingCSIShiftEnter() {
	m := c.model
	m.pendingCSIShiftEnter = false
	m.pendingCSIShiftEnterAt = time.Time{}
}

func (c uiInputController) normalizePendingCSIShiftEnterOnEnter() {
	m := c.model
	if !m.pendingCSIShiftEnter {
		return
	}
	if m.pendingCSIShiftEnterAt.IsZero() || time.Since(m.pendingCSIShiftEnterAt) > csiShiftEnterDedupWindow {
		c.clearPendingCSIShiftEnter()
		return
	}
	if strings.HasSuffix(m.input, "\n") {
		m.input = strings.TrimSuffix(m.input, "\n")
		m.inputCursor = -1
		m.refreshSlashCommandFilterFromInput()
	}
	c.clearPendingCSIShiftEnter()
}
