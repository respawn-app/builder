package app

import (
	"strconv"
	"strings"

	"builder/cli/tui"
	"builder/server/llm"
	"builder/server/runtime"
	patchformat "builder/server/tools/patch/format"
	"builder/shared/clientui"
	"builder/shared/transcript"
	"builder/shared/transcriptdiag"

	tea "github.com/charmbracelet/bubbletea"
)

const uiNoopFinalToken = "NO_OP"

type uiRuntimeAdapter struct {
	model *uiModel
}

type runtimeEventApplyResult struct {
	cmd               tea.Cmd
	transcriptMutated bool
	awaitsHydration   bool
}

type projectedTranscriptEntryPlanMode uint8

const (
	projectedTranscriptEntryPlanSkip projectedTranscriptEntryPlanMode = iota + 1
	projectedTranscriptEntryPlanAppend
	projectedTranscriptEntryPlanReplace
	projectedTranscriptEntryPlanHydrate
)

type projectedTranscriptEntryPlan struct {
	mode       projectedTranscriptEntryPlanMode
	rangeStart int
	rangeEnd   int
	entries    []clientui.ChatEntry
}

func (a uiRuntimeAdapter) handleProjectedRuntimeEvent(evt clientui.Event) tea.Cmd {
	return a.applyProjectedRuntimeEvent(evt, true).cmd
}

func (a uiRuntimeAdapter) handleProjectedRuntimeEventsBatch(events []clientui.Event) tea.Cmd {
	return a.applyProjectedRuntimeEventsBatch(events).cmd
}

func (a uiRuntimeAdapter) applyProjectedRuntimeEventsBatch(events []clientui.Event) runtimeEventApplyResult {
	cmds := make([]tea.Cmd, 0, len(events)+1)
	transcriptMutated := false
	awaitsHydration := false
	for _, evt := range events {
		result := a.applyProjectedRuntimeEvent(evt, false)
		cmds = append(cmds, result.cmd)
		transcriptMutated = transcriptMutated || result.transcriptMutated
		awaitsHydration = awaitsHydration || result.awaitsHydration
	}
	batchedCmd := batchCmds(cmds...)
	if !transcriptMutated {
		return runtimeEventApplyResult{cmd: batchedCmd, awaitsHydration: awaitsHydration}
	}
	nativeCmd := a.model.syncNativeHistoryFromTranscript()
	return runtimeEventApplyResult{cmd: sequenceCmds(nativeCmd, batchedCmd), transcriptMutated: true, awaitsHydration: awaitsHydration}
}

func (a uiRuntimeAdapter) applyProjectedRuntimeEvent(evt clientui.Event, flushNativeHistory bool) runtimeEventApplyResult {
	m := a.model
	if m.turnQueueHook != nil {
		m.turnQueueHook.OnProjectedRuntimeEvent(evt)
	}
	update := clientui.ReduceRuntimeEvent(
		a.runtimeEventState(),
		a.pendingInputState(),
		m.activity == uiActivityRunning,
		evt,
	)
	effectiveSyncSessionView := update.SyncSessionView
	if evt.Kind == clientui.EventConversationUpdated && len(evt.TranscriptEntries) > 0 {
		effectiveSyncSessionView = false
	}
	m.logTranscriptEventDiag("transcript.diag.client.apply_event", evt, map[string]string{
		"path":                  "live_event",
		"sync_session_view":     strconv.FormatBool(effectiveSyncSessionView),
		"record_prompt_history": strconv.FormatBool(update.RecordPromptHistory),
	})
	a.applyRuntimeEventUpdate(update)
	cmds := make([]tea.Cmd, 0, 4)
	transcriptMutated := false
	awaitsHydration := false
	if evt.Kind == clientui.EventConversationUpdated {
		m.invalidateTransientTranscriptState()
	}
	if len(evt.TranscriptEntries) > 0 {
		cmd, mutated, needsHydration := a.applyProjectedTranscriptEntries(evt, flushNativeHistory)
		cmds = append(cmds, cmd)
		transcriptMutated = transcriptMutated || mutated
		awaitsHydration = awaitsHydration || needsHydration
		if shouldClearAssistantStreamForCommittedAssistantEvent(evt) && (mutated || skippedAssistantCommitMatchesActiveLiveStream(m, evt)) {
			if stepID := strings.TrimSpace(evt.StepID); stepID != "" {
				m.lastCommittedAssistantStepID = stepID
			}
			m.sawAssistantDelta = false
			m.forwardToView(tui.ClearOngoingAssistantMsg{})
		}
	}
	if update.AssistantDelta != "" {
		if shouldIgnoreStaleAssistantDelta(m, evt, update.AssistantDelta) {
			update.AssistantDelta = ""
		} else if strings.TrimSpace(update.AssistantDelta) == uiNoopFinalToken {
			update.AssistantDelta = ""
		} else {
			m.sawAssistantDelta = true
			m.forwardToView(tui.StreamAssistantMsg{Delta: update.AssistantDelta})
		}
	}
	if update.ClearAssistantStream {
		if evt.Kind == clientui.EventAssistantDeltaReset {
			if stepID := strings.TrimSpace(evt.StepID); stepID != "" {
				m.lastCommittedAssistantStepID = stepID
			}
		}
		m.sawAssistantDelta = false
		m.forwardToView(tui.ClearOngoingAssistantMsg{})
	}
	if update.ReasoningDelta != nil {
		m.reasoningLiveDirty = true
		m.forwardToView(tui.UpsertStreamingReasoningMsg{Key: update.ReasoningDelta.Key, Role: update.ReasoningDelta.Role, Text: update.ReasoningDelta.Text})
	}
	if update.ClearReasoningStream {
		m.reasoningLiveDirty = false
		m.forwardToView(tui.ClearStreamingReasoningMsg{})
	}
	if update.BackgroundNotice != nil {
		kind := uiStatusNoticeSuccess
		if update.BackgroundNotice.Kind == clientui.BackgroundNoticeError {
			kind = uiStatusNoticeError
		}
		cmds = append(cmds, m.setTransientStatusWithKind(update.BackgroundNotice.Message, kind))
	}
	if update.RecordPromptHistory && strings.TrimSpace(evt.UserMessage) != "" {
		cmds = append(cmds, m.recordPromptHistory(evt.UserMessage))
	}
	if effectiveSyncSessionView {
		cmds = append(cmds, a.syncConversationFromEngine())
		awaitsHydration = awaitsHydration || shouldPauseRuntimeEventsForHydration(m)
	}
	return runtimeEventApplyResult{cmd: batchCmds(cmds...), transcriptMutated: transcriptMutated, awaitsHydration: awaitsHydration}
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
		PendingInjected:   m.pendingInjected,
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
		m.refreshProcessEntriesIfOpen()
	}
}

