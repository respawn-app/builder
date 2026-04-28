package app

import (
	"strconv"
	"strings"

	"builder/cli/tui"
	"builder/server/runtime"
	"builder/shared/clientui"
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
	if merge := reduceDeferredCommittedTailMerge(newDeferredCommittedTailState(deferredCommittedTailSnapshotFromModel(m)), evt); merge.merged {
		evt = merge.event
		m.deferredCommittedTail = merge.remaining
		m.logTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.merge_deferred_tail", map[string]string{
			"session_id":     strings.TrimSpace(m.sessionID),
			"mode":           m.transcriptModeLabel(),
			"kind":           string(evt.Kind),
			"merged_start":   strconv.Itoa(merge.mergedStart),
			"merged_count":   strconv.Itoa(merge.mergedCount),
			"consumed_tails": strconv.Itoa(merge.consumedTails),
		}))
	}
	if m.turnQueueHook != nil {
		m.turnQueueHook.OnProjectedRuntimeEvent(evt)
	}
	reduction := clientui.ReduceRuntimeEvent(
		a.runtimeRunState(),
		a.runtimeConversationState(),
		a.pendingInputState(),
		a.runtimeReasoningState(),
		m.activity == uiActivityRunning,
		evt,
	)
	transcriptSync := a.effectiveRuntimeTranscriptSync(evt, reduction.Transcript.Sync)
	m.logTranscriptEventDiag("transcript.diag.client.apply_event", evt, map[string]string{
		"path":                  "live_event",
		"recovery_cause":        string(evt.RecoveryCause),
		"sync_session_view":     strconv.FormatBool(transcriptSync.IsSet()),
		"sync_reason":           runtimeTranscriptSyncReasonLabel(transcriptSync),
		"record_prompt_history": strconv.FormatBool(reduction.PendingInput.PromptHistoryCommand != nil),
	})
	m.markActiveSubmitFlushed(evt)
	a.applyRuntimeEventReduction(reduction)
	cmds := make([]tea.Cmd, 0, 4)
	transcriptMutated := false
	awaitsHydration := false
	if shouldAppendSyntheticOngoingEntry(m, reduction.Transcript.SyntheticOngoingEntry) {
		entry := transcriptEntryFromProjectedChatEntry(*reduction.Transcript.SyntheticOngoingEntry, true, false)
		m.forwardToView(appendTranscriptMsgFromEntry(entry))
	}
	if evt.Kind == clientui.EventConversationUpdated && transcriptSync.IsSet() {
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
	for _, streamCommand := range reduction.Transcript.AssistantStream {
		switch streamCommand.Kind {
		case clientui.RuntimeAssistantStreamAppend:
			delta := streamCommand.Delta
			if shouldIgnoreStaleAssistantDelta(m, evt, delta) {
				continue
			}
			if isNoopFinalText(delta) {
				continue
			}
			m.sawAssistantDelta = true
			m.forwardToView(tui.StreamAssistantMsg{Delta: delta})
		case clientui.RuntimeAssistantStreamClear:
			if stepID := strings.TrimSpace(streamCommand.StepID); stepID != "" {
				m.lastCommittedAssistantStepID = stepID
			}
			m.sawAssistantDelta = false
			m.forwardToView(tui.ClearOngoingAssistantMsg{})
		}
	}
	for _, streamCommand := range reduction.Reasoning.Stream {
		switch streamCommand.Kind {
		case clientui.RuntimeReasoningStreamUpsert:
			if streamCommand.Delta == nil {
				continue
			}
			m.reasoningLiveDirty = true
			m.forwardToView(tui.UpsertStreamingReasoningMsg{Key: streamCommand.Delta.Key, Role: streamCommand.Delta.Role, Text: streamCommand.Delta.Text})
		case clientui.RuntimeReasoningStreamClear:
			m.reasoningLiveDirty = false
			m.forwardToView(tui.ClearStreamingReasoningMsg{})
		}
	}
	if reduction.Notices.BackgroundNotice != nil {
		kind := uiStatusNoticeSuccess
		if reduction.Notices.BackgroundNotice.Kind == clientui.BackgroundNoticeError {
			kind = uiStatusNoticeError
		}
		cmds = append(cmds, m.setTransientStatusWithKind(reduction.Notices.BackgroundNotice.Message, kind))
	}
	if reduction.PendingInput.PromptHistoryCommand != nil && strings.TrimSpace(reduction.PendingInput.PromptHistoryCommand.Text) != "" {
		cmds = append(cmds, m.recordPromptHistory(reduction.PendingInput.PromptHistoryCommand.Text))
	}
	if transcriptSync.IsSet() {
		cmds = append(cmds, a.syncConversationFromRuntimeTranscriptCommand(transcriptSync))
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

func (a uiRuntimeAdapter) runtimeRunState() clientui.RuntimeRunState {
	m := a.model
	return clientui.RuntimeRunState{
		Busy:             m.busy,
		Compacting:       m.compacting,
		ReviewerRunning:  m.reviewerRunning,
		ReviewerBlocking: m.reviewerBlocking,
	}
}

func (a uiRuntimeAdapter) runtimeConversationState() clientui.RuntimeConversationState {
	return clientui.RuntimeConversationState{Freshness: a.model.conversationFreshness}
}

func (a uiRuntimeAdapter) runtimeReasoningState() clientui.RuntimeReasoningState {
	return clientui.RuntimeReasoningState{StatusHeader: a.model.reasoningStatusHeader}
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

func (a uiRuntimeAdapter) applyRuntimeEventReduction(reduction clientui.RuntimeEventReduction) {
	m := a.model
	m.busy = reduction.RunState.State.Busy
	m.compacting = reduction.RunState.State.Compacting
	m.reviewerRunning = reduction.RunState.State.ReviewerRunning
	m.reviewerBlocking = reduction.RunState.State.ReviewerBlocking
	m.conversationFreshness = reduction.Conversation.State.Freshness
	m.reasoningStatusHeader = reduction.Reasoning.State.StatusHeader
	m.pendingInjected = reduction.PendingInput.State.PendingInjected
	m.lockedInjectText = reduction.PendingInput.State.LockedInjectText
	m.inputSubmitLocked = reduction.PendingInput.State.InputSubmitLocked
	switch reduction.PendingInput.DraftCommand {
	case clientui.RuntimePendingInputClearDraft:
		m.clearInput()
	}
	switch reduction.PendingInput.PreSubmitCommand {
	case clientui.RuntimePendingInputClearPreSubmit:
		m.pendingPreSubmitText = ""
	}
	switch reduction.RunState.Activity {
	case clientui.RuntimeActivityRunning:
		m.activity = uiActivityRunning
	case clientui.RuntimeActivityIdle:
		m.activity = uiActivityIdle
	}
	switch reduction.BackgroundProcesses.Command {
	case clientui.RuntimeBackgroundProcessRefresh:
		m.refreshProcessEntriesIfOpen()
	}
}

func (a uiRuntimeAdapter) effectiveRuntimeTranscriptSync(evt clientui.Event, proposed clientui.RuntimeTranscriptSyncCommand) clientui.RuntimeTranscriptSyncCommand {
	if evt.Kind != clientui.EventConversationUpdated {
		return proposed
	}
	if !shouldRecoverCommittedTranscriptFromConversationUpdate(a.model, evt) {
		return clientui.RuntimeTranscriptSyncCommand{}
	}
	if proposed.IsSet() {
		return proposed
	}
	return clientui.RuntimeTranscriptSyncCommand{Reason: clientui.RuntimeTranscriptSyncCommittedAdvance}
}

func runtimeTranscriptSyncReasonLabel(sync clientui.RuntimeTranscriptSyncCommand) string {
	if !sync.IsSet() {
		return ""
	}
	return string(sync.Reason)
}

func (a uiRuntimeAdapter) syncConversationFromRuntimeTranscriptCommand(sync clientui.RuntimeTranscriptSyncCommand) tea.Cmd {
	switch sync.Reason {
	case clientui.RuntimeTranscriptSyncRecovery, clientui.RuntimeTranscriptSyncStreamGap:
		return a.model.requestRuntimeTranscriptSyncForContinuityLoss(sync.RecoveryCause)
	case clientui.RuntimeTranscriptSyncCommittedAdvance, clientui.RuntimeTranscriptSyncOngoingErrorUpdated:
		return a.syncConversationFromEngine()
	default:
		return nil
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
	incomingCount := len(evt.TranscriptEntries)
	reduction := reduceProjectedTranscriptEvent(newProjectedTranscriptEventState(projectedTranscriptEventSnapshotFromModel(m)), evt)
	if reduction.decision == projectedTranscriptDecisionSkip && reduction.duplicateToolStarts {
		m.logTranscriptEventDiag("transcript.diag.client.append_entries", evt, map[string]string{
			"path":           "live_event",
			"incoming_count": strconv.Itoa(incomingCount),
			"reason":         reduction.skipReason,
			"applied_count":  "0",
		})
		return nil, false, false
	}
	plan := reduction.plan
	m.logProjectedTranscriptPlanDiag(evt, plan, incomingCount)
	switch reduction.decision {
	case projectedTranscriptDecisionSkip:
		if evt.CommittedTranscriptChanged {
			m.transcriptRevision = max(m.transcriptRevision, evt.TranscriptRevision)
			m.transcriptTotalEntries = max(m.transcriptTotalEntries, evt.CommittedEntryCount)
		}
		m.logTranscriptEventDiag("transcript.diag.client.append_entries", evt, map[string]string{
			"path":           "live_event",
			"incoming_count": strconv.Itoa(incomingCount),
			"reason":         reduction.skipReason,
			"applied_count":  "0",
		})
		return nil, false, false
	case projectedTranscriptDecisionHydrate:
		m.beginCommittedTranscriptContinuityRecovery()
		m.logTranscriptEventDiag("transcript.diag.client.append_entries", evt, map[string]string{
			"path":           "live_event",
			"incoming_count": strconv.Itoa(incomingCount),
			"reason":         "requires_hydration",
			"divergence":     plan.divergence,
			"applied_count":  "0",
		})
		if m.hasRuntimeClient() {
			if reduction.hydrationCause != clientui.TranscriptRecoveryCauseNone {
				return m.requestRuntimeTranscriptSyncForContinuityLoss(reduction.hydrationCause), false, true
			}
			return m.requestRuntimeCommittedGapSync(), false, true
		}
		return nil, false, false
	case projectedTranscriptDecisionDefer:
		m.deferProjectedCommittedTail(evt)
		m.logTranscriptEventDiag("transcript.diag.client.append_entries", evt, map[string]string{
			"path":           "live_event",
			"incoming_count": strconv.Itoa(incomingCount),
			"reason":         reduction.skipReason,
			"applied_count":  "0",
		})
		return nil, false, false
	}
	entries := plan.entries
	m.transcriptLiveDirty = true
	startOffset := m.transcriptBaseOffset + plan.rangeStart
	convertedEntries := make([]tui.TranscriptEntry, 0, len(entries))
	for _, entry := range entries {
		convertedEntries = append(convertedEntries, transcriptEntryFromProjectedChatEntry(entry, reduction.projectedTransient, reduction.projectedCommitted))
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
	reduction := reduceRuntimeTranscriptPage(newRuntimeTranscriptPageState(runtimeTranscriptPageSnapshotFromModel(m)), req, page, recoveryCause)
	pageReq := reduction.request
	page = reduction.page
	entries := reduction.entries
	if reduction.decision == runtimeTranscriptPageDecisionReject {
		m.logTranscriptPageDiag("transcript.diag.client.apply_page_reject", pageReq, page, map[string]string{
			"path":                    "hydrate",
			"reason":                  reduction.rejectReason,
			"recovery_cause":          string(recoveryCause),
			"replacement_branch":      reduction.branch,
			"preserve_live_reasoning": strconv.FormatBool(reduction.preserveLiveReasoning),
		})
		if previousWindowTitle != m.windowTitle() {
			return tea.SetWindowTitle(m.windowTitle())
		}
		return nil
	}
	if reduction.shouldSyncNativeHistory {
		m.armNativeHistoryReplayPermit(reduction.nativeReplayPermit)
		m.clearDeferredCommittedTail("authoritative_hydrate")
		a.applyAuthoritativeOngoingTailPage(page, entries, reduction.preserveLiveReasoning)
	}
	if pageReq.Window == clientui.TranscriptWindowOngoingTail || (pageReq == (clientui.TranscriptPageRequest{}) && m.view.Mode() != tui.ModeDetail) {
		m.detailTranscript.syncTail(page)
		if m.view.Mode() != tui.ModeDetail {
			if !reduction.preserveLiveReasoning {
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
		if !reduction.preserveLiveReasoning {
			m.reasoningLiveDirty = false
		}
		detailPage := m.detailTranscript.page()
		detailPage.SessionID = page.SessionID
		detailPage.SessionName = page.SessionName
		detailPage.ConversationFreshness = page.ConversationFreshness
		detailPage.Revision = page.Revision
		if m.view.Mode() == tui.ModeDetail {
			if !reduction.preserveLiveReasoning {
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
	if reduction.shouldSyncNativeHistory {
		cmds = append(cmds, m.syncNativeHistoryFromTranscript())
	}
	m.logTranscriptPageDiag("transcript.diag.client.apply_page_commit", pageReq, page, map[string]string{
		"path":                      "hydrate",
		"recovery_cause":            string(recoveryCause),
		"branch":                    reduction.branch,
		"preserve_live_reasoning":   strconv.FormatBool(reduction.preserveLiveReasoning),
		"transcript_revision_after": strconv.FormatInt(m.transcriptRevision, 10),
		"transcript_total_after":    strconv.Itoa(m.transcriptTotalEntries),
		"native_history_sync":       strconv.FormatBool(reduction.shouldSyncNativeHistory),
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

func shouldAppendSyntheticOngoingEntry(m *uiModel, entry *clientui.ChatEntry) bool {
	if m == nil || entry == nil || !m.hasRuntimeClient() || m.view.Mode() != tui.ModeOngoing {
		return false
	}
	role := tui.TranscriptRoleFromWire(entry.Role)
	text := strings.TrimSpace(entry.Text)
	if role == tui.TranscriptRoleUnknown || text == "" {
		return false
	}
	for _, loaded := range m.view.LoadedTranscriptEntries() {
		if loaded.Role == role && strings.TrimSpace(loaded.Text) == text {
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
			if loadedEntry.Role == committedEntry.Role && strings.TrimSpace(loadedEntry.Text) == strings.TrimSpace(committedEntry.Text) {
				return true
			}
		}
	}
	return false
}

func (m *uiModel) deferProjectedCommittedTail(evt clientui.Event) {
	if m == nil {
		return
	}
	reduction := reduceDeferredCommittedTailDefer(newDeferredCommittedTailState(deferredCommittedTailSnapshotFromModel(m)), evt)
	if !reduction.shouldDefer {
		return
	}
	m.deferredCommittedTail = append(m.deferredCommittedTail, reduction.tail)
	m.transcriptRevision = reduction.revisionAfter
	m.transcriptTotalEntries = reduction.totalEntriesAfter
	m.logTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.defer_tail", map[string]string{
		"session_id":     strings.TrimSpace(m.sessionID),
		"mode":           m.transcriptModeLabel(),
		"kind":           string(evt.Kind),
		"range_start":    strconv.Itoa(reduction.tail.rangeStart),
		"range_end":      strconv.Itoa(reduction.tail.rangeEnd),
		"revision":       strconv.FormatInt(evt.TranscriptRevision, 10),
		"entries_digest": transcriptdiag.EntriesDigest(evt.TranscriptEntries),
		"pending_count":  strconv.Itoa(len(reduction.tail.pending)),
	}))
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

func shouldClearAssistantStreamForCommittedAssistantEvent(evt clientui.Event) bool {
	if evt.Kind != clientui.EventAssistantMessage {
		return false
	}
	for _, entry := range evt.TranscriptEntries {
		if tui.TranscriptRoleFromWire(entry.Role) == tui.TranscriptRoleAssistant {
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
		if tui.TranscriptRoleFromWire(entry.Role) != tui.TranscriptRoleAssistant {
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
		if entry.Role != tui.TranscriptRoleAssistant {
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
		if entry.Role != tui.TranscriptRoleAssistant {
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
	case clientui.EventStreamGap,
		clientui.EventConversationUpdated,
		clientui.EventAssistantDelta,
		clientui.EventReasoningDelta,
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
