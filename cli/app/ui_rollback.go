package app

import (
	"strings"

	"builder/cli/tui"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *uiModel) refreshRollbackCandidates() {
	candidates := make([]rollbackCandidate, 0)
	userMessageIndex := 0
	for idx, entry := range m.transcriptEntries {
		if strings.TrimSpace(entry.Role) != "user" {
			continue
		}
		userMessageIndex++
		candidates = append(candidates, rollbackCandidate{
			TranscriptIndex:  idx,
			UserMessageIndex: userMessageIndex,
			Text:             entry.Text,
		})
	}
	m.rollback.candidates = candidates
	if len(m.rollback.candidates) == 0 {
		m.rollback.selection = 0
		m.rollback.phase = uiRollbackPhaseInactive
		m.rollback.ownsTranscriptMode = false
		m.rollback.selectedUserMessageIndex = 0
		m.clearRollbackSelectionHighlight()
		if m.inputMode() == uiInputModeRollbackSelection || m.inputMode() == uiInputModeRollbackEdit {
			m.restorePrimaryInputMode()
		}
		return
	}
	if m.rollback.selection < 0 {
		m.rollback.selection = 0
	}
	if m.rollback.selection >= len(m.rollback.candidates) {
		m.rollback.selection = len(m.rollback.candidates) - 1
	}
	if m.rollback.isSelecting() {
		m.applyRollbackSelectionHighlight()
	}
}

func (m *uiModel) startRollbackSelectionMode() bool {
	if !m.rollback.isActive() && !m.rollback.restoreScrollActive {
		m.rollback.restoreOngoingScroll = m.view.OngoingScroll()
		m.rollback.restoreScrollActive = true
	}
	m.refreshRollbackCandidates()
	if len(m.rollback.candidates) == 0 {
		return false
	}
	if m.rollback.selectedUserMessageIndex > 0 {
		matched := -1
		for idx, candidate := range m.rollback.candidates {
			if candidate.UserMessageIndex == m.rollback.selectedUserMessageIndex {
				matched = idx
				break
			}
		}
		if matched >= 0 {
			m.rollback.selection = matched
		}
	} else {
		m.rollback.selection = len(m.rollback.candidates) - 1
	}
	m.rollback.phase = uiRollbackPhaseSelection
	m.rollback.selectedUserMessageIndex = 0
	m.setInputMode(uiInputModeRollbackSelection)
	m.clearInput()
	m.applyRollbackSelectionHighlight()
	return true
}

func (m *uiModel) stopRollbackSelectionMode() {
	m.rollback.phase = uiRollbackPhaseInactive
	m.clearRollbackSelectionHighlight()
	if m.rollback.restoreScrollActive {
		m.forwardToView(tui.SetOngoingScrollMsg{Scroll: m.rollback.restoreOngoingScroll})
		m.forwardToView(tui.SetSelectedTranscriptEntryMsg{Active: false, EntryIndex: -1, RefreshDetailSnapshot: true})
		m.rollback.restoreScrollActive = false
	}
	m.restorePrimaryInputMode()
}

func (m *uiModel) applyRollbackSelectionHighlight() {
	if !m.rollback.isSelecting() || len(m.rollback.candidates) == 0 {
		m.clearRollbackSelectionHighlight()
		return
	}
	candidate := m.rollback.candidates[m.rollback.selection]
	m.forwardToView(tui.SetSelectedTranscriptEntryMsg{Active: true, EntryIndex: candidate.TranscriptIndex, RefreshDetailSnapshot: false})
	m.focusRollbackSelection()
}

func (m *uiModel) focusRollbackSelection() {
	if !m.rollback.isSelecting() || len(m.rollback.candidates) == 0 {
		return
	}
	candidate := m.rollback.candidates[m.rollback.selection]
	m.forwardToView(tui.FocusTranscriptEntryMsg{EntryIndex: candidate.TranscriptIndex, Center: true})
}

func (m *uiModel) clearRollbackSelectionHighlight() {
	m.forwardToView(tui.SetSelectedTranscriptEntryMsg{Active: false, EntryIndex: -1, RefreshDetailSnapshot: false})
}

func (m *uiModel) moveRollbackSelection(delta int) {
	if len(m.rollback.candidates) == 0 {
		return
	}
	m.rollback.selection += delta
	if m.rollback.selection < 0 {
		m.rollback.selection = 0
	}
	if m.rollback.selection >= len(m.rollback.candidates) {
		m.rollback.selection = len(m.rollback.candidates) - 1
	}
	m.applyRollbackSelectionHighlight()
}

func (m *uiModel) beginRollbackEditing() (int, bool) {
	if !m.rollback.isSelecting() || len(m.rollback.candidates) == 0 {
		return -1, false
	}
	selected := m.rollback.candidates[m.rollback.selection]
	m.rollback.selectedUserMessageIndex = selected.UserMessageIndex
	m.rollback.phase = uiRollbackPhaseEditing
	m.setInputMode(uiInputModeRollbackEdit)
	m.replaceMainInput(selected.Text, -1)
	m.forwardToView(tui.SetSelectedTranscriptEntryMsg{Active: true, EntryIndex: selected.TranscriptIndex, RefreshDetailSnapshot: false})
	return selected.TranscriptIndex, true
}

func (m *uiModel) cancelRollbackEditingBackToSelection() bool {
	if !m.rollback.isEditing() {
		return false
	}
	m.rollback.phase = uiRollbackPhaseInactive
	return m.startRollbackSelectionMode()
}

func (m *uiModel) clearRollbackFlow() {
	m.rollback.phase = uiRollbackPhaseInactive
	m.rollback.ownsTranscriptMode = false
	m.rollback.selectedUserMessageIndex = 0
	m.rollback.restoreScrollActive = false
	m.clearRollbackSelectionHighlight()
	m.restorePrimaryInputMode()
}

func (m *uiModel) pushRollbackOverlayIfNeeded() tea.Cmd {
	if m.rollback.ownsTranscriptMode {
		return nil
	}
	if m.view.Mode() != tui.ModeOngoing {
		return nil
	}
	m.rollback.ownsTranscriptMode = true
	if transitionCmd := m.transitionTranscriptMode(tui.ModeDetail, false, true); transitionCmd != nil {
		return transitionCmd
	}
	return tea.ClearScreen
}

func (m *uiModel) popRollbackOverlayIfNeeded() tea.Cmd {
	return m.popRollbackOverlayWithNativeReplay(true)
}

func (m *uiModel) popRollbackOverlayWithNativeReplay(emitNativeReplay bool) tea.Cmd {
	if !m.rollback.ownsTranscriptMode {
		return nil
	}
	m.rollback.ownsTranscriptMode = false
	if m.view.Mode() != tui.ModeDetail {
		return nil
	}
	if transitionCmd := m.transitionTranscriptMode(tui.ModeOngoing, false, emitNativeReplay); transitionCmd != nil {
		return transitionCmd
	}
	return tea.ClearScreen
}