func (a uiRuntimeAdapter) syncConversationFromEngine() tea.Cmd {
	m := a.model
	if !m.hasRuntimeClient() {
		return nil
	}
	return m.requestRuntimeTranscriptSync()
}

func (a uiRuntimeAdapter) applyProjectedTranscriptEntries(evt clientui.Event, flushNativeHistory bool) (tea.Cmd, bool, bool) {
	m := a.model
	entries := cloneChatEntries(evt.TranscriptEntries)
	incomingCount := len(entries)
	if shouldSkipProjectedToolCallStart(m, evt) {
		m.logTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.append_entries", map[string]string{
			"session_id":            strings.TrimSpace(m.sessionID),
			"mode":                  m.transcriptModeLabel(),
			"path":                  "live_event",
			"incoming_count":        strconv.Itoa(incomingCount),
			"reason":                "duplicate_tool_call_start",
			"applied_count":         "0",
			"event_revision":        strconv.FormatInt(evt.TranscriptRevision, 10),
			"event_committed_count": strconv.Itoa(evt.CommittedEntryCount),
		}))
		return nil, false, false
	}
	if shouldDeferProjectedUserMessageFlushAppend(m, evt) {
		m.logTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.append_entries", map[string]string{
			"session_id":            strings.TrimSpace(m.sessionID),
			"mode":                  m.transcriptModeLabel(),
			"path":                  "live_event",
			"incoming_count":        strconv.Itoa(incomingCount),
			"reason":                "defer_user_flush_until_assistant_catch_up",
			"applied_count":         "0",
			"event_revision":        strconv.FormatInt(evt.TranscriptRevision, 10),
			"event_committed_count": strconv.Itoa(evt.CommittedEntryCount),
		}))
		if m.hasRuntimeClient() {
			return m.requestRuntimeTranscriptSync(), false, true
		}
		return nil, false, false
	}
	plan := planProjectedTranscriptEntries(m, evt)
	switch plan.mode {
	case projectedTranscriptEntryPlanSkip:
		m.logTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.append_entries", map[string]string{
			"session_id":            strings.TrimSpace(m.sessionID),
			"mode":                  m.transcriptModeLabel(),
			"path":                  "live_event",
			"incoming_count":        strconv.Itoa(incomingCount),
			"reason":                "already_hydrated",
			"applied_count":         "0",
			"event_revision":        strconv.FormatInt(evt.TranscriptRevision, 10),
			"event_committed_count": strconv.Itoa(evt.CommittedEntryCount),
		}))
		return nil, false, false
	case projectedTranscriptEntryPlanHydrate:
		m.logTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.append_entries", map[string]string{
			"session_id":            strings.TrimSpace(m.sessionID),
			"mode":                  m.transcriptModeLabel(),
			"path":                  "live_event",
			"incoming_count":        strconv.Itoa(incomingCount),
			"reason":                "requires_hydration",
			"applied_count":         "0",
			"event_revision":        strconv.FormatInt(evt.TranscriptRevision, 10),
			"event_committed_count": strconv.Itoa(evt.CommittedEntryCount),
		}))
		if m.hasRuntimeClient() {
			return m.requestRuntimeTranscriptSync(), false, true
		}
		return nil, false, false
	}
	entries = plan.entries
	m.transcriptLiveDirty = true
	startOffset := m.transcriptBaseOffset + plan.rangeStart
	projectedEntriesTransient := m.hasRuntimeClient() && evt.Kind != clientui.EventConversationUpdated
	convertedEntries := make([]tui.TranscriptEntry, 0, len(entries))
	for _, entry := range entries {
		convertedEntries = append(convertedEntries, transcriptEntryFromProjectedChatEntry(entry, projectedEntriesTransient))
	}
	showTransientInCurrentView := m.view.Mode() != tui.ModeDetail || !allTranscriptEntriesTransient(convertedEntries)
	if plan.mode == projectedTranscriptEntryPlanAppend {
		for _, transcriptEntry := range convertedEntries {
			m.transcriptEntries = append(m.transcriptEntries, transcriptEntry)
			if showTransientInCurrentView {
				m.forwardToView(tui.AppendTranscriptMsg{
					Visibility:  transcriptEntry.Visibility,
					Transient:   transcriptEntry.Transient,
					Role:        transcriptEntry.Role,
					Text:        transcriptEntry.Text,
					OngoingText: transcriptEntry.OngoingText,
					Phase:       transcriptEntry.Phase,
					ToolCallID:  transcriptEntry.ToolCallID,
					ToolCall:    transcriptEntry.ToolCall,
				})
			}
		}
	} else {
		prefix := append([]tui.TranscriptEntry(nil), m.transcriptEntries[:plan.rangeStart]...)
		suffix := append([]tui.TranscriptEntry(nil), m.transcriptEntries[plan.rangeEnd:]...)
		m.transcriptEntries = append(prefix, convertedEntries...)
		m.transcriptEntries = append(m.transcriptEntries, suffix...)
	}
	m.transcriptRevision = max(m.transcriptRevision, evt.TranscriptRevision)
	m.transcriptTotalEntries = max(m.transcriptTotalEntries, max(evt.CommittedEntryCount, m.transcriptBaseOffset+len(m.transcriptEntries)))
	m.refreshRollbackCandidates()
	if m.detailTranscript.loaded && !allTranscriptEntriesTransient(convertedEntries) {
		page := clientui.TranscriptPage{
			Revision:     m.transcriptRevision,
			Offset:       startOffset,
			TotalEntries: m.transcriptTotalEntries,
			Entries:      cloneChatEntries(entries),
			Ongoing:      m.view.OngoingStreamingText(),
			OngoingError: m.view.OngoingErrorText(),
		}
		m.detailTranscript.apply(page)
	}
	if plan.mode == projectedTranscriptEntryPlanReplace && showTransientInCurrentView {
		m.forwardToView(tui.SetConversationMsg{
			BaseOffset:   m.transcriptBaseOffset,
			TotalEntries: m.transcriptTotalEntries,
			Entries:      append([]tui.TranscriptEntry(nil), m.transcriptEntries...),
			Ongoing:      m.view.OngoingStreamingText(),
			OngoingError: m.view.OngoingErrorText(),
		})
	}
	if plan.mode == projectedTranscriptEntryPlanAppend && m.view.Mode() == tui.ModeDetail && !allTranscriptEntriesTransient(convertedEntries) && m.view.TranscriptBaseOffset() == m.transcriptBaseOffset {
		m.forwardToView(tui.SetConversationMsg{
			BaseOffset:   m.transcriptBaseOffset,
			TotalEntries: m.transcriptTotalEntries,
			Entries:      append([]tui.TranscriptEntry(nil), m.transcriptEntries...),
			Ongoing:      m.view.OngoingStreamingText(),
			OngoingError: m.view.OngoingErrorText(),
		})
	}
	if showTransientInCurrentView && m.view.Mode() == tui.ModeOngoing {
		m.forwardToView(tui.SetOngoingScrollMsg{Scroll: m.view.OngoingScroll()})
	}
	if !flushNativeHistory {
		m.logTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.append_entries", map[string]string{
			"session_id":            strings.TrimSpace(m.sessionID),
			"mode":                  m.transcriptModeLabel(),
			"path":                  "live_event",
			"incoming_count":        strconv.Itoa(incomingCount),
			"applied_count":         strconv.Itoa(len(entries)),
			"start_offset":          strconv.Itoa(startOffset),
			"entries_digest":        transcriptdiag.EntriesDigest(entries),
			"reconcile_mode":        plan.mode.label(),
			"event_revision":        strconv.FormatInt(evt.TranscriptRevision, 10),
			"event_committed_count": strconv.Itoa(evt.CommittedEntryCount),
			"transcript_revision":   strconv.FormatInt(m.transcriptRevision, 10),
			"transcript_total":      strconv.Itoa(m.transcriptTotalEntries),
		}))
		return nil, true, false
	}
	m.logTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.append_entries", map[string]string{
		"session_id":            strings.TrimSpace(m.sessionID),
		"mode":                  m.transcriptModeLabel(),
		"path":                  "live_event",
		"incoming_count":        strconv.Itoa(incomingCount),
		"applied_count":         strconv.Itoa(len(entries)),
		"start_offset":          strconv.Itoa(startOffset),
		"entries_digest":        transcriptdiag.EntriesDigest(entries),
		"reconcile_mode":        plan.mode.label(),
		"event_revision":        strconv.FormatInt(evt.TranscriptRevision, 10),
		"event_committed_count": strconv.Itoa(evt.CommittedEntryCount),
		"transcript_revision":   strconv.FormatInt(m.transcriptRevision, 10),
		"transcript_total":      strconv.Itoa(m.transcriptTotalEntries),
		"native_history_sync":   "true",
	}))
	return m.syncNativeHistoryFromTranscript(), true, false
}

