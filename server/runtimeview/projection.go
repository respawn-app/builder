package runtimeview

import (
	"builder/server/llm"
	"builder/server/runtime"
	"builder/shared/clientui"
	"builder/shared/transcript"
)

func EventFromRuntime(evt runtime.Event) clientui.Event {
	view := clientui.Event{
		Kind:             clientui.EventKind(evt.Kind),
		StepID:           evt.StepID,
		Error:            evt.Error,
		AssistantDelta:   evt.AssistantDelta,
		UserMessage:      evt.UserMessage,
		UserMessageBatch: append([]string(nil), evt.UserMessageBatch...),
	}
	if evt.ReasoningDelta != nil {
		view.ReasoningDelta = &clientui.ReasoningDelta{
			Key:  evt.ReasoningDelta.Key,
			Role: evt.ReasoningDelta.Role,
			Text: evt.ReasoningDelta.Text,
		}
	}
	if evt.RunState != nil {
		view.RunState = &clientui.RunState{Busy: evt.RunState.Busy}
	}
	if evt.Background != nil {
		view.Background = &clientui.BackgroundShellEvent{
			Type:              evt.Background.Type,
			ID:                evt.Background.ID,
			State:             evt.Background.State,
			Command:           evt.Background.Command,
			Workdir:           evt.Background.Workdir,
			LogPath:           evt.Background.LogPath,
			NoticeText:        evt.Background.NoticeText,
			CompactText:       evt.Background.CompactText,
			Preview:           evt.Background.Preview,
			Removed:           evt.Background.Removed,
			UserRequestedKill: evt.Background.UserRequestedKill,
			NoticeSuppressed:  evt.Background.NoticeSuppressed,
		}
		if evt.Background.ExitCode != nil {
			exitCode := *evt.Background.ExitCode
			view.Background.ExitCode = &exitCode
		}
	}
	return view
}

func ChatSnapshotFromRuntime(snapshot runtime.ChatSnapshot) clientui.ChatSnapshot {
	entries := make([]clientui.ChatEntry, 0, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		entries = append(entries, clientui.ChatEntry{
			Role:        entry.Role,
			Text:        entry.Text,
			OngoingText: entry.OngoingText,
			Phase:       string(entry.Phase),
			ToolCallID:  entry.ToolCallID,
			ToolCall:    cloneToolCallMeta(entry.ToolCall),
		})
	}
	return clientui.ChatSnapshot{
		Entries:      entries,
		Ongoing:      snapshot.Ongoing,
		OngoingError: snapshot.OngoingError,
	}
}

func cloneToolCallMeta(meta *transcript.ToolCallMeta) *transcript.ToolCallMeta {
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
	if meta.PatchRender != nil {
		patchRender := *meta.PatchRender
		copyMeta.PatchRender = &patchRender
	}
	return &copyMeta
}

func MessagePhase(raw string) llm.MessagePhase {
	return llm.MessagePhase(raw)
}
