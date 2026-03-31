package app

import (
	"fmt"
	"strings"

	"builder/cli/tui"
	"builder/server/llm"
	"builder/server/runtime"
	patchformat "builder/server/tools/patch/format"
	"builder/shared/clientui"
	"builder/shared/transcript"

	tea "github.com/charmbracelet/bubbletea"
)

const uiNoopFinalToken = "NO_OP"

type uiRuntimeAdapter struct {
	model *uiModel
}

func (a uiRuntimeAdapter) handleProjectedRuntimeEvent(evt clientui.Event) tea.Cmd {
	m := a.model
	switch evt.Kind {
	case clientui.EventConversationUpdated:
		return a.syncConversationFromEngine()
	case clientui.EventAssistantDelta:
		delta := evt.AssistantDelta
		if strings.TrimSpace(delta) == uiNoopFinalToken {
			return nil
		}
		m.sawAssistantDelta = delta != ""
		if delta != "" {
			m.forwardToView(tui.StreamAssistantMsg{Delta: delta})
		}
	case clientui.EventAssistantDeltaReset:
		m.sawAssistantDelta = false
		m.forwardToView(tui.ClearOngoingAssistantMsg{})
	case clientui.EventReasoningDelta:
		if evt.ReasoningDelta != nil {
			if header := extractReasoningStatusHeader(evt.ReasoningDelta.Text); header != "" {
				m.reasoningStatusHeader = header
			}
			m.forwardToView(tui.UpsertStreamingReasoningMsg{Key: evt.ReasoningDelta.Key, Role: evt.ReasoningDelta.Role, Text: evt.ReasoningDelta.Text})
		}
	case clientui.EventReasoningDeltaReset:
		m.forwardToView(tui.ClearStreamingReasoningMsg{})
	case clientui.EventCompactionStarted:
		m.compacting = true
	case clientui.EventCompactionCompleted, clientui.EventCompactionFailed:
		m.compacting = false
	case clientui.EventReviewerStarted:
		m.reviewerRunning = true
		m.reviewerBlocking = true
	case clientui.EventReviewerCompleted:
		m.clearReviewerState()
	case clientui.EventRunStateChanged:
		if evt.RunState != nil {
			m.busy = evt.RunState.Busy
			if evt.RunState.Busy {
				m.pendingPreSubmitText = ""
				m.activity = uiActivityRunning
			} else {
				if m.activity == uiActivityRunning {
					m.activity = uiActivityIdle
				}
				m.reasoningStatusHeader = ""
				m.forwardToView(tui.ClearStreamingReasoningMsg{})
			}
		}
	case clientui.EventBackgroundUpdated:
		m.refreshProcessEntries()
		if evt.Background != nil && (evt.Background.Type == "completed" || evt.Background.Type == "killed") {
			if evt.Background.NoticeSuppressed {
				return nil
			}
			kind := uiStatusNoticeSuccess
			if evt.Background.Type == "killed" && !evt.Background.UserRequestedKill {
				kind = uiStatusNoticeError
			}
			return m.setTransientStatusWithKind(fmt.Sprintf("background shell %s %s", evt.Background.ID, evt.Background.State), kind)
		}
	case clientui.EventUserMessageFlushed:
		shouldRecordHistory := a.onUserMessageFlushed(evt.UserMessage, evt.UserMessageBatch)
		if shouldRecordHistory {
			return sequenceCmds(a.syncConversationFromEngine(), m.recordPromptHistory(evt.UserMessage))
		}
		return a.syncConversationFromEngine()
	}
	return nil
}

func (a uiRuntimeAdapter) onUserMessageFlushed(text string, batch []string) bool {
	m := a.model
	m.conversationFreshness = clientui.ConversationFreshnessEstablished
	if len(batch) == 0 && strings.TrimSpace(text) != "" {
		batch = []string{text}
	}
	shouldRecordHistory := false
	consumed := 0
	for consumed < len(batch) && consumed < len(m.pendingInjected) {
		if strings.TrimSpace(m.pendingInjected[consumed]) != strings.TrimSpace(batch[consumed]) {
			break
		}
		consumed++
	}
	if consumed > 0 {
		m.pendingInjected = append([]string(nil), m.pendingInjected[consumed:]...)
		shouldRecordHistory = true
	}
	if m.inputSubmitLocked && strings.TrimSpace(m.lockedInjectText) == strings.TrimSpace(text) {
		if strings.TrimSpace(m.input) == strings.TrimSpace(m.lockedInjectText) {
			m.clearInput()
		}
		m.lockedInjectText = ""
		m.inputSubmitLocked = false
	}
	return shouldRecordHistory
}

func (a uiRuntimeAdapter) syncConversationFromEngine() tea.Cmd {
	m := a.model
	if m.engine == nil {
		return nil
	}
	m.conversationFreshness = m.runtimeStatus().ConversationFreshness
	return a.applyProjectedChatSnapshot(m.engine.ChatSnapshot())
}

