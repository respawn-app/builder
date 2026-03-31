package app

import (
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
	update := clientui.ReduceRuntimeEvent(
		a.runtimeEventState(),
		a.pendingInputState(),
		m.activity == uiActivityRunning,
		evt,
	)
	a.applyRuntimeEventUpdate(update)
	if update.AssistantDelta != "" {
		if strings.TrimSpace(update.AssistantDelta) == uiNoopFinalToken {
			return nil
		}
		m.sawAssistantDelta = true
		m.forwardToView(tui.StreamAssistantMsg{Delta: update.AssistantDelta})
	}
	if update.ClearAssistantStream {
		m.sawAssistantDelta = false
		m.forwardToView(tui.ClearOngoingAssistantMsg{})
	}
	if update.ReasoningDelta != nil {
		m.forwardToView(tui.UpsertStreamingReasoningMsg{Key: update.ReasoningDelta.Key, Role: update.ReasoningDelta.Role, Text: update.ReasoningDelta.Text})
	}
	if update.ClearReasoningStream {
		m.forwardToView(tui.ClearStreamingReasoningMsg{})
	}
	if update.BackgroundNotice != nil {
		kind := uiStatusNoticeSuccess
		if update.BackgroundNotice.Kind == clientui.BackgroundNoticeError {
			kind = uiStatusNoticeError
		}
		return m.setTransientStatusWithKind(update.BackgroundNotice.Message, kind)
	}
	if update.SyncSessionView {
		if update.RecordPromptHistory {
			return sequenceCmds(a.syncConversationFromEngine(), m.recordPromptHistory(evt.UserMessage))
		}
		return a.syncConversationFromEngine()
	}
	return nil
}

func (a uiRuntimeAdapter) runtimeEventState() clientui.RuntimeEventState {
	m := a.model
	return clientui.RuntimeEventState{
		Busy:                  m.busy,
		Compacting:            m.compacting,
		ReviewerRunning:       m.reviewerRunning,
		ReviewerBlocking:      m.reviewerBlocking,
		ConversationFreshness: m.conversationFreshness,
		ReasoningStatusHeader: m.reasoningStatusHeader,
	}
}

func (a uiRuntimeAdapter) pendingInputState() clientui.PendingInputState {
	m := a.model
	return clientui.PendingInputState{
		Input:             m.input,
		PendingInjected:   append([]string(nil), m.pendingInjected...),
		LockedInjectText:  m.lockedInjectText,
		InputSubmitLocked: m.inputSubmitLocked,
	}
}

func (a uiRuntimeAdapter) applyRuntimeEventUpdate(update clientui.RuntimeEventUpdate) {
	m := a.model
	m.busy = update.State.Busy
	m.compacting = update.State.Compacting
	m.reviewerRunning = update.State.ReviewerRunning
	m.reviewerBlocking = update.State.ReviewerBlocking
	m.conversationFreshness = update.State.ConversationFreshness
	m.reasoningStatusHeader = update.State.ReasoningStatusHeader
	m.pendingInjected = update.Input.PendingInjected
	m.lockedInjectText = update.Input.LockedInjectText
	m.inputSubmitLocked = update.Input.InputSubmitLocked
	if update.ClearInput {
		m.clearInput()
	}
	if update.ClearPendingPreSubmit {
		m.pendingPreSubmitText = ""
	}
	if update.SetActivityRunning {
		m.activity = uiActivityRunning
	}
	if update.SetActivityIdle {
		m.activity = uiActivityIdle
	}
	if update.RefreshProcesses {
		m.refreshProcessEntries()
	}
}

func (a uiRuntimeAdapter) syncConversationFromEngine() tea.Cmd {
	m := a.model
	if !m.hasRuntimeClient() {
		return nil
	}
	return a.applyProjectedSessionView(m.runtimeSessionView())
}

func (a uiRuntimeAdapter) applyProjectedChatSnapshot(snapshot clientui.ChatSnapshot) tea.Cmd {
	view := a.model.runtimeSessionView()
	view.Chat = snapshot
	return a.applyProjectedSessionView(view)
}

func (a uiRuntimeAdapter) applyProjectedSessionView(view clientui.RuntimeSessionView) tea.Cmd {
	m := a.model
	if len(m.startupCmds) > 0 {
		m.startupCmds = nil
		m.nativeProjection = tui.TranscriptProjection{}
		m.nativeRenderedProjection = tui.TranscriptProjection{}
		m.nativeFlushedEntryCount = 0
		m.nativeRenderedSnapshot = ""
	}
	previousWindowTitle := m.windowTitle()
	m.sessionID = strings.TrimSpace(view.SessionID)
	m.sessionName = strings.TrimSpace(view.SessionName)
	m.conversationFreshness = view.ConversationFreshness
	entries := make([]tui.TranscriptEntry, 0, len(view.Chat.Entries))
	for _, entry := range view.Chat.Entries {
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
		Ongoing:      view.Chat.Ongoing,
		OngoingError: view.Chat.OngoingError,
	})
	if m.view.Mode() == tui.ModeOngoing {
		m.forwardToView(tui.SetOngoingScrollMsg{Scroll: m.view.OngoingScroll()})
	}
	if strings.TrimSpace(view.Chat.Ongoing) == "" {
		m.sawAssistantDelta = false
	}
	cmds := []tea.Cmd{m.syncNativeHistoryFromTranscript()}
	if previousWindowTitle != m.windowTitle() {
		cmds = append(cmds, tea.SetWindowTitle(m.windowTitle()))
	}
	return sequenceCmds(cmds...)
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