func (a uiRuntimeAdapter) applyProjectedChatSnapshot(snapshot clientui.ChatSnapshot) tea.Cmd {
	page := a.model.runtimeTranscript()
	page.Entries = cloneTranscriptEntries(snapshot.Entries)
	page.TotalEntries = len(page.Entries)
	page.Offset = 0
	page.NextOffset = 0
	page.HasMore = false
	page.Ongoing = snapshot.Ongoing
	page.OngoingError = snapshot.OngoingError
	return a.applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, page)
}

func (a uiRuntimeAdapter) applyProjectedSessionView(view clientui.RuntimeSessionView) tea.Cmd {
	transcript := transcriptPageFromSessionView(view)
	return sequenceCmds(a.applyProjectedSessionMetadata(view), a.applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, transcript))
}

func (a uiRuntimeAdapter) applyProjectedSessionMetadata(view clientui.RuntimeSessionView) tea.Cmd {
	m := a.model
	if len(m.startupCmds) > 0 {
		m.startupCmds = nil
	}
	previousWindowTitle := m.windowTitle()
	if transcriptPageSessionChanged(m.sessionID, view.SessionID) {
		m.detailTranscript.reset()
		m.transcriptRevision = 0
		m.transcriptLiveDirty = false
		m.reasoningLiveDirty = false
	}
	m.sessionID = strings.TrimSpace(view.SessionID)
	m.sessionName = strings.TrimSpace(view.SessionName)
	m.conversationFreshness = view.ConversationFreshness
	if view.Transcript.Revision > m.transcriptRevision {
		m.transcriptRevision = view.Transcript.Revision
	}
	if previousWindowTitle != m.windowTitle() {
		return tea.SetWindowTitle(m.windowTitle())
	}
	return nil
}

