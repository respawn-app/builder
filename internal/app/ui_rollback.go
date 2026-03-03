package app

import (
	"strings"

	"builder/internal/tui"

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
	m.rollbackCandidates = candidates
	if len(m.rollbackCandidates) == 0 {
		m.rollbackSelection = 0
		m.rollbackMode = false
		m.rollbackEditing = false
		m.rollbackOverlayPushed = false
		m.rollbackSelectedUserMessageIndex = 0
		m.clearRollbackSelectionHighlight()
		return
	}
	if m.rollbackSelection < 0 {
		m.rollbackSelection = 0
	}
	if m.rollbackSelection >= len(m.rollbackCandidates) {
		m.rollbackSelection = len(m.rollbackCandidates) - 1
	}
	if m.rollbackMode {
		m.applyRollbackSelectionHighlight()
	}
}

func (m *uiModel) startRollbackSelectionMode() bool {
	if !m.rollbackMode && !m.rollbackEditing && !m.rollbackRestoreScrollActive {
		m.rollbackRestoreOngoingScroll = m.view.OngoingScroll()
		m.rollbackRestoreScrollActive = true
	}
	m.refreshRollbackCandidates()
	if len(m.rollbackCandidates) == 0 {
		return false
	}
	if m.rollbackSelectedUserMessageIndex > 0 {
		matched := -1
		for idx, candidate := range m.rollbackCandidates {
			if candidate.UserMessageIndex == m.rollbackSelectedUserMessageIndex {
				matched = idx
				break
			}
		}
		if matched >= 0 {
			m.rollbackSelection = matched
		}
	} else {
		m.rollbackSelection = len(m.rollbackCandidates) - 1
	}
	m.rollbackMode = true
	m.rollbackEditing = false
	m.rollbackSelectedUserMessageIndex = 0
	m.clearInput()
	m.applyRollbackSelectionHighlight()
	return true
}

func (m *uiModel) stopRollbackSelectionMode() {
	m.rollbackMode = false
	m.clearRollbackSelectionHighlight()
	if m.rollbackRestoreScrollActive {
		m.forwardToView(tui.SetOngoingScrollMsg{Scroll: m.rollbackRestoreOngoingScroll})
		m.rollbackRestoreScrollActive = false
	}
}

func (m *uiModel) applyRollbackSelectionHighlight() {
	if !m.rollbackMode || len(m.rollbackCandidates) == 0 {
		m.clearRollbackSelectionHighlight()
		return
	}
	candidate := m.rollbackCandidates[m.rollbackSelection]
	m.forwardToView(tui.SetSelectedTranscriptEntryMsg{Active: true, EntryIndex: candidate.TranscriptIndex, RefreshDetailSnapshot: false})
	m.focusRollbackSelection()
}

func (m *uiModel) focusRollbackSelection() {
	if !m.rollbackMode || len(m.rollbackCandidates) == 0 {
		return
	}
	candidate := m.rollbackCandidates[m.rollbackSelection]
	m.forwardToView(tui.FocusTranscriptEntryMsg{EntryIndex: candidate.TranscriptIndex, Center: true})
}

func (m *uiModel) clearRollbackSelectionHighlight() {
	m.forwardToView(tui.SetSelectedTranscriptEntryMsg{Active: false, EntryIndex: -1, RefreshDetailSnapshot: false})
}

func (m *uiModel) moveRollbackSelection(delta int) {
	if len(m.rollbackCandidates) == 0 {
		return
	}
	m.rollbackSelection += delta
	if m.rollbackSelection < 0 {
		m.rollbackSelection = 0
	}
	if m.rollbackSelection >= len(m.rollbackCandidates) {
		m.rollbackSelection = len(m.rollbackCandidates) - 1
	}
	m.applyRollbackSelectionHighlight()
}

func (m *uiModel) beginRollbackEditing() bool {
	if !m.rollbackMode || len(m.rollbackCandidates) == 0 {
		return false
	}
	selected := m.rollbackCandidates[m.rollbackSelection]
	m.rollbackSelectedUserMessageIndex = selected.UserMessageIndex
	m.rollbackMode = false
	m.rollbackEditing = true
	m.input = selected.Text
	m.inputCursor = -1
	m.clearRollbackSelectionHighlight()
	return true
}

func (m *uiModel) cancelRollbackEditingBackToSelection() bool {
	if !m.rollbackEditing {
		return false
	}
	m.rollbackEditing = false
	return m.startRollbackSelectionMode()
}

func (m *uiModel) clearRollbackFlow() {
	m.rollbackMode = false
	m.rollbackEditing = false
	m.rollbackOverlayPushed = false
	m.rollbackSelectedUserMessageIndex = 0
	m.rollbackRestoreScrollActive = false
	m.clearRollbackSelectionHighlight()
}

func (m *uiModel) pushRollbackOverlayIfNeeded() tea.Cmd {
	if !m.usesNativeScrollback() {
		return nil
	}
	if m.rollbackOverlayPushed {
		return nil
	}
	if m.view.Mode() != tui.ModeOngoing {
		return nil
	}
	m.rollbackOverlayPushed = true
	if transitionCmd := m.toggleTranscriptMode(); transitionCmd != nil {
		return transitionCmd
	}
	return tea.ClearScreen
}

func (m *uiModel) popRollbackOverlayIfNeeded() tea.Cmd {
	if !m.rollbackOverlayPushed {
		return nil
	}
	m.rollbackOverlayPushed = false
	if m.view.Mode() != tui.ModeDetail {
		return nil
	}
	if transitionCmd := m.toggleTranscriptMode(); transitionCmd != nil {
		return transitionCmd
	}
	return tea.ClearScreen
}
