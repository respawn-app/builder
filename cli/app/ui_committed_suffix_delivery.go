package app

import (
	"builder/cli/tui"
	"builder/shared/clientui"

	tea "github.com/charmbracelet/bubbletea"
)

func shouldDeliverCommittedRuntimeEventFromSuffix(m *uiModel, evt clientui.Event) bool {
	if m == nil || !evt.CommittedTranscriptChanged || len(evt.TranscriptEntries) == 0 {
		return false
	}
	if evt.Kind == clientui.EventUserMessageFlushed {
		return false
	}
	if _, ok := m.runtimeClient().(interface {
		RefreshCommittedTranscriptSuffix(clientui.CommittedTranscriptSuffixRequest) (clientui.CommittedTranscriptSuffix, error)
	}); !ok {
		return false
	}
	return true
}

func committedTranscriptSuffixRequestForEvent(m *uiModel, evt clientui.Event) clientui.CommittedTranscriptSuffixRequest {
	after := committedTranscriptTailEnd(m)
	limit := clientui.DefaultCommittedTranscriptSuffixLimit
	if evt.CommittedEntryCount > after {
		limit = evt.CommittedEntryCount - after
	}
	return clientui.NormalizeCommittedTranscriptSuffixRequest(clientui.CommittedTranscriptSuffixRequest{
		AfterEntryCount: after,
		Limit:           limit,
	})
}

func committedTranscriptTailEnd(m *uiModel) int {
	if m == nil {
		return 0
	}
	if m.ongoingCommittedDelivery.initialized {
		return m.ongoingCommittedDelivery.lastEmittedCommittedEntryCount
	}
	return committedTranscriptLocalFrontierEnd(m)
}

func committedTranscriptLocalFrontierEnd(m *uiModel) int {
	if m == nil {
		return 0
	}
	committedCount := 0
	for _, entry := range m.transcriptEntries {
		if transcriptEntryCommittedForApp(entry) {
			committedCount++
		}
	}
	end := m.transcriptBaseOffset + committedCount
	if end < 0 {
		return 0
	}
	return end
}

func committedOngoingLocalFrontierEnd(m *uiModel) int {
	if m == nil {
		return 0
	}
	return m.transcriptBaseOffset + len(committedTranscriptEntriesForApp(m.transcriptEntries))
}

func (m *uiModel) truncatePendingOngoingTailBeforeSuffix(startEntryCount int) {
	if m == nil {
		return
	}
	localIndex := startEntryCount - m.transcriptBaseOffset
	if localIndex < 0 || localIndex > len(m.transcriptEntries) {
		return
	}
	if localIndex == len(m.transcriptEntries) {
		return
	}
	m.transcriptEntries = append([]tui.TranscriptEntry(nil), m.transcriptEntries[:localIndex]...)
	if m.view.Mode() == tui.ModeOngoing {
		m.forwardToView(tui.SetConversationMsg{
			BaseOffset:   m.transcriptBaseOffset,
			TotalEntries: m.transcriptTotalEntries,
			Entries:      append([]tui.TranscriptEntry(nil), m.transcriptEntries...),
			Ongoing:      m.view.OngoingStreamingText(),
			OngoingError: m.view.OngoingErrorText(),
		})
	}
}

func (m *uiModel) applyCommittedTranscriptSuffixAppend(suffix clientui.CommittedTranscriptSuffix) tea.Cmd {
	if m == nil {
		return nil
	}
	if !m.ongoingCommittedDelivery.initialized {
		m.ongoingCommittedDelivery = newOngoingCommittedDeliveryCursor(committedTranscriptTailEnd(m), m.transcriptRevision)
	}
	page := transcriptPageFromCommittedTranscriptSuffix(suffix)
	entries := transcriptEntriesFromPage(page)
	expectedStart := committedTranscriptTailEnd(m)
	if page.Offset != expectedStart {
		m.runtimeAdapter().applyAuthoritativeOngoingTailPage(page, entries, false)
		if m.view.Mode() == tui.ModeOngoing {
			m.forwardToView(tui.SetConversationMsg{
				BaseOffset:   page.Offset,
				TotalEntries: page.TotalEntries,
				Entries:      entries,
				Ongoing:      page.Ongoing,
				OngoingError: page.OngoingError,
			})
		}
		return m.syncNativeHistoryFromTranscript()
	}
	m.truncatePendingOngoingTailBeforeSuffix(expectedStart)
	for _, entry := range entries {
		m.transcriptEntries = append(m.transcriptEntries, entry)
		if m.view.Mode() == tui.ModeOngoing {
			m.forwardToView(appendTranscriptMsgFromEntry(entry))
		}
	}
	m.transcriptRevision = max(m.transcriptRevision, suffix.Revision)
	m.transcriptTotalEntries = max(m.transcriptTotalEntries, suffix.CommittedEntryCount)
	m.transcriptLiveDirty = true
	m.refreshRollbackCandidates()
	if m.view.Mode() == tui.ModeDetail {
		m.detailTranscript.apply(page)
	}
	if suffix.NextEntryCount > suffix.StartEntryCount {
		m.sawAssistantDelta = false
		m.forwardToView(tui.ClearOngoingAssistantMsg{})
	}
	beforeSequence := m.nativeFlushSequence
	cmd := m.syncNativeHistoryFromTranscript()
	m.trackOngoingCommittedSuffixFlush(suffix, beforeSequence)
	return cmd
}

func (m *uiModel) trackOngoingCommittedSuffixFlush(suffix clientui.CommittedTranscriptSuffix, beforeSequence uint64) {
	if m == nil || !m.ongoingCommittedDelivery.initialized || suffix.NextEntryCount <= suffix.StartEntryCount {
		return
	}
	emittedEnd := committedOngoingLocalFrontierEnd(m)
	if emittedEnd <= m.ongoingCommittedDelivery.lastEmittedCommittedEntryCount {
		return
	}
	if !m.shouldEmitNativeHistory() {
		m.ongoingCommittedDelivery.recordCommittedAdvance(emittedEnd, suffix.Revision)
		return
	}
	if m.nativeFlushSequence <= beforeSequence {
		m.ongoingCommittedDelivery.lastEmittedCommittedEntryCount = emittedEnd
		m.ongoingCommittedDelivery.lastEmittedTranscriptRevision = suffix.Revision
		return
	}
	emittedSuffix := suffix
	emittedSuffix.NextEntryCount = emittedEnd
	_ = m.ongoingCommittedDelivery.beginNativeFlush(emittedSuffix, m.nativeFlushSequence)
}