func (a uiRuntimeAdapter) applyProjectedTranscriptPage(page clientui.TranscriptPage) tea.Cmd {
	return a.applyRuntimeTranscriptPage(clientui.TranscriptPageRequest{}, page)
}

func (a uiRuntimeAdapter) applyRuntimeTranscriptPage(req clientui.TranscriptPageRequest, page clientui.TranscriptPage) tea.Cmd {
	m := a.model
	m.logTranscriptPageDiag("transcript.diag.client.apply_page_start", req, page, map[string]string{"path": "hydrate"})
	if len(m.startupCmds) > 0 {
		m.startupCmds = nil
		m.nativeProjection = tui.TranscriptProjection{}
		m.nativeRenderedProjection = tui.TranscriptProjection{}
		m.nativeFlushedEntryCount = 0
		m.nativeRenderedSnapshot = ""
	}
	previousWindowTitle := m.windowTitle()
	if transcriptPageSessionChanged(m.sessionID, page.SessionID) {
		m.detailTranscript.reset()
		m.transcriptRevision = 0
		m.transcriptLiveDirty = false
		m.reasoningLiveDirty = false
	}
	m.sessionID = strings.TrimSpace(page.SessionID)
	if strings.TrimSpace(page.SessionName) != "" {
		m.sessionName = strings.TrimSpace(page.SessionName)
	}
	m.conversationFreshness = page.ConversationFreshness
	pageReq := req
	if pageReq.Window == clientui.TranscriptWindowDefault && transcriptPageLooksLikeOngoingTail(page) && m.view.Mode() == tui.ModeOngoing {
		pageReq.Window = clientui.TranscriptWindowOngoingTail
	}
	entries := transcriptEntriesFromPage(page)
	if authoritativePageDuplicatesCommittedAssistantOngoing(entries, page.Ongoing, m.view.OngoingStreamingText()) {
		page.Ongoing = ""
		page.OngoingError = ""
	}
	if reason := transcriptPageReplacementRejectReason(m, pageReq, page); reason != "" {
		m.logTranscriptPageDiag("transcript.diag.client.apply_page_reject", pageReq, page, map[string]string{"path": "hydrate", "reason": reason})
		if previousWindowTitle != m.windowTitle() {
			return tea.SetWindowTitle(m.windowTitle())
		}
		return nil
	}
	shouldSyncNativeHistory := pageReq.Window == clientui.TranscriptWindowOngoingTail || pageReq == (clientui.TranscriptPageRequest{})
	preserveLiveReasoning := shouldPreserveLiveReasoning(m, page)
	if shouldSyncNativeHistory {
		a.applyAuthoritativeOngoingTailPage(page, entries, preserveLiveReasoning)
	}
	if pageReq.Window == clientui.TranscriptWindowOngoingTail || (pageReq == (clientui.TranscriptPageRequest{}) && m.view.Mode() != tui.ModeDetail) {
		m.detailTranscript.syncTail(page)
		if m.view.Mode() != tui.ModeDetail {
			if !preserveLiveReasoning {
				m.forwardToView(tui.ClearStreamingReasoningMsg{})
			}
			m.forwardToView(tui.SetConversationMsg{
				BaseOffset:   page.Offset,
				TotalEntries: page.TotalEntries,
				Entries:      entries,
				Ongoing:      page.Ongoing,
				OngoingError: page.OngoingError,
			})
		}
	} else {
		if m.view.Mode() == tui.ModeDetail && m.detailTranscript.matchesPage(page) {
			m.transcriptRevision = max(m.transcriptRevision, page.Revision)
			if previousWindowTitle != m.windowTitle() {
				return tea.SetWindowTitle(m.windowTitle())
			}
			return nil
		}
		m.detailTranscript.apply(page)
		m.transcriptRevision = max(m.transcriptRevision, page.Revision)
		if !preserveLiveReasoning {
			m.reasoningLiveDirty = false
		}
		detailPage := m.detailTranscript.page()
		detailPage.SessionID = page.SessionID
		detailPage.SessionName = page.SessionName
		detailPage.ConversationFreshness = page.ConversationFreshness
		detailPage.Revision = page.Revision
		if m.view.Mode() == tui.ModeDetail {
			if !preserveLiveReasoning {
				m.forwardToView(tui.ClearStreamingReasoningMsg{})
			}
			m.forwardToView(tui.SetConversationMsg{
				BaseOffset:   detailPage.Offset,
				TotalEntries: detailPage.TotalEntries,
				Entries:      transcriptEntriesFromPage(detailPage),
				Ongoing:      detailPage.Ongoing,
				OngoingError: detailPage.OngoingError,
			})
		}
	}
	if m.view.Mode() == tui.ModeOngoing {
		m.forwardToView(tui.SetOngoingScrollMsg{Scroll: m.view.OngoingScroll()})
	}
	if strings.TrimSpace(page.Ongoing) == "" {
		m.sawAssistantDelta = false
	}
	cmds := make([]tea.Cmd, 0, 2)
	if shouldSyncNativeHistory {
		cmds = append(cmds, m.syncNativeHistoryFromTranscript())
	}
	m.logTranscriptPageDiag("transcript.diag.client.apply_page_commit", pageReq, page, map[string]string{
		"path":                      "hydrate",
		"branch":                    transcriptPageApplyBranch(pageReq, m),
		"preserve_live_reasoning":   strconv.FormatBool(preserveLiveReasoning),
		"transcript_revision_after": strconv.FormatInt(m.transcriptRevision, 10),
		"transcript_total_after":    strconv.Itoa(m.transcriptTotalEntries),
		"native_history_sync":       strconv.FormatBool(shouldSyncNativeHistory),
	})
	if previousWindowTitle != m.windowTitle() {
		cmds = append(cmds, tea.SetWindowTitle(m.windowTitle()))
	}
	return sequenceCmds(cmds...)
}

