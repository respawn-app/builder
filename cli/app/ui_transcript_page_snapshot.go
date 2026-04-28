package app

func runtimeTranscriptPageSnapshotFromModel(m *uiModel) runtimeTranscriptPageSnapshot {
	if m == nil {
		return runtimeTranscriptPageSnapshot{}
	}
	entries := cloneTUITranscriptEntries(m.transcriptEntries)
	effectiveRevision, effectiveCommittedCount := committedTranscriptStateIncludingDeferredTail(m)
	return runtimeTranscriptPageSnapshot{
		entries:                 entries,
		baseOffset:              m.transcriptBaseOffset,
		totalEntries:            m.transcriptTotalEntries,
		revision:                m.transcriptRevision,
		effectiveRevision:       effectiveRevision,
		effectiveCommittedCount: effectiveCommittedCount,
		viewMode:                m.view.Mode(),
		liveOngoing:             m.view.OngoingStreamingText(),
		liveOngoingError:        m.view.OngoingErrorText(),
		transcriptLiveDirty:     m.transcriptLiveDirty,
		reasoningLiveDirty:      m.reasoningLiveDirty,
	}
}
