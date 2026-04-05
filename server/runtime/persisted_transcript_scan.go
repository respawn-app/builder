package runtime

import (
	"strings"

	"builder/server/llm"
	"builder/server/session"
	"builder/shared/transcript"
)

type PersistedTranscriptScanRequest struct {
	Offset int
	Limit  int

	TrackOngoingTail bool
	TailLimit        int
}

type PersistedTranscriptScan struct {
	request PersistedTranscriptScanRequest

	projector *TranscriptProjector

	materialized             bool
	fullSnapshot             ChatSnapshot
	collectedPage            ChatSnapshot
	totalEntries             int
	ongoingTail              TranscriptWindowSnapshot
	lastCommittedFinalAnswer string
}

func NewPersistedTranscriptScan(req PersistedTranscriptScanRequest) *PersistedTranscriptScan {
	if req.Offset < 0 {
		req.Offset = 0
	}
	if req.Limit < 0 {
		req.Limit = 0
	}
	if req.TailLimit < 0 {
		req.TailLimit = 0
	}
	return &PersistedTranscriptScan{
		request:   req,
		projector: NewTranscriptProjector(),
	}
}

func (s *PersistedTranscriptScan) ApplyPersistedEvent(evt session.Event) error {
	if s == nil {
		return nil
	}
	s.materialized = false
	return s.projector.ApplyPersistedEvent(evt)
}

func (s *PersistedTranscriptScan) TotalEntries() int {
	if s == nil {
		return 0
	}
	s.materialize()
	return s.totalEntries
}

func (s *PersistedTranscriptScan) CollectedPageSnapshot() ChatSnapshot {
	if s == nil {
		return ChatSnapshot{}
	}
	s.materialize()
	return ChatSnapshot{Entries: clonePersistedChatEntries(s.collectedPage.Entries)}
}

func (s *PersistedTranscriptScan) OngoingTailSnapshot() TranscriptWindowSnapshot {
	if s == nil {
		return TranscriptWindowSnapshot{}
	}
	s.materialize()
	if !s.request.TrackOngoingTail || s.request.TailLimit <= 0 {
		return TranscriptWindowSnapshot{}
	}
	return TranscriptWindowSnapshot{
		Snapshot:     ChatSnapshot{Entries: clonePersistedChatEntries(s.ongoingTail.Snapshot.Entries)},
		TotalEntries: s.ongoingTail.TotalEntries,
		Offset:       s.ongoingTail.Offset,
	}
}

func (s *PersistedTranscriptScan) LastCommittedAssistantFinalAnswer() string {
	if s == nil {
		return ""
	}
	s.materialize()
	return s.lastCommittedFinalAnswer
}

func (s *PersistedTranscriptScan) materialize() {
	if s == nil || s.materialized {
		return
	}
	full := s.projector.ChatSnapshot()
	s.fullSnapshot = ChatSnapshot{Entries: clonePersistedChatEntries(full.Entries)}
	s.totalEntries = len(s.fullSnapshot.Entries)
	s.collectedPage = ChatSnapshot{Entries: persistedTranscriptPageEntries(s.fullSnapshot.Entries, s.request.Offset, s.request.Limit)}
	s.lastCommittedFinalAnswer = s.projector.LastCommittedAssistantFinalAnswer()
	if s.request.TrackOngoingTail && s.request.TailLimit > 0 {
		tail := s.projector.OngoingTailSnapshot(s.request.TailLimit)
		s.ongoingTail = TranscriptWindowSnapshot{
			Snapshot:     ChatSnapshot{Entries: clonePersistedChatEntries(tail.Snapshot.Entries)},
			TotalEntries: tail.TotalEntries,
			Offset:       tail.Offset,
		}
	} else {
		s.ongoingTail = TranscriptWindowSnapshot{}
	}
	s.materialized = true
}

func persistedTranscriptPageEntries(entries []ChatEntry, offset, limit int) []ChatEntry {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(entries) {
		return nil
	}
	end := len(entries)
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return clonePersistedChatEntries(entries[offset:end])
}

func clonePersistedChatEntries(entries []ChatEntry) []ChatEntry {
	if len(entries) == 0 {
		return nil
	}
	cloned := make([]ChatEntry, 0, len(entries))
	for _, entry := range entries {
		cloned = append(cloned, clonePersistedChatEntry(entry))
	}
	return cloned
}

func clonePersistedChatEntry(entry ChatEntry) ChatEntry {
	copyEntry := entry
	copyEntry.ToolCall = clonePersistedToolCallMeta(entry.ToolCall)
	return copyEntry
}

func clonePersistedToolCallMeta(meta *transcript.ToolCallMeta) *transcript.ToolCallMeta {
	if meta == nil {
		return nil
	}
	copyMeta := *meta
	if len(meta.Suggestions) > 0 {
		copyMeta.Suggestions = append([]string(nil), meta.Suggestions...)
	}
	if meta.RenderHint != nil {
		renderHint := *meta.RenderHint
		copyMeta.RenderHint = &renderHint
	}
	return &copyMeta
}

func formatPersistedToolCall(call llm.ToolCall) ChatEntry {
	meta := transcriptToolCallMeta(call, "")
	text := "tool call"
	if meta != nil {
		text = strings.TrimSpace(meta.Command)
	}
	if text == "" {
		text = "tool call"
	}
	return ChatEntry{
		Role:       "tool_call",
		Text:       text,
		ToolCallID: strings.TrimSpace(call.ID),
		ToolCall:   meta,
	}
}