func (a uiRuntimeAdapter) applyAuthoritativeOngoingTailPage(page clientui.TranscriptPage, entries []tui.TranscriptEntry, preserveLiveReasoning bool) {
	m := a.model
	if m == nil {
		return
	}
	m.transcriptBaseOffset = page.Offset
	m.transcriptEntries = append(m.transcriptEntries[:0], entries...)
	m.transcriptTotalEntries = max(page.TotalEntries, page.Offset+len(entries))
	m.transcriptRevision = max(m.transcriptRevision, page.Revision)
	m.transcriptLiveDirty = false
	if !preserveLiveReasoning {
		m.reasoningLiveDirty = false
	}
	m.seedPromptHistoryFromTranscriptEntries(m.transcriptEntries)
	m.refreshRollbackCandidates()
}

func (m *uiModel) invalidateTransientTranscriptState() {
	if m == nil {
		return
	}
	hadTransient := false
	committed := make([]tui.TranscriptEntry, 0, len(m.transcriptEntries))
	for _, entry := range m.transcriptEntries {
		if entry.Transient {
			hadTransient = true
			continue
		}
		committed = append(committed, entry)
	}
	if hadTransient {
		m.transcriptEntries = committed
		m.refreshRollbackCandidates()
	}
	m.transcriptLiveDirty = false
	m.reasoningLiveDirty = false
	m.sawAssistantDelta = false
	if m.detailTranscript.loaded {
		m.detailTranscript.ongoing = ""
		m.detailTranscript.ongoingError = ""
	}
	if !hadTransient && strings.TrimSpace(m.view.OngoingStreamingText()) == "" && strings.TrimSpace(m.view.OngoingErrorText()) == "" {
		return
	}
	m.forwardToView(tui.ClearStreamingReasoningMsg{})
	page := m.localRuntimeTranscript()
	if m.view.Mode() == tui.ModeDetail && m.detailTranscript.loaded {
		page = m.detailTranscript.page()
	}
	m.forwardToView(tui.SetConversationMsg{
		BaseOffset:   page.Offset,
		TotalEntries: page.TotalEntries,
		Entries:      transcriptEntriesFromPage(page),
		Ongoing:      "",
		OngoingError: "",
	})
	if m.view.Mode() == tui.ModeOngoing {
		m.forwardToView(tui.SetOngoingScrollMsg{Scroll: m.view.OngoingScroll()})
	}
}

func authoritativePageDuplicatesCommittedAssistantOngoing(entries []tui.TranscriptEntry, pageOngoing string, liveOngoing string) bool {
	trimmedPageOngoing := strings.TrimSpace(pageOngoing)
	trimmedLiveOngoing := strings.TrimSpace(liveOngoing)
	if trimmedPageOngoing != "" || trimmedLiveOngoing == "" {
		return false
	}
	for idx := len(entries) - 1; idx >= 0; idx-- {
		entry := entries[idx]
		if strings.TrimSpace(entry.Text) == "" && strings.TrimSpace(entry.OngoingText) == "" {
			continue
		}
		if strings.TrimSpace(entry.Role) != "assistant" {
			return false
		}
		return strings.TrimSpace(entry.Text) == trimmedLiveOngoing
	}
	return false
}

func shouldRejectTranscriptPageReplacement(m *uiModel, req clientui.TranscriptPageRequest, page clientui.TranscriptPage) bool {
	return transcriptPageReplacementRejectReason(m, req, page) != ""
}

