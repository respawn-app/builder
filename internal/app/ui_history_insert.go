package app

import (
	"strings"

	"builder/internal/config"
	"builder/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *uiModel) canUseHistoryInsertion() bool {
	return m.tuiAlternateScreen != config.TUIAlternateScreenAlways
}

func (m *uiModel) shouldInsertHistoryNow() bool {
	return m.canUseHistoryInsertion() && m.view.Mode() == tui.ModeOngoing && !m.altScreenActive
}

func (m *uiModel) currentOngoingSnapshot() string {
	return m.view.OngoingSnapshot()
}

func (m *uiModel) resetHistoryInsertionBaseline() {
	if !m.canUseHistoryInsertion() {
		m.lastInsertedOngoingSnapshot = ""
		m.pendingOngoingSnapshot = ""
		return
	}
	m.lastInsertedOngoingSnapshot = m.currentOngoingSnapshot()
	m.pendingOngoingSnapshot = ""
}

func (m *uiModel) onConversationSyncedCmd() tea.Cmd {
	if !m.canUseHistoryInsertion() {
		m.lastInsertedOngoingSnapshot = ""
		m.pendingOngoingSnapshot = ""
		return nil
	}

	snapshot := m.currentOngoingSnapshot()
	if strings.TrimSpace(m.lastInsertedOngoingSnapshot) == "" {
		m.lastInsertedOngoingSnapshot = snapshot
		m.pendingOngoingSnapshot = ""
		return nil
	}

	if !m.shouldInsertHistoryNow() {
		m.pendingOngoingSnapshot = snapshot
		return nil
	}

	target := snapshot
	if m.pendingOngoingSnapshot != "" {
		target = m.pendingOngoingSnapshot
	}
	m.pendingOngoingSnapshot = ""
	return m.flushSnapshotDeltaCmd(target)
}

func (m *uiModel) flushPendingHistoryCmd() tea.Cmd {
	if !m.shouldInsertHistoryNow() {
		return nil
	}
	target := m.currentOngoingSnapshot()
	if m.pendingOngoingSnapshot != "" {
		target = m.pendingOngoingSnapshot
	}
	m.pendingOngoingSnapshot = ""
	return m.flushSnapshotDeltaCmd(target)
}

func (m *uiModel) flushSnapshotDeltaCmd(target string) tea.Cmd {
	if !m.canUseHistoryInsertion() {
		m.lastInsertedOngoingSnapshot = ""
		m.pendingOngoingSnapshot = ""
		return nil
	}
	prev := m.lastInsertedOngoingSnapshot
	if prev == "" {
		m.lastInsertedOngoingSnapshot = target
		return nil
	}
	delta, ok := ongoingSnapshotDelta(prev, target)
	m.lastInsertedOngoingSnapshot = target
	if !ok || strings.TrimSpace(delta) == "" {
		return nil
	}
	return tea.Printf("%s", delta)
}

func ongoingSnapshotDelta(previous, current string) (string, bool) {
	prevLines := splitSnapshotLines(previous)
	currLines := splitSnapshotLines(current)
	if len(currLines) < len(prevLines) {
		return "", false
	}
	for i := range prevLines {
		if prevLines[i] != currLines[i] {
			return "", false
		}
	}
	if len(currLines) == len(prevLines) {
		return "", true
	}
	return strings.Join(currLines[len(prevLines):], "\n"), true
}

func splitSnapshotLines(snapshot string) []string {
	if snapshot == "" {
		return nil
	}
	return strings.Split(snapshot, "\n")
}
