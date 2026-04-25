package app

import (
	"strconv"
	"strings"

	"builder/cli/tui"
	"builder/server/llm"
	"builder/server/runtime"
	"builder/shared/clientui"
	"builder/shared/transcript"
	patchformat "builder/shared/transcript/patchformat"
	"builder/shared/transcriptdiag"

	tea "github.com/charmbracelet/bubbletea"
)

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
	divergence string
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
	if merged, ok := mergeDeferredCommittedTailIntoEvent(m, evt); ok {
		evt = merged
	}
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
	if evt.Kind == clientui.EventConversationUpdated {
		effectiveSyncSessionView = shouldRecoverCommittedTranscriptFromConversationUpdate(m, evt)
	}
	m.logTranscriptEventDiag("transcript.diag.client.apply_event", evt, map[string]string{
		"path":                  "live_event",
		"recovery_cause":        string(evt.RecoveryCause),
		"sync_session_view":     strconv.FormatBool(effectiveSyncSessionView),
		"record_prompt_history": strconv.FormatBool(update.RecordPromptHistory),
	})
	a.applyRuntimeEventUpdate(update)
	cmds := make([]tea.Cmd, 0, 4)
	transcriptMutated := false
	awaitsHydration := false
	if shouldAppendSyntheticOngoingEntry(m, update.SyntheticOngoingEntry) {
		m.forwardToView(tui.AppendTranscriptMsg{
			Visibility:  update.SyntheticOngoingEntry.Visibility,
			Transient:   true,
			Committed:   false,
			Role:        update.SyntheticOngoingEntry.Role,
			Text:        update.SyntheticOngoingEntry.Text,
			OngoingText: update.SyntheticOngoingEntry.OngoingText,
			Phase:       llm.MessagePhase(update.SyntheticOngoingEntry.Phase),
			ToolCallID:  strings.TrimSpace(update.SyntheticOngoingEntry.ToolCallID),
			ToolCall:    transcriptToolCallMeta(update.SyntheticOngoingEntry.ToolCall),
		})
	}
	if evt.Kind == clientui.EventConversationUpdated && effectiveSyncSessionView {
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
		} else if isNoopFinalText(update.AssistantDelta) {
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
		if evt.RecoveryCause != clientui.TranscriptRecoveryCauseNone {
			cmds = append(cmds, m.requestRuntimeTranscriptSyncForContinuityLoss(evt.RecoveryCause))
		} else {
			cmds = append(cmds, a.syncConversationFromEngine())
		}
		awaitsHydration = awaitsHydration || shouldPauseRuntimeEventsForHydration(m)
	} else if shouldRefreshDeferredCommittedTailOnRunEnd(m, evt) {
		cmds = append(cmds, m.requestRuntimeCommittedConversationSync())
	}
	return runtimeEventApplyResult{cmd: batchCmds(cmds...), transcriptMutated: transcriptMutated, awaitsHydration: awaitsHydration}
}

func shouldRefreshDeferredCommittedTailOnRunEnd(m *uiModel, evt clientui.Event) bool {
	if m == nil || !m.hasRuntimeClient() || len(m.deferredCommittedTail) == 0 {
		return false
	}
	if evt.Kind != clientui.EventRunStateChanged || evt.RunState == nil {
		return false
	}
	return !evt.RunState.Busy
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
	return m.requestRuntimeCommittedConversationSync()
}