func transcriptPageReplacementRejectReason(m *uiModel, req clientui.TranscriptPageRequest, page clientui.TranscriptPage) string {
	if m == nil || page.Revision <= 0 {
		return ""
	}
	if page.Revision < m.transcriptRevision {
		return "stale_revision"
	}
	replacesOngoingTail := req.Window == clientui.TranscriptWindowOngoingTail || (req == (clientui.TranscriptPageRequest{}) && m.view.Mode() != tui.ModeDetail)
	if !replacesOngoingTail {
		return ""
	}
	if page.Revision == m.transcriptRevision && strings.TrimSpace(m.view.OngoingStreamingText()) != "" && strings.TrimSpace(page.Ongoing) == "" {
		if authoritativePageDuplicatesCommittedAssistantOngoing(transcriptEntriesFromPage(page), page.Ongoing, m.view.OngoingStreamingText()) {
			return ""
		}
		return "same_revision_would_clear_ongoing"
	}
	if m.transcriptLiveDirty && page.Revision == m.transcriptRevision && shouldAcceptEqualRevisionTailReplacement(m, page) {
		return ""
	}
	if m.transcriptLiveDirty && page.Revision <= m.transcriptRevision {
		return "live_dirty_same_or_older_revision"
	}
	return ""
}

func shouldAcceptEqualRevisionTailReplacement(m *uiModel, page clientui.TranscriptPage) bool {
	if m == nil {
		return false
	}
	currentStart := m.transcriptBaseOffset
	currentEnd := currentStart + len(m.transcriptEntries)
	pageStart := page.Offset
	pageEnd := page.Offset + len(page.Entries)
	if pageStart > currentStart || pageEnd < currentEnd {
		return false
	}
	overlapStart := max(currentStart, pageStart)
	overlapEnd := min(currentEnd, pageEnd)
	if overlapStart >= overlapEnd {
		return pageEnd > currentEnd || m.view.OngoingStreamingText() != page.Ongoing || m.view.OngoingErrorText() != page.OngoingError
	}
	hasOverlapDiff := false
	for absolute := overlapStart; absolute < overlapEnd; absolute++ {
		currentIndex := absolute - currentStart
		pageIndex := absolute - pageStart
		if !transcriptEntryMatchesChatEntry(m.transcriptEntries[currentIndex], page.Entries[pageIndex]) {
			hasOverlapDiff = true
			break
		}
	}
	if hasOverlapDiff {
		return true
	}
	if pageEnd > currentEnd {
		return true
	}
	if m.view.OngoingStreamingText() != page.Ongoing {
		return true
	}
	if m.view.OngoingErrorText() != page.OngoingError {
		return true
	}
	return false
}

func transcriptPageApplyBranch(req clientui.TranscriptPageRequest, m *uiModel) string {
	if req.Window == clientui.TranscriptWindowOngoingTail || (req == (clientui.TranscriptPageRequest{}) && m != nil && m.view.Mode() != tui.ModeDetail) {
		return "ongoing_tail_replace"
	}
	return "detail_merge"
}

func shouldPreserveLiveReasoning(m *uiModel, page clientui.TranscriptPage) bool {
	if m == nil {
		return false
	}
	if !m.reasoningLiveDirty {
		return false
	}
	if page.Revision <= 0 {
		return true
	}
	return page.Revision <= m.transcriptRevision
}

func transcriptEntriesFromPage(page clientui.TranscriptPage) []tui.TranscriptEntry {
	entries := make([]tui.TranscriptEntry, 0, len(page.Entries))
	for _, entry := range page.Entries {
		entries = append(entries, transcriptEntryFromProjectedChatEntry(entry, false))
	}
	return entries
}

func transcriptEntryFromProjectedChatEntry(entry clientui.ChatEntry, transient bool) tui.TranscriptEntry {
	return tui.TranscriptEntry{
		Visibility:  entry.Visibility,
		Transient:   transient,
		Role:        entry.Role,
		Text:        entry.Text,
		OngoingText: entry.OngoingText,
		Phase:       llm.MessagePhase(entry.Phase),
		ToolCallID:  entry.ToolCallID,
		ToolCall:    transcriptToolCallMeta(entry.ToolCall),
	}
}

func allTranscriptEntriesTransient(entries []tui.TranscriptEntry) bool {
	if len(entries) == 0 {
		return false
	}
	for _, entry := range entries {
		if !entry.Transient {
			return false
		}
	}
	return true
}

func cloneChatEntries(entries []clientui.ChatEntry) []clientui.ChatEntry {
	if len(entries) == 0 {
		return nil
	}
	cloned := make([]clientui.ChatEntry, 0, len(entries))
	for _, entry := range entries {
		copyEntry := entry
		copyEntry.ToolCallID = strings.TrimSpace(copyEntry.ToolCallID)
		copyEntry.ToolCall = transcriptToolCallMetaClient(transcriptToolCallMeta(entry.ToolCall))
		cloned = append(cloned, copyEntry)
	}
	return cloned
}

