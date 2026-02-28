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

func (m *uiModel) currentOngoingSnapshot() (canonical string, printable string) {
	return m.layout().ongoingScrollbackSnapshot()
}

func (m *uiModel) resetHistoryInsertionBaseline() {
	if !m.canUseHistoryInsertion() {
		m.lastInsertedOngoingSnapshot = ""
		m.pendingOngoingSnapshot = ""
		m.pendingOngoingPrintable = ""
		return
	}
	canonical, _ := m.currentOngoingSnapshot()
	m.lastInsertedOngoingSnapshot = canonical
	m.pendingOngoingSnapshot = ""
	m.pendingOngoingPrintable = ""
}

func (m *uiModel) onConversationSyncedCmd() tea.Cmd {
	if !m.canUseHistoryInsertion() {
		m.lastInsertedOngoingSnapshot = ""
		m.pendingOngoingSnapshot = ""
		m.pendingOngoingPrintable = ""
		return nil
	}

	snapshotCanonical, snapshotPrintable := m.currentOngoingSnapshot()
	if strings.TrimSpace(m.lastInsertedOngoingSnapshot) == "" {
		m.lastInsertedOngoingSnapshot = snapshotCanonical
		m.pendingOngoingSnapshot = ""
		m.pendingOngoingPrintable = ""
		return nil
	}

	if !m.shouldInsertHistoryNow() {
		m.pendingOngoingSnapshot = snapshotCanonical
		m.pendingOngoingPrintable = snapshotPrintable
		return nil
	}

	targetCanonical := snapshotCanonical
	targetPrintable := snapshotPrintable
	if m.pendingOngoingSnapshot != "" {
		targetCanonical = m.pendingOngoingSnapshot
		targetPrintable = m.pendingOngoingPrintable
	}
	m.pendingOngoingSnapshot = ""
	m.pendingOngoingPrintable = ""
	return m.flushSnapshotDeltaCmd(targetCanonical, targetPrintable)
}

func (m *uiModel) flushPendingHistoryCmd() tea.Cmd {
	if !m.shouldInsertHistoryNow() {
		return nil
	}
	targetCanonical, targetPrintable := m.currentOngoingSnapshot()
	if m.pendingOngoingSnapshot != "" {
		targetCanonical = m.pendingOngoingSnapshot
		targetPrintable = m.pendingOngoingPrintable
	}
	m.pendingOngoingSnapshot = ""
	m.pendingOngoingPrintable = ""
	return m.flushSnapshotDeltaCmd(targetCanonical, targetPrintable)
}

func (m *uiModel) flushSnapshotDeltaCmd(targetCanonical, targetPrintable string) tea.Cmd {
	if !m.canUseHistoryInsertion() {
		m.lastInsertedOngoingSnapshot = ""
		m.pendingOngoingSnapshot = ""
		m.pendingOngoingPrintable = ""
		return nil
	}
	prev := m.lastInsertedOngoingSnapshot
	if prev == "" {
		m.lastInsertedOngoingSnapshot = targetCanonical
		return nil
	}
	prefixLines, ok := ongoingSnapshotPrefixLineCount(prev, targetCanonical)
	m.lastInsertedOngoingSnapshot = targetCanonical
	printableDelta, shouldPrint := ongoingSnapshotPrintableSuffix(targetPrintable, prefixLines, ok)
	printableDelta, shouldPrint = ongoingSnapshotPrintableDelta(printableDelta, shouldPrint)
	if !shouldPrint {
		return nil
	}
	return tea.Printf("%s", printableDelta)
}

func ongoingSnapshotDelta(previous, current string) (string, bool) {
	prefixLines, ok := ongoingSnapshotPrefixLineCount(previous, current)
	if !ok {
		return "", false
	}
	currLines := splitSnapshotLines(current)
	if len(currLines) == prefixLines {
		return "", true
	}
	return strings.Join(currLines[prefixLines:], "\n"), true
}

func ongoingSnapshotPrefixLineCount(previous, current string) (int, bool) {
	prevLines := splitSnapshotLines(previous)
	currLines := splitSnapshotLines(current)
	if len(currLines) < len(prevLines) {
		return 0, false
	}
	for i := range prevLines {
		if prevLines[i] != currLines[i] {
			return 0, false
		}
	}
	return len(prevLines), true
}

func ongoingSnapshotPrintableSuffix(currentPrintable string, prefixLines int, valid bool) (string, bool) {
	if !valid {
		return "", false
	}
	currLines := splitSnapshotLines(currentPrintable)
	if len(currLines) < prefixLines {
		return "", false
	}
	if len(currLines) == prefixLines {
		return "", true
	}
	return strings.Join(currLines[prefixLines:], "\n"), true
}

func ongoingSnapshotPrintableDelta(delta string, valid bool) (string, bool) {
	if !valid || strings.TrimSpace(delta) == "" {
		return "", false
	}
	if strings.HasSuffix(delta, "\n") {
		return delta, true
	}
	return delta + "\n", true
}

func splitSnapshotLines(snapshot string) []string {
	if snapshot == "" {
		return nil
	}
	return strings.Split(snapshot, "\n")
}