func (a uiRuntimeAdapter) applyProjectedChatSnapshot(snapshot clientui.ChatSnapshot) tea.Cmd {
	m := a.model
	if len(m.startupCmds) > 0 {
		m.startupCmds = nil
		m.nativeProjection = tui.TranscriptProjection{}
		m.nativeRenderedProjection = tui.TranscriptProjection{}
		m.nativeFlushedEntryCount = 0
		m.nativeRenderedSnapshot = ""
	}
	entries := make([]tui.TranscriptEntry, 0, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		entries = append(entries, tui.TranscriptEntry{
			Role:        entry.Role,
			Text:        entry.Text,
			OngoingText: entry.OngoingText,
			Phase:       llm.MessagePhase(entry.Phase),
			ToolCallID:  entry.ToolCallID,
			ToolCall:    transcriptToolCallMeta(entry.ToolCall),
		})
	}
	m.transcriptEntries = append(m.transcriptEntries[:0], entries...)
	m.seedPromptHistoryFromTranscriptEntries(m.transcriptEntries)
	m.refreshRollbackCandidates()
	m.forwardToView(tui.ClearStreamingReasoningMsg{})
	m.forwardToView(tui.SetConversationMsg{
		Entries:      entries,
		Ongoing:      snapshot.Ongoing,
		OngoingError: snapshot.OngoingError,
	})
	if m.view.Mode() == tui.ModeOngoing {
		m.forwardToView(tui.SetOngoingScrollMsg{Scroll: m.view.OngoingScroll()})
	}
	if strings.TrimSpace(snapshot.Ongoing) == "" {
		m.sawAssistantDelta = false
	}
	return m.syncNativeHistoryFromTranscript()
}

func (a uiRuntimeAdapter) handleRuntimeEvent(evt runtime.Event) tea.Cmd {
	return a.handleProjectedRuntimeEvent(projectRuntimeEvent(evt))
}

func (a uiRuntimeAdapter) applyChatSnapshot(snapshot runtime.ChatSnapshot) tea.Cmd {
	return a.applyProjectedChatSnapshot(projectChatSnapshot(snapshot))
}

func waitRuntimeEvent(ch <-chan clientui.Event) tea.Cmd {
	return func() tea.Msg {
		evt, ok := <-ch
		if !ok {
			return nil
		}
		return runtimeEventMsg{event: evt}
	}
}

func waitAskEvent(ch <-chan askEvent) tea.Cmd {
	return func() tea.Msg {
		evt, ok := <-ch
		if !ok {
			return nil
		}
		return askEventMsg{event: evt}
	}
}

func (m *uiModel) handleRuntimeEvent(evt clientui.Event) {
	_ = m.runtimeAdapter().handleProjectedRuntimeEvent(evt)
}

func (m *uiModel) onUserMessageFlushed(text string) {
	_ = m.runtimeAdapter().onUserMessageFlushed(text, nil)
}

func (m *uiModel) syncConversationFromEngine() {
	_ = m.runtimeAdapter().syncConversationFromEngine()
}

func transcriptToolCallMeta(meta *clientui.ToolCallMeta) *transcript.ToolCallMeta {
	if meta == nil {
		return nil
	}
	out := &transcript.ToolCallMeta{
		ToolName:               meta.ToolName,
		Presentation:           transcript.ToolPresentationKind(meta.Presentation),
		RenderBehavior:         transcript.ToolCallRenderBehavior(meta.RenderBehavior),
		IsShell:                meta.IsShell,
		UserInitiated:          meta.UserInitiated,
		Command:                meta.Command,
		CompactText:            meta.CompactText,
		InlineMeta:             meta.InlineMeta,
		TimeoutLabel:           meta.TimeoutLabel,
		PatchSummary:           meta.PatchSummary,
		PatchDetail:            meta.PatchDetail,
		Question:               meta.Question,
		RecommendedOptionIndex: meta.RecommendedOptionIndex,
		OmitSuccessfulResult:   meta.OmitSuccessfulResult,
	}
	if len(meta.Suggestions) > 0 {
		out.Suggestions = append([]string(nil), meta.Suggestions...)
	}
	if meta.RenderHint != nil {
		out.RenderHint = &transcript.ToolRenderHint{
			Kind:       transcript.ToolRenderKind(meta.RenderHint.Kind),
			Path:       meta.RenderHint.Path,
			ResultOnly: meta.RenderHint.ResultOnly,
		}
	}
	if meta.PatchRender != nil {
		out.PatchRender = cloneRenderedPatch(meta.PatchRender)
	}
	return out
}

func cloneRenderedPatch(in *patchformat.RenderedPatch) *patchformat.RenderedPatch {
	if in == nil {
		return nil
	}
	out := &patchformat.RenderedPatch{}
	if len(in.Files) > 0 {
		out.Files = make([]patchformat.RenderedFile, 0, len(in.Files))
		for _, file := range in.Files {
			copyFile := file
			if len(file.Diff) > 0 {
				copyFile.Diff = append([]string(nil), file.Diff...)
			}
			out.Files = append(out.Files, copyFile)
		}
	}
	if len(in.SummaryLines) > 0 {
		out.SummaryLines = append([]patchformat.RenderedLine(nil), in.SummaryLines...)
	}
	if len(in.DetailLines) > 0 {
		out.DetailLines = append([]patchformat.RenderedLine(nil), in.DetailLines...)
	}
	return out
}