func planProjectedTranscriptEntries(m *uiModel, evt clientui.Event) projectedTranscriptEntryPlan {
	entries := cloneChatEntries(evt.TranscriptEntries)
	plan := projectedTranscriptEntryPlan{
		mode:       projectedTranscriptEntryPlanAppend,
		rangeStart: 0,
		rangeEnd:   0,
		entries:    entries,
	}
	if m == nil {
		return plan
	}
	plan.rangeStart = len(m.transcriptEntries)
	plan.rangeEnd = len(m.transcriptEntries)
	if len(entries) == 0 || !eventTranscriptEntriesReconcileWithCommittedTail(evt.Kind) {
		return plan
	}
	if evt.CommittedEntryCount <= 0 && evt.TranscriptRevision <= 0 {
		return plan
	}
	eventEnd := evt.CommittedEntryCount
	eventStart := eventEnd - len(entries)
	if eventStart < 0 {
		return projectedTranscriptEntryPlan{mode: projectedTranscriptEntryPlanHydrate}
	}
	currentStart := m.transcriptBaseOffset
	currentEnd := currentStart + len(m.transcriptEntries)
	if eventEnd <= currentStart {
		return projectedTranscriptEntryPlan{mode: projectedTranscriptEntryPlanSkip}
	}
	if eventStart < currentStart {
		return projectedTranscriptEntryPlan{mode: projectedTranscriptEntryPlanHydrate}
	}
	if evt.TranscriptRevision < m.transcriptRevision {
		if eventEnd > currentEnd {
			return projectedTranscriptEntryPlan{mode: projectedTranscriptEntryPlanHydrate}
		}
		if projectedTranscriptEntriesMatchCurrentRange(m, eventStart, entries) {
			return projectedTranscriptEntryPlan{mode: projectedTranscriptEntryPlanSkip}
		}
		return projectedTranscriptEntryPlan{mode: projectedTranscriptEntryPlanSkip}
	}
	if eventStart > currentEnd {
		return projectedTranscriptEntryPlan{mode: projectedTranscriptEntryPlanHydrate}
	}
	overlapStart := max(eventStart, currentStart)
	overlapEnd := min(eventEnd, currentEnd)
	if projectedTranscriptEntriesMatchCurrentOverlap(m, eventStart, overlapStart, overlapEnd, entries) {
		if eventEnd <= currentEnd {
			return projectedTranscriptEntryPlan{mode: projectedTranscriptEntryPlanSkip}
		}
		suffixStart := currentEnd - eventStart
		return projectedTranscriptEntryPlan{
			mode:       projectedTranscriptEntryPlanAppend,
			rangeStart: len(m.transcriptEntries),
			rangeEnd:   len(m.transcriptEntries),
			entries:    cloneChatEntries(entries[suffixStart:]),
		}
	}
	return projectedTranscriptEntryPlan{
		mode:       projectedTranscriptEntryPlanReplace,
		rangeStart: eventStart - currentStart,
		rangeEnd:   min(eventEnd, currentEnd) - currentStart,
		entries:    entries,
	}
}

func projectedTranscriptEntriesMatchCurrentRange(m *uiModel, eventStart int, entries []clientui.ChatEntry) bool {
	if m == nil {
		return false
	}
	currentStart := m.transcriptBaseOffset
	currentEnd := currentStart + len(m.transcriptEntries)
	eventEnd := eventStart + len(entries)
	if eventStart < currentStart || eventEnd > currentEnd {
		return false
	}
	return projectedTranscriptEntriesMatchCurrentOverlap(m, eventStart, eventStart, eventEnd, entries)
}

func projectedTranscriptEntriesMatchCurrentOverlap(m *uiModel, eventStart int, overlapStart int, overlapEnd int, entries []clientui.ChatEntry) bool {
	if m == nil {
		return false
	}
	if overlapStart >= overlapEnd {
		return true
	}
	currentStart := m.transcriptBaseOffset
	for absolute := overlapStart; absolute < overlapEnd; absolute++ {
		currentIndex := absolute - currentStart
		incomingIndex := absolute - eventStart
		if !transcriptEntryMatchesChatEntry(m.transcriptEntries[currentIndex], entries[incomingIndex]) {
			return false
		}
	}
	return true
}

func (mode projectedTranscriptEntryPlanMode) label() string {
	switch mode {
	case projectedTranscriptEntryPlanSkip:
		return "skip"
	case projectedTranscriptEntryPlanAppend:
		return "append"
	case projectedTranscriptEntryPlanReplace:
		return "replace"
	case projectedTranscriptEntryPlanHydrate:
		return "hydrate"
	default:
		return "unknown"
	}
}

func shouldSkipProjectedToolCallStart(m *uiModel, evt clientui.Event) bool {
	if m == nil || evt.Kind != clientui.EventToolCallStarted || len(evt.TranscriptEntries) == 0 {
		return false
	}
	matched := false
	for _, entry := range evt.TranscriptEntries {
		if entry.Role != "tool_call" {
			return false
		}
		toolCallID := strings.TrimSpace(entry.ToolCallID)
		if toolCallID == "" {
			return false
		}
		if !transcriptContainsToolCallID(m.transcriptEntries, toolCallID) {
			return false
		}
		matched = true
	}
	return matched
}

func shouldDeferProjectedUserMessageFlushAppend(m *uiModel, evt clientui.Event) bool {
	if m == nil || evt.Kind != clientui.EventUserMessageFlushed || len(evt.TranscriptEntries) == 0 {
		return false
	}
	if !m.busy {
		return false
	}
	if strings.TrimSpace(m.view.OngoingStreamingText()) == "" && !m.sawAssistantDelta {
		return false
	}
	for _, entry := range evt.TranscriptEntries {
		if entry.Role != "user" {
			return false
		}
	}
	committed := tui.CommittedOngoingEntries(m.transcriptEntries)
	if len(committed) == 0 {
		return true
	}
	lastCommittedRole := strings.TrimSpace(committed[len(committed)-1].Role)
	return lastCommittedRole == "user"
}

