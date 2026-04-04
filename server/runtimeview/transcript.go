package runtimeview

import (
	"builder/server/runtime"
	"builder/shared/clientui"
)

func TranscriptPageFromRuntime(engine *runtime.Engine, req clientui.TranscriptPageRequest) clientui.TranscriptPage {
	if engine == nil {
		return clientui.TranscriptPage{}
	}
	return TranscriptPageFromChat(
		engine.SessionID(),
		engine.SessionName(),
		ConversationFreshnessFromSession(engine.ConversationFreshness()),
		engine.TranscriptRevision(),
		ChatSnapshotFromRuntime(engine.ChatSnapshot()),
		req,
	)
}

func TranscriptPageFromChat(sessionID, sessionName string, freshness clientui.ConversationFreshness, revision int64, snapshot clientui.ChatSnapshot, req clientui.TranscriptPageRequest) clientui.TranscriptPage {
	total := len(snapshot.Entries)
	start := req.Offset
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end := total
	if req.Limit > 0 && start+req.Limit < end {
		end = start + req.Limit
	}
	entries := cloneChatEntries(snapshot.Entries[start:end])
	nextOffset := 0
	hasMore := end < total
	if hasMore {
		nextOffset = end
	}
	return clientui.TranscriptPage{
		SessionID:             sessionID,
		SessionName:           sessionName,
		ConversationFreshness: freshness,
		Revision:              revision,
		TotalEntries:          total,
		Offset:                start,
		NextOffset:            nextOffset,
		HasMore:               hasMore,
		Entries:               entries,
		Ongoing:               snapshot.Ongoing,
		OngoingError:          snapshot.OngoingError,
	}
}

func cloneChatEntries(entries []clientui.ChatEntry) []clientui.ChatEntry {
	if len(entries) == 0 {
		return nil
	}
	cloned := make([]clientui.ChatEntry, 0, len(entries))
	for _, entry := range entries {
		copyEntry := entry
		copyEntry.ToolCall = cloneClientToolCallMeta(entry.ToolCall)
		cloned = append(cloned, copyEntry)
	}
	return cloned
}

func cloneClientToolCallMeta(meta *clientui.ToolCallMeta) *clientui.ToolCallMeta {
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