func (a uiRuntimeAdapter) applyProjectedTranscriptEntries(evt clientui.Event, flushNativeHistory bool) (tea.Cmd, bool, bool) {
	m := a.model
	entries := cloneChatEntries(evt.TranscriptEntries)
	incomingCount := len(entries)
	if shouldSkipProjectedToolCallStart(m, evt) {
		m.logTranscriptEventDiag("transcript.diag.client.append_entries", evt, map[string]string{
			"path":           "live_event",
			"incoming_count": strconv.Itoa(incomingCount),
			"reason":         "duplicate_tool_call_start",
			"applied_count":  "0",
		})
		return nil, false, false
	}
	plan := planProjectedTranscriptEntries(m, evt)
	m.logProjectedTranscriptPlanDiag(evt, plan, incomingCount)
	switch plan.mode {
	case projectedTranscriptEntryPlanSkip:
		if evt.CommittedTranscriptChanged {
			m.transcriptRevision = max(m.transcriptRevision, evt.TranscriptRevision)
			m.transcriptTotalEntries = max(m.transcriptTotalEntries, evt.CommittedEntryCount)
		}
		m.logTranscriptEventDiag("transcript.diag.client.append_entries", evt, map[string]string{
			"path":           "live_event",
			"incoming_count": strconv.Itoa(incomingCount),
			"reason":         "already_hydrated",
			"applied_count":  "0",
		})
		return nil, false, false
	case projectedTranscriptEntryPlanHydrate:
		m.beginCommittedTranscriptContinuityRecovery()
		m.logTranscriptEventDiag("transcript.diag.client.append_entries", evt, map[string]string{
			"path":           "live_event",
			"incoming_count": strconv.Itoa(incomingCount),
			"reason":         "requires_hydration",
			"divergence":     plan.divergence,
			"applied_count":  "0",
		})
		if m.hasRuntimeClient() {
			if evt.RecoveryCause != clientui.TranscriptRecoveryCauseNone {
				return m.requestRuntimeTranscriptSyncForContinuityLoss(evt.RecoveryCause), false, true
			}
			return m.requestRuntimeCommittedGapSync(), false, true
		}
		return nil, false, false
	}
	if plan.mode == projectedTranscriptEntryPlanAppend && shouldDeferProjectedUserMessageFlushAppend(m, evt) {
		deferProjectedCommittedTail(m, evt)
		m.logTranscriptEventDiag("transcript.diag.client.append_entries", evt, map[string]string{
			"path":           "live_event",
			"incoming_count": strconv.Itoa(incomingCount),
			"reason":         "deferred_tail",
			"applied_count":  "0",
		})
		return nil, false, false
	}
	entries = plan.entries
	m.transcriptLiveDirty = true
	startOffset := m.transcriptBaseOffset + plan.rangeStart
	projectedEntriesCommitted := eventTranscriptEntriesAreCommitted(evt)
	projectedEntriesTransient := m.hasRuntimeClient() && evt.Kind != clientui.EventConversationUpdated && !projectedEntriesCommitted
	convertedEntries := make([]tui.TranscriptEntry, 0, len(entries))
	for _, entry := range entries {
		convertedEntries = append(convertedEntries, transcriptEntryFromProjectedChatEntry(entry, projectedEntriesTransient, projectedEntriesCommitted))
	}
	showTransientInCurrentView := m.view.Mode() != tui.ModeDetail || !allTranscriptEntriesTransient(convertedEntries)
	replaceLoadedSyntheticEntries := shouldReplaceLoadedSyntheticEntriesWithCommittedAppend(m, convertedEntries)
	if plan.mode == projectedTranscriptEntryPlanAppend {
		for _, transcriptEntry := range convertedEntries {
			m.transcriptEntries = append(m.transcriptEntries, transcriptEntry)
			if showTransientInCurrentView && !replaceLoadedSyntheticEntries {
				m.forwardToView(appendTranscriptMsgFromEntry(transcriptEntry))
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
	if plan.mode == projectedTranscriptEntryPlanAppend && replaceLoadedSyntheticEntries {
		m.forwardToView(tui.SetConversationMsg{
			BaseOffset:   m.transcriptBaseOffset,
			TotalEntries: m.transcriptTotalEntries,
			Entries:      append([]tui.TranscriptEntry(nil), m.transcriptEntries...),
			Ongoing:      m.view.OngoingStreamingText(),
			OngoingError: m.view.OngoingErrorText(),
		})
	}
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
	if plan.mode == projectedTranscriptEntryPlanAppend && m.view.Mode() == tui.ModeDetail && !allTranscriptEntriesTransient(convertedEntries) && m.detailTranscript.loaded && m.view.TranscriptBaseOffset() == m.detailTranscript.offset {
		m.forwardToView(tui.SetConversationMsg{
			BaseOffset:   m.detailTranscript.offset,
			TotalEntries: m.detailTranscript.totalEntries,
			Entries:      append([]tui.TranscriptEntry(nil), m.detailTranscript.entries...),
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
	return a.applyRuntimeTranscriptPageWithRecovery(clientui.TranscriptPageRequest{}, page, clientui.TranscriptRecoveryCauseNone)
}

func (a uiRuntimeAdapter) applyProjectedSessionView(view clientui.RuntimeSessionView) tea.Cmd {
	transcript := transcriptPageFromSessionView(view)
	return sequenceCmds(a.applyProjectedSessionMetadata(view), a.applyRuntimeTranscriptPageWithRecovery(clientui.TranscriptPageRequest{}, transcript, clientui.TranscriptRecoveryCauseNone))
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
		m.clearDeferredCommittedTail("session_switch")
		m.nativeHistoryReplayPermit = nativeHistoryReplayPermitNone
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
	return a.applyRuntimeTranscriptPageWithRecovery(clientui.TranscriptPageRequest{}, page, clientui.TranscriptRecoveryCauseNone)
}

func (a uiRuntimeAdapter) applyRuntimeTranscriptPage(req clientui.TranscriptPageRequest, page clientui.TranscriptPage) tea.Cmd {
	return a.applyRuntimeTranscriptPageWithRecovery(req, page, clientui.TranscriptRecoveryCauseNone)
}

func (a uiRuntimeAdapter) applyRuntimeTranscriptPageWithRecovery(req clientui.TranscriptPageRequest, page clientui.TranscriptPage, recoveryCause clientui.TranscriptRecoveryCause) tea.Cmd {
	m := a.model
	m.logTranscriptPageDiag("transcript.diag.client.apply_page_start", req, page, map[string]string{"path": "hydrate", "recovery_cause": string(recoveryCause)})
	if len(m.startupCmds) > 0 {
		m.startupCmds = nil
	}
	previousWindowTitle := m.windowTitle()
	if transcriptPageSessionChanged(m.sessionID, page.SessionID) {
		m.detailTranscript.reset()
		m.transcriptRevision = 0
		m.transcriptLiveDirty = false
		m.reasoningLiveDirty = false
		m.clearDeferredCommittedTail("session_switch")
		m.nativeHistoryReplayPermit = nativeHistoryReplayPermitNone
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
	if shouldPreserveLiveAssistantOngoingForPage(m, pageReq, page) {
		page.Ongoing = m.view.OngoingStreamingText()
		page.OngoingError = m.view.OngoingErrorText()
	}
	if authoritativePageDuplicatesCommittedAssistantOngoing(entries, page.Ongoing, m.view.OngoingStreamingText()) {
		page.Ongoing = ""
		page.OngoingError = ""
	}
	if reason := transcriptPageReplacementRejectReason(m, pageReq, page); reason != "" {
		m.logTranscriptPageDiag("transcript.diag.client.apply_page_reject", pageReq, page, map[string]string{
			"path":                    "hydrate",
			"reason":                  reason,
			"recovery_cause":          string(recoveryCause),
			"replacement_branch":      transcriptPageApplyBranch(pageReq, m),
			"preserve_live_reasoning": strconv.FormatBool(shouldPreserveLiveReasoning(m, page)),
		})
		if previousWindowTitle != m.windowTitle() {
			return tea.SetWindowTitle(m.windowTitle())
		}
		return nil
	}
	shouldSyncNativeHistory := pageReq.Window == clientui.TranscriptWindowOngoingTail || pageReq == (clientui.TranscriptPageRequest{})
	preserveLiveReasoning := shouldPreserveLiveReasoning(m, page)
	if shouldSyncNativeHistory {
		permit := nativeHistoryReplayPermitAuthoritativeHydrate
		if recoveryCause != clientui.TranscriptRecoveryCauseNone {
			permit = nativeHistoryReplayPermitContinuityRecovery
		}
		m.armNativeHistoryReplayPermit(permit)
		m.clearDeferredCommittedTail("authoritative_hydrate")
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
		"recovery_cause":            string(recoveryCause),
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
	m.clearDeferredCommittedTail("invalidate_transient")
	hadTransient := false
	committed := make([]tui.TranscriptEntry, 0, len(m.transcriptEntries))
	for _, entry := range m.transcriptEntries {
		if !transcriptEntryCommittedForApp(entry) {
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

func shouldPreserveLiveAssistantOngoingForPage(m *uiModel, req clientui.TranscriptPageRequest, page clientui.TranscriptPage) bool {
	if m == nil {
		return false
	}
	replacesOngoingTail := req.Window == clientui.TranscriptWindowOngoingTail || (req == (clientui.TranscriptPageRequest{}) && m.view.Mode() != tui.ModeDetail)
	if replacesOngoingTail {
		return false
	}
	effectiveRevision, _ := committedTranscriptStateIncludingDeferredTail(m)
	if page.Revision <= 0 || page.Revision != effectiveRevision {
		return false
	}
	trimmedLiveOngoing := strings.TrimSpace(m.view.OngoingStreamingText())
	if trimmedLiveOngoing == "" || strings.TrimSpace(page.Ongoing) != "" {
		return false
	}
	entries := page.Entries
	for idx := len(entries) - 1; idx >= 0; idx-- {
		entry := entries[idx]
		if strings.TrimSpace(entry.Text) == "" && strings.TrimSpace(entry.OngoingText) == "" {
			continue
		}
		if strings.TrimSpace(entry.Role) != "assistant" {
			continue
		}
		return strings.TrimSpace(entry.Text) != trimmedLiveOngoing
	}
	return true
}

func authoritativePageCommitsLiveAssistantOngoing(m *uiModel, page clientui.TranscriptPage) bool {
	if m == nil {
		return false
	}
	trimmedLiveOngoing := strings.TrimSpace(m.view.OngoingStreamingText())
	if trimmedLiveOngoing == "" || strings.TrimSpace(page.Ongoing) != "" {
		return false
	}
	entries := page.Entries
	if len(entries) == 0 {
		return false
	}
	currentStart := m.transcriptBaseOffset
	currentEnd := currentStart + len(m.transcriptEntries)
	for idx := len(entries) - 1; idx >= 0; idx-- {
		entry := entries[idx]
		if strings.TrimSpace(entry.Text) == "" && strings.TrimSpace(entry.OngoingText) == "" {
			continue
		}
		if strings.TrimSpace(entry.Role) != "assistant" {
			continue
		}
		if strings.TrimSpace(entry.Text) != trimmedLiveOngoing {
			return false
		}
		absolute := page.Offset + idx
		if absolute < currentStart || absolute >= currentEnd {
			return true
		}
		if !transcriptEntryMatchesChatEntry(m.transcriptEntries[absolute-currentStart], entry) {
			return true
		}
		return false
	}
	return false
}

func committedTranscriptAlreadyMatchesAssistantOngoing(entries []tui.TranscriptEntry, liveOngoing string) bool {
	trimmedLiveOngoing := strings.TrimSpace(liveOngoing)
	if trimmedLiveOngoing == "" {
		return false
	}
	committed := committedTranscriptEntriesForApp(entries)
	for idx := len(committed) - 1; idx >= 0; idx-- {
		entry := committed[idx]
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
	if m == nil {
		return ""
	}
	effectiveRevision, effectiveCommittedCount := committedTranscriptStateIncludingDeferredTail(m)
	if page.Revision <= 0 {
		if effectiveRevision > 0 {
			return "stale_revision"
		}
		return ""
	}
	if page.Revision < effectiveRevision {
		return "stale_revision"
	}
	replacesOngoingTail := req.Window == clientui.TranscriptWindowOngoingTail || (req == (clientui.TranscriptPageRequest{}) && m.view.Mode() != tui.ModeDetail)
	if !replacesOngoingTail {
		return ""
	}
	if page.Revision == effectiveRevision && page.TotalEntries < effectiveCommittedCount {
		return "stale_total_entries"
	}
	if page.Revision == effectiveRevision && strings.TrimSpace(m.view.OngoingStreamingText()) != "" && strings.TrimSpace(page.Ongoing) == "" {
		if authoritativePageCommitsLiveAssistantOngoing(m, page) {
			return ""
		}
		if authoritativePageDuplicatesCommittedAssistantOngoing(transcriptEntriesFromPage(page), page.Ongoing, m.view.OngoingStreamingText()) {
			return ""
		}
		if committedTranscriptAlreadyMatchesAssistantOngoing(m.transcriptEntries, m.view.OngoingStreamingText()) {
			return ""
		}
		return "same_revision_would_clear_ongoing"
	}
	if m.transcriptLiveDirty && page.Revision == effectiveRevision && shouldAcceptEqualRevisionTailReplacement(m, page) {
		return ""
	}
	if m.transcriptLiveDirty && page.Revision <= effectiveRevision {
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
		entries = append(entries, transcriptEntryFromProjectedChatEntry(entry, false, false))
	}
	return entries
}

func transcriptEntryCommittedForApp(entry tui.TranscriptEntry) bool {
	return !entry.Transient || entry.Committed
}

func shouldAppendSyntheticOngoingEntry(m *uiModel, entry *clientui.ChatEntry) bool {
	if m == nil || entry == nil || !m.hasRuntimeClient() || m.view.Mode() != tui.ModeOngoing {
		return false
	}
	role := strings.TrimSpace(entry.Role)
	text := strings.TrimSpace(entry.Text)
	if role == "" || text == "" {
		return false
	}
	for _, loaded := range m.view.LoadedTranscriptEntries() {
		if strings.TrimSpace(loaded.Role) == role && strings.TrimSpace(loaded.Text) == text {
			return false
		}
	}
	return true
}

func shouldReplaceLoadedSyntheticEntriesWithCommittedAppend(m *uiModel, entries []tui.TranscriptEntry) bool {
	if m == nil || m.view.Mode() != tui.ModeOngoing || len(entries) == 0 {
		return false
	}
	loaded := m.view.LoadedTranscriptEntries()
	if len(loaded) == 0 {
		return false
	}
	for _, loadedEntry := range loaded {
		if !loadedEntry.Transient || loadedEntry.Committed {
			continue
		}
		for _, committedEntry := range entries {
			if committedEntry.Transient || !committedEntry.Committed {
				continue
			}
			if strings.TrimSpace(loadedEntry.Role) == strings.TrimSpace(committedEntry.Role) && strings.TrimSpace(loadedEntry.Text) == strings.TrimSpace(committedEntry.Text) {
				return true
			}
		}
	}
	return false
}

func committedTranscriptEntriesForApp(entries []tui.TranscriptEntry) []tui.TranscriptEntry {
	if len(entries) == 0 {
		return nil
	}
	normalized := make([]tui.TranscriptEntry, 0, len(entries))
	for _, entry := range entries {
		if !transcriptEntryCommittedForApp(entry) {
			continue
		}
		copyEntry := entry
		copyEntry.Transient = false
		normalized = append(normalized, copyEntry)
	}
	return tui.CommittedOngoingEntries(normalized)
}

func committedTranscriptProjectionForApp(view tui.Model, entries []tui.TranscriptEntry) tui.TranscriptProjection {
	return view.CommittedOngoingProjectionForEntries(committedTranscriptEntriesForApp(entries))
}

func transcriptEntryFromProjectedChatEntry(entry clientui.ChatEntry, transient bool, committed bool) tui.TranscriptEntry {
	return tui.TranscriptEntry{
		Visibility:        entry.Visibility,
		Transient:         transient,
		Committed:         committed,
		Role:              entry.Role,
		Text:              entry.Text,
		OngoingText:       entry.OngoingText,
		Phase:             llm.MessagePhase(entry.Phase),
		MessageType:       llm.MessageType(entry.MessageType),
		SourcePath:        strings.TrimSpace(entry.SourcePath),
		CompactLabel:      strings.TrimSpace(entry.CompactLabel),
		ToolResultSummary: strings.TrimSpace(entry.ToolResultSummary),
		ToolCallID:        entry.ToolCallID,
		ToolCall:          transcriptToolCallMeta(entry.ToolCall),
	}
}

func appendTranscriptMsgFromEntry(entry tui.TranscriptEntry) tui.AppendTranscriptMsg {
	return tui.AppendTranscriptMsg{
		Visibility:        entry.Visibility,
		Transient:         entry.Transient,
		Committed:         entry.Committed,
		Role:              entry.Role,
		Text:              entry.Text,
		OngoingText:       entry.OngoingText,
		Phase:             entry.Phase,
		MessageType:       entry.MessageType,
		SourcePath:        strings.TrimSpace(entry.SourcePath),
		CompactLabel:      strings.TrimSpace(entry.CompactLabel),
		ToolResultSummary: strings.TrimSpace(entry.ToolResultSummary),
		ToolCallID:        strings.TrimSpace(entry.ToolCallID),
		ToolCall:          entry.ToolCall,
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
	if len(entries) == 0 || !eventTranscriptEntriesReconcileWithCommittedTail(evt) {
		return plan
	}
	eventStart, eventEnd, ok := projectedTranscriptEventRange(evt, len(entries))
	if !ok {
		plan.divergence = "missing_event_range"
		return plan
	}
	if eventStart < 0 {
		return projectedTranscriptEntryPlan{mode: projectedTranscriptEntryPlanHydrate, divergence: "negative_event_start"}
	}
	currentStart := m.transcriptBaseOffset
	currentEnd := currentStart + len(m.transcriptEntries)
	if eventEnd <= currentStart {
		return projectedTranscriptEntryPlan{mode: projectedTranscriptEntryPlanSkip}
	}
	if eventStart < currentStart {
		trimmedPrefixCount := currentStart - eventStart
		if trimmedPrefixCount >= len(entries) {
			return projectedTranscriptEntryPlan{mode: projectedTranscriptEntryPlanSkip}
		}
		entries = cloneChatEntries(entries[trimmedPrefixCount:])
		eventStart = currentStart
		eventEnd = eventStart + len(entries)
	}
	if evt.TranscriptRevision < m.transcriptRevision {
		if eventEnd > currentEnd {
			return projectedTranscriptEntryPlan{mode: projectedTranscriptEntryPlanHydrate, divergence: "stale_revision_extends_tail"}
		}
		if projectedTranscriptEntriesMatchCurrentRange(m, eventStart, entries, evt.CommittedTranscriptChanged) {
			return projectedTranscriptEntryPlan{mode: projectedTranscriptEntryPlanSkip}
		}
		return projectedTranscriptEntryPlan{mode: projectedTranscriptEntryPlanSkip}
	}
	if eventStart > currentEnd {
		return projectedTranscriptEntryPlan{mode: projectedTranscriptEntryPlanHydrate, divergence: "gap_after_tail"}
	}
	overlapStart := max(eventStart, currentStart)
	overlapEnd := min(eventEnd, currentEnd)
	if projectedTranscriptEntriesMatchCurrentOverlap(m, eventStart, overlapStart, overlapEnd, entries, evt.CommittedTranscriptChanged) {
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

func deferProjectedCommittedTail(m *uiModel, evt clientui.Event) {
	if m == nil || len(evt.TranscriptEntries) == 0 {
		return
	}
	pendingBatch := deferredPendingInjectedBatchFromEvent(evt)
	start, end, ok := projectedTranscriptEventRange(evt, len(evt.TranscriptEntries))
	if !ok {
		start = m.transcriptBaseOffset + len(committedTranscriptEntriesForApp(m.transcriptEntries))
		end = start + len(evt.TranscriptEntries)
	}
	m.deferredCommittedTail = append(m.deferredCommittedTail, deferredProjectedTranscriptTail{
		rangeStart: start,
		rangeEnd:   end,
		revision:   evt.TranscriptRevision,
		entries:    cloneChatEntries(evt.TranscriptEntries),
		pending:    pendingBatch,
	})
	if evt.TranscriptRevision > m.transcriptRevision {
		m.transcriptRevision = evt.TranscriptRevision
	}
	if end > m.transcriptTotalEntries {
		m.transcriptTotalEntries = end
	}
	m.logTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.defer_tail", map[string]string{
		"session_id":     strings.TrimSpace(m.sessionID),
		"mode":           m.transcriptModeLabel(),
		"kind":           string(evt.Kind),
		"range_start":    strconv.Itoa(start),
		"range_end":      strconv.Itoa(end),
		"revision":       strconv.FormatInt(evt.TranscriptRevision, 10),
		"entries_digest": transcriptdiag.EntriesDigest(evt.TranscriptEntries),
		"pending_count":  strconv.Itoa(len(pendingBatch)),
	}))
}

func deferredPendingInjectedBatchFromEvent(evt clientui.Event) []string {
	if evt.Kind != clientui.EventUserMessageFlushed {
		return nil
	}
	batch := append([]string(nil), evt.UserMessageBatch...)
	if len(batch) == 0 {
		trimmed := strings.TrimSpace(evt.UserMessage)
		if trimmed == "" {
			return nil
		}
		batch = []string{trimmed}
	}
	for idx := range batch {
		batch[idx] = strings.TrimSpace(batch[idx])
	}
	return batch
}

func (m *uiModel) clearDeferredCommittedTail(reason string) {
	if m == nil {
		return
	}
	if len(m.deferredCommittedTail) > 0 {
		pendingCount := 0
		for _, deferred := range m.deferredCommittedTail {
			pendingCount += len(deferred.pending)
		}
		m.logTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.clear_deferred_tail", map[string]string{
			"session_id":    strings.TrimSpace(m.sessionID),
			"mode":          m.transcriptModeLabel(),
			"reason":        reason,
			"tail_count":    strconv.Itoa(len(m.deferredCommittedTail)),
			"pending_count": strconv.Itoa(pendingCount),
		}))
	}
	m.deferredCommittedTail = nil
}

func (m *uiModel) beginCommittedTranscriptContinuityRecovery() {
	if m == nil {
		return
	}
	m.logTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.begin_continuity_recovery", map[string]string{
		"session_id":    strings.TrimSpace(m.sessionID),
		"mode":          m.transcriptModeLabel(),
		"current_base":  strconv.Itoa(m.transcriptBaseOffset),
		"current_count": strconv.Itoa(len(m.transcriptEntries)),
		"current_total": strconv.Itoa(m.transcriptTotalEntries),
	}))
	m.invalidateTransientTranscriptState()
}

func mergeDeferredCommittedTailIntoEvent(m *uiModel, evt clientui.Event) (clientui.Event, bool) {
	if m == nil || len(m.deferredCommittedTail) == 0 || len(evt.TranscriptEntries) == 0 || !evt.CommittedTranscriptChanged {
		return evt, false
	}
	eventStart, _, ok := projectedTranscriptEventRange(evt, len(evt.TranscriptEntries))
	if !ok {
		return evt, false
	}
	currentEnd := m.transcriptBaseOffset + len(committedTranscriptEntriesForApp(m.transcriptEntries))
	mergedEntries := make([]clientui.ChatEntry, 0, len(evt.TranscriptEntries)+len(m.deferredCommittedTail))
	mergedStart := currentEnd
	used := 0
	chainEnd := currentEnd
	for _, deferred := range m.deferredCommittedTail {
		if deferred.rangeStart != chainEnd {
			break
		}
		mergedEntries = append(mergedEntries, cloneChatEntries(deferred.entries)...)
		chainEnd = deferred.rangeEnd
		used++
	}
	if used == 0 || eventStart != chainEnd {
		return evt, false
	}
	mergedEntries = append(mergedEntries, cloneChatEntries(evt.TranscriptEntries)...)
	evt.TranscriptEntries = mergedEntries
	evt.CommittedEntryStart = mergedStart
	evt.CommittedEntryStartSet = true
	m.logTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.merge_deferred_tail", map[string]string{
		"session_id":     strings.TrimSpace(m.sessionID),
		"mode":           m.transcriptModeLabel(),
		"kind":           string(evt.Kind),
		"merged_start":   strconv.Itoa(mergedStart),
		"merged_count":   strconv.Itoa(len(mergedEntries)),
		"consumed_tails": strconv.Itoa(used),
	}))
	m.deferredCommittedTail = append([]deferredProjectedTranscriptTail(nil), m.deferredCommittedTail[used:]...)
	return evt, true
}

func projectedTranscriptEntriesMatchCurrentRange(m *uiModel, eventStart int, entries []clientui.ChatEntry, requireCommitted bool) bool {
	if m == nil {
		return false
	}
	currentStart := m.transcriptBaseOffset
	currentEnd := currentStart + len(m.transcriptEntries)
	eventEnd := eventStart + len(entries)
	if eventStart < currentStart || eventEnd > currentEnd {
		return false
	}
	return projectedTranscriptEntriesMatchCurrentOverlap(m, eventStart, eventStart, eventEnd, entries, requireCommitted)
}

func projectedTranscriptEntriesMatchCurrentOverlap(m *uiModel, eventStart int, overlapStart int, overlapEnd int, entries []clientui.ChatEntry, requireCommitted bool) bool {
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
		if requireCommitted && !transcriptEntryCommittedForApp(m.transcriptEntries[currentIndex]) {
			return false
		}
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
		if !transcriptContainsCommittedToolCallID(m.transcriptEntries, toolCallID) {
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
	if !m.hasRuntimeClient() {
		return true
	}
	committed := committedTranscriptEntriesForApp(m.transcriptEntries)
	return len(committed) == 0
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
	committedEntries := committedTranscriptEntriesForApp(m.transcriptEntries)
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
	committedEntries := committedTranscriptEntriesForApp(m.transcriptEntries)
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

func transcriptContainsCommittedToolCallID(entries []tui.TranscriptEntry, toolCallID string) bool {
	trimmed := strings.TrimSpace(toolCallID)
	if trimmed == "" {
		return false
	}
	for _, entry := range entries {
		if strings.TrimSpace(entry.ToolCallID) != trimmed {
			continue
		}
		if transcriptEntryCommittedForApp(entry) {
			return true
		}
	}
	return false
}

func shouldRecoverCommittedTranscriptFromConversationUpdate(m *uiModel, evt clientui.Event) bool {
	if evt.Kind != clientui.EventConversationUpdated {
		return false
	}
	if evt.RecoveryCause != clientui.TranscriptRecoveryCauseNone {
		return true
	}
	if !evt.CommittedTranscriptChanged {
		return false
	}
	if len(evt.TranscriptEntries) > 0 {
		return false
	}
	if evt.TranscriptRevision <= 0 && evt.CommittedEntryCount <= 0 {
		return true
	}
	if m == nil {
		return true
	}
	effectiveRevision, effectiveCommittedCount := committedTranscriptStateIncludingDeferredTail(m)
	return evt.TranscriptRevision != effectiveRevision || evt.CommittedEntryCount != effectiveCommittedCount
}

func committedTranscriptStateIncludingDeferredTail(m *uiModel) (int64, int) {
	if m == nil {
		return 0, 0
	}
	revision := m.transcriptRevision
	count := m.transcriptBaseOffset + len(committedTranscriptEntriesForApp(m.transcriptEntries))
	chainEnd := count
	for _, deferred := range m.deferredCommittedTail {
		if deferred.rangeStart != chainEnd {
			break
		}
		chainEnd = deferred.rangeEnd
		if deferred.revision > revision {
			revision = deferred.revision
		}
	}
	return revision, max(m.transcriptTotalEntries, chainEnd)
}

func eventTranscriptEntriesReconcileWithCommittedTail(evt clientui.Event) bool {
	if !evt.CommittedTranscriptChanged || len(evt.TranscriptEntries) == 0 {
		return false
	}
	if evt.RecoveryCause != clientui.TranscriptRecoveryCauseNone {
		return false
	}
	_, _, ok := projectedTranscriptEventRange(evt, len(evt.TranscriptEntries))
	return ok
}

func eventTranscriptEntriesAreCommitted(evt clientui.Event) bool {
	return evt.CommittedTranscriptChanged
}

func transcriptEntryMatchesChatEntry(existing tui.TranscriptEntry, incoming clientui.ChatEntry) bool {
	return existing.Visibility == incoming.Visibility &&
		existing.Role == incoming.Role &&
		existing.Text == incoming.Text &&
		existing.OngoingText == incoming.OngoingText &&
		existing.Phase == llm.MessagePhase(incoming.Phase) &&
		existing.MessageType == llm.MessageType(incoming.MessageType) &&
		strings.TrimSpace(existing.SourcePath) == strings.TrimSpace(incoming.SourcePath) &&
		strings.TrimSpace(existing.CompactLabel) == strings.TrimSpace(incoming.CompactLabel) &&
		strings.TrimSpace(existing.ToolResultSummary) == strings.TrimSpace(incoming.ToolResultSummary) &&
		strings.TrimSpace(existing.ToolCallID) == strings.TrimSpace(incoming.ToolCallID)
}

func projectedTranscriptEventRange(evt clientui.Event, entryCount int) (int, int, bool) {
	if entryCount <= 0 {
		return 0, 0, false
	}
	if evt.CommittedEntryStartSet {
		if evt.CommittedEntryStart < 0 {
			return 0, 0, false
		}
		return evt.CommittedEntryStart, evt.CommittedEntryStart + entryCount, true
	}
	if evt.CommittedEntryCount <= 0 {
		return 0, 0, false
	}
	start := evt.CommittedEntryCount - entryCount
	if start < 0 {
		return 0, 0, false
	}
	return start, evt.CommittedEntryCount, true
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
			if runtimeEventCanDeferCommittedConversationFence(evt) {
				select {
				case next, ok := <-ch:
					if !ok {
						return runtimeEventBatchMsg{events: events}
					}
					if runtimeEventCoversDeferredCommittedConversationUpdate(evt, next) {
						return runtimeEventBatchMsg{events: []clientui.Event{next}}
					}
					if runtimeEventShouldBatchAfterCommittedConversationFence(evt, next) {
						return runtimeEventBatchMsg{events: []clientui.Event{evt, next}}
					}
					if runtimeEventBatchFence(next) {
						carry := next
						return runtimeEventBatchMsg{events: events, carry: &carry}
					}
					events = append(events, next)
				default:
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

func runtimeEventCanDeferCommittedConversationFence(evt clientui.Event) bool {
	return evt.Kind == clientui.EventConversationUpdated &&
		evt.CommittedTranscriptChanged &&
		evt.RecoveryCause == clientui.TranscriptRecoveryCauseNone &&
		len(evt.TranscriptEntries) == 0
}

func runtimeEventCoversDeferredCommittedConversationUpdate(update clientui.Event, next clientui.Event) bool {
	if !runtimeEventCanDeferCommittedConversationFence(update) {
		return false
	}
	if !next.CommittedTranscriptChanged || len(next.TranscriptEntries) == 0 {
		return false
	}
	if strings.TrimSpace(next.StepID) == "" || strings.TrimSpace(next.StepID) != strings.TrimSpace(update.StepID) {
		return false
	}
	if update.TranscriptRevision > 0 && next.TranscriptRevision > 0 && next.TranscriptRevision < update.TranscriptRevision {
		return false
	}
	if next.CommittedEntryCount != update.CommittedEntryCount {
		return false
	}
	return true
}

func runtimeEventShouldBatchAfterCommittedConversationFence(update clientui.Event, next clientui.Event) bool {
	if !runtimeEventCanDeferCommittedConversationFence(update) {
		return false
	}
	if !next.CommittedTranscriptChanged || len(next.TranscriptEntries) == 0 {
		return false
	}
	if strings.TrimSpace(next.StepID) == "" || strings.TrimSpace(next.StepID) != strings.TrimSpace(update.StepID) {
		return false
	}
	if update.TranscriptRevision > 0 && next.TranscriptRevision > 0 && next.TranscriptRevision < update.TranscriptRevision {
		return false
	}
	if runtimeEventCoversDeferredCommittedConversationUpdate(update, next) {
		return false
	}
	return true
}

func runtimeEventBatchFence(evt clientui.Event) bool {
	if len(evt.TranscriptEntries) > 0 {
		return true
	}
	switch evt.Kind {
	case clientui.EventAssistantDelta,
		clientui.EventReasoningDelta,
		clientui.EventConversationUpdated,
		clientui.EventOngoingErrorUpdated,
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

func waitRuntimeLeaseRecoveryWarning(ch <-chan runtimeLeaseRecoveryWarningMsg) tea.Cmd {
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
			Kind:         transcript.ToolRenderKind(meta.RenderHint.Kind),
			Path:         meta.RenderHint.Path,
			ResultOnly:   meta.RenderHint.ResultOnly,
			ShellDialect: transcript.ToolShellDialect(meta.RenderHint.ShellDialect),
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
			Kind:         clientui.ToolRenderKind(meta.RenderHint.Kind),
			Path:         meta.RenderHint.Path,
			ResultOnly:   meta.RenderHint.ResultOnly,
			ShellDialect: clientui.ToolShellDialect(meta.RenderHint.ShellDialect),
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