func shouldClearAssistantStreamForCommittedAssistantEvent(evt clientui.Event) bool {
	if evt.Kind != clientui.EventAssistantMessage {
		return false
	}
	for _, entry := range evt.TranscriptEntries {
		if strings.TrimSpace(entry.Role) == "assistant" {
			return true
		}
	}
	return false
}

func skippedAssistantCommitMatchesActiveLiveStream(m *uiModel, evt clientui.Event) bool {
	if m == nil || strings.TrimSpace(m.view.OngoingStreamingText()) == "" {
		return false
	}
	if evt.TranscriptRevision != m.transcriptRevision {
		return false
	}
	committedEntries := tui.CommittedOngoingEntries(m.transcriptEntries)
	if evt.CommittedEntryCount != m.transcriptBaseOffset+len(committedEntries) {
		return false
	}
	assistantText := ""
	for _, entry := range evt.TranscriptEntries {
		if strings.TrimSpace(entry.Role) != "assistant" {
			continue
		}
		assistantText = strings.TrimSpace(entry.Text)
		break
	}
	if assistantText == "" || assistantText != strings.TrimSpace(m.view.OngoingStreamingText()) {
		return false
	}
	for idx := len(committedEntries) - 1; idx >= 0; idx-- {
		entry := committedEntries[idx]
		if strings.TrimSpace(entry.Role) != "assistant" {
			continue
		}
		return strings.TrimSpace(entry.Text) == assistantText
	}
	return false
}

func shouldIgnoreStaleAssistantDelta(m *uiModel, evt clientui.Event, delta string) bool {
	if m == nil || evt.Kind != clientui.EventAssistantDelta {
		return false
	}
	if strings.TrimSpace(delta) == "" {
		return false
	}
	if m.busy || m.compacting || m.reviewerRunning {
		return false
	}
	if strings.TrimSpace(m.view.OngoingStreamingText()) != "" || m.sawAssistantDelta {
		return false
	}
	if stepID := strings.TrimSpace(evt.StepID); stepID != "" && stepID != strings.TrimSpace(m.lastCommittedAssistantStepID) {
		return false
	}
	committedEntries := tui.CommittedOngoingEntries(m.transcriptEntries)
	for idx := len(committedEntries) - 1; idx >= 0; idx-- {
		entry := committedEntries[idx]
		if strings.TrimSpace(entry.Role) != "assistant" {
			continue
		}
		return strings.TrimSpace(entry.Text) == strings.TrimSpace(delta)
	}
	return false
}

func shouldPauseRuntimeEventsForHydration(m *uiModel) bool {
	if m == nil {
		return false
	}
	return strings.TrimSpace(m.view.OngoingStreamingText()) == "" && !m.sawAssistantDelta
}

func transcriptContainsToolCallID(entries []tui.TranscriptEntry, toolCallID string) bool {
	trimmed := strings.TrimSpace(toolCallID)
	if trimmed == "" {
		return false
	}
	for _, entry := range entries {
		if strings.TrimSpace(entry.ToolCallID) == trimmed {
			return true
		}
	}
	return false
}

func eventTranscriptEntriesReconcileWithCommittedTail(kind clientui.EventKind) bool {
	switch kind {
	case clientui.EventUserMessageFlushed,
		clientui.EventAssistantMessage,
		clientui.EventToolCallCompleted,
		clientui.EventReviewerCompleted,
		clientui.EventCacheWarning,
		clientui.EventLocalEntryAdded:
		return true
	default:
		return false
	}
}

func transcriptEntryMatchesChatEntry(existing tui.TranscriptEntry, incoming clientui.ChatEntry) bool {
	return existing.Visibility == incoming.Visibility &&
		existing.Role == incoming.Role &&
		existing.Text == incoming.Text &&
		existing.OngoingText == incoming.OngoingText &&
		existing.Phase == llm.MessagePhase(incoming.Phase) &&
		strings.TrimSpace(existing.ToolCallID) == strings.TrimSpace(incoming.ToolCallID)
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
		events := []clientui.Event{evt}
		if runtimeEventBatchFence(evt) {
			return runtimeEventBatchMsg{events: events}
		}
		for len(events) < 64 {
			select {
			case next, ok := <-ch:
				if !ok {
					return runtimeEventBatchMsg{events: events}
				}
				if runtimeEventBatchFence(next) {
					carry := next
					return runtimeEventBatchMsg{events: events, carry: &carry}
				}
				events = append(events, next)
			default:
				return runtimeEventBatchMsg{events: events}
			}
		}
		return runtimeEventBatchMsg{events: events}
	}
}

func runtimeEventBatchFence(evt clientui.Event) bool {
	if len(evt.TranscriptEntries) > 0 {
		return true
	}
	switch evt.Kind {
	case clientui.EventConversationUpdated,
		clientui.EventAssistantDeltaReset,
		clientui.EventReasoningDeltaReset:
		return true
	default:
		return false
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

func waitRuntimeConnectionStateChange(ch <-chan runtimeConnectionStateChangedMsg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
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

func transcriptToolCallMetaClient(meta *transcript.ToolCallMeta) *clientui.ToolCallMeta {
	if meta == nil {
		return nil
	}
	out := &clientui.ToolCallMeta{
		ToolName:               meta.ToolName,
		Presentation:           clientui.ToolPresentationKind(meta.Presentation),
		RenderBehavior:         clientui.ToolCallRenderBehavior(meta.RenderBehavior),
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
		out.RenderHint = &clientui.ToolRenderHint{
			Kind:       clientui.ToolRenderKind(meta.RenderHint.Kind),
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
