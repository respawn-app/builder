package app

import (
	"builder/cli/tui"
	"builder/shared/clientui"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *uiModel) transcriptRequestForCurrentMode() clientui.TranscriptPageRequest {
	if m.view.Mode() == tui.ModeDetail {
		return m.detailTranscript.requestedPageForDetailEntry()
	}
	return clientui.TranscriptPageRequest{Window: clientui.TranscriptWindowOngoingTail}
}

func (m *uiModel) maybeRequestDetailTranscriptPage() tea.Cmd {
	if !m.hasRuntimeClient() || m.view.Mode() != tui.ModeDetail || m.runtimeTranscriptBusy {
		return nil
	}
	if !m.view.DetailMetricsResolved() {
		firstVisible, lastVisible, ok := m.view.DetailVisibleEntryRange()
		if !ok {
			return nil
		}
		if firstVisible <= m.view.TranscriptBaseOffset()+1 {
			if req, ok := m.detailTranscript.pageBefore(); ok {
				return m.requestRuntimeTranscriptPage(req)
			}
		}
		loadedLast := m.view.TranscriptBaseOffset() + m.view.LoadedTranscriptEntryCount() - 1
		if lastVisible >= loadedLast-1 {
			if req, ok := m.detailTranscript.pageAfter(); ok {
				return m.requestRuntimeTranscriptPage(req)
			}
		}
		return nil
	}
	if m.view.DetailScroll() <= uiDetailTranscriptEdgeLineMargin {
		if req, ok := m.detailTranscript.pageBefore(); ok {
			return m.requestRuntimeTranscriptPage(req)
		}
	}
	if m.view.DetailMaxScroll()-m.view.DetailScroll() <= uiDetailTranscriptEdgeLineMargin {
		if req, ok := m.detailTranscript.pageAfter(); ok {
			return m.requestRuntimeTranscriptPage(req)
		}
	}
	return nil
}

func (m *uiModel) primeDetailTranscriptFromCurrentTail() {
	if m.view.Mode() != tui.ModeDetail {
		return
	}
	if m.detailTranscript.loaded {
		return
	}
	page := clientui.TranscriptPage{
		Offset:       m.transcriptBaseOffset,
		TotalEntries: m.transcriptTotalEntries,
		Ongoing:      m.view.OngoingStreamingText(),
		OngoingError: m.view.OngoingErrorText(),
	}
	for _, entry := range tui.CommittedOngoingEntries(m.transcriptEntries) {
		page.Entries = append(page.Entries, clientui.ChatEntry{
			Visibility:  entry.Visibility,
			Role:        entry.Role,
			Text:        entry.Text,
			OngoingText: entry.OngoingText,
			Phase:       string(entry.Phase),
			ToolCallID:  entry.ToolCallID,
			ToolCall:    transcriptToolCallMetaClient(entry.ToolCall),
		})
	}
	if page.TotalEntries == 0 {
		page.TotalEntries = page.Offset + len(page.Entries)
	}
	if len(page.Entries) == 0 && len(m.transcriptEntries) > 0 {
		page.Entries = m.localRuntimeTranscript().Entries
		page.TotalEntries = max(page.TotalEntries, page.Offset+len(page.Entries))
	}
	m.detailTranscript.replace(page)
}
