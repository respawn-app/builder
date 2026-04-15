package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"builder/cli/tui"
	"builder/shared/clientui"
	"builder/shared/serverapi"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSessionActivityGapRecoveryEventuallyHydratesCommittedTranscriptInBothModes(t *testing.T) {
	oldDelay := uiRuntimeHydrationRetryDelay
	uiRuntimeHydrationRetryDelay = 0
	defer func() { uiRuntimeHydrationRetryDelay = oldDelay }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initial := &stubSessionActivitySubscription{steps: []stubSessionActivityStep{{err: serverapi.ErrStreamGap}}}
	resubscribed := &stubSessionActivitySubscription{}
	remaining := []serverapi.SessionActivitySubscription{resubscribed}
	events, stop := startSessionActivityEvents(ctx, initial, func(context.Context) (serverapi.SessionActivitySubscription, error) {
		if len(remaining) == 0 {
			return nil, context.Canceled
		}
		next := remaining[0]
		remaining = remaining[1:]
		return next, nil
	}, func() bool { return false }, nil)
	defer stop()

	client := &refreshingRuntimeClient{
		transcripts: []clientui.TranscriptPage{
			{SessionID: "session-1"},
			{SessionID: "session-1", SessionName: "debug session", Entries: []clientui.ChatEntry{{Role: "assistant", Text: "final answer after retry"}}, TotalEntries: 1},
		},
		errs: []error{errors.New("temporary refresh failure"), nil},
	}

	m := newProjectedTestUIModel(client, events, closedAskEvents())
	m.startupCmds = nil
	m.termWidth = 90
	m.termHeight = 16
	m.windowSizeKnown = true
	m.syncViewport()

	evt := waitSessionActivityEvent(t, events)
	if evt.Kind != clientui.EventConversationUpdated {
		t.Fatalf("expected synthetic conversation_updated after gap, got %+v", evt)
	}
	if evt.RecoveryCause != clientui.TranscriptRecoveryCauseStreamGap {
		t.Fatalf("expected stream-gap recovery cause, got %+v", evt)
	}

	firstCmd := m.runtimeAdapter().handleProjectedRuntimeEvent(evt)
	if firstCmd == nil {
		t.Fatal("expected first authoritative refresh command")
	}
	firstRefresh, ok := firstCmd().(runtimeTranscriptRefreshedMsg)
	if !ok {
		t.Fatalf("expected runtimeTranscriptRefreshedMsg, got %T", firstCmd())
	}
	if firstRefresh.syncCause != runtimeTranscriptSyncCauseContinuityRecovery {
		t.Fatalf("first sync cause = %q, want %q", firstRefresh.syncCause, runtimeTranscriptSyncCauseContinuityRecovery)
	}
	next, retryCmd := m.Update(firstRefresh)
	if retryCmd == nil {
		t.Fatal("expected retry command after first refresh failure")
	}
	retryMsg, ok := retryCmd().(runtimeTranscriptRetryMsg)
	if !ok {
		t.Fatalf("expected runtimeTranscriptRetryMsg, got %T", retryCmd())
	}
	if retryMsg.recoveryCause != clientui.TranscriptRecoveryCauseStreamGap {
		t.Fatalf("expected retry to preserve stream-gap recovery cause, got %+v", retryMsg)
	}
	if retryMsg.syncCause != runtimeTranscriptSyncCauseContinuityRecovery {
		t.Fatalf("retry sync cause = %q, want %q", retryMsg.syncCause, runtimeTranscriptSyncCauseContinuityRecovery)
	}

	next, secondCmd := next.(*uiModel).Update(retryMsg)
	if secondCmd == nil {
		t.Fatal("expected second authoritative refresh command after retry tick")
	}
	secondRefresh, ok := secondCmd().(runtimeTranscriptRefreshedMsg)
	if !ok {
		t.Fatalf("expected runtimeTranscriptRefreshedMsg, got %T", secondCmd())
	}
	if secondRefresh.syncCause != runtimeTranscriptSyncCauseContinuityRecovery {
		t.Fatalf("second sync cause = %q, want %q", secondRefresh.syncCause, runtimeTranscriptSyncCauseContinuityRecovery)
	}
	next, followUp := next.(*uiModel).Update(secondRefresh)
	m = next.(*uiModel)
	if followUp != nil {
		if _, ok := followUp().(nativeHistoryFlushMsg); !ok {
			// Window-title updates are allowed here; transcript correctness is asserted below.
		}
	}

	ongoing := stripANSIAndTrimRight(m.view.OngoingSnapshot())
	if !strings.Contains(ongoing, "final answer after retry") {
		t.Fatalf("expected ongoing mode to converge after refresh retry, got %q", ongoing)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = next.(*uiModel)
	if m.view.Mode() != tui.ModeDetail {
		t.Fatalf("mode=%q want detail", m.view.Mode())
	}
	detail := stripANSIAndTrimRight(m.view.View())
	if !strings.Contains(detail, "final answer after retry") {
		t.Fatalf("expected detail mode to converge after refresh retry, got %q", detail)
	}

	if client.calls != 2 {
		t.Fatalf("refresh call count = %d, want 2", client.calls)
	}
}

func TestDeferredContinuityRefreshPreservesRecoveryCauseAcrossBusyHydration(t *testing.T) {
	client := &refreshingRuntimeClient{
		transcripts: []clientui.TranscriptPage{{SessionID: "session-1", Entries: []clientui.ChatEntry{{Role: "assistant", Text: "authoritative after gap"}}, TotalEntries: 1}},
	}
	m := newProjectedTestUIModel(client, closedProjectedRuntimeEvents(), closedAskEvents())
	m.startupCmds = nil
	m.runtimeTranscriptBusy = true
	m.runtimeTranscriptToken = 7

	if cmd := m.requestRuntimeTranscriptSyncForContinuityLoss(clientui.TranscriptRecoveryCauseStreamGap); cmd != nil {
		t.Fatalf("expected no command while hydration is already in flight, got %T", cmd)
	}
	if !m.runtimeTranscriptDirty {
		t.Fatal("expected dirty hydrate follow-up after deferred continuity refresh")
	}
	if got := m.runtimeTranscriptDirtyRecoveryCause; got != clientui.TranscriptRecoveryCauseStreamGap {
		t.Fatalf("dirty recovery cause = %q, want %q", got, clientui.TranscriptRecoveryCauseStreamGap)
	}

	next, followCmd := m.Update(runtimeTranscriptRefreshedMsg{token: 7, transcript: clientui.TranscriptPage{SessionID: "session-1"}})
	if followCmd == nil {
		t.Fatal("expected follow-up refresh after dirty hydrate completion")
	}
	followMsg, ok := followCmd().(runtimeTranscriptRefreshedMsg)
	if !ok {
		t.Fatalf("expected runtimeTranscriptRefreshedMsg, got %T", followCmd())
	}
	if followMsg.recoveryCause != clientui.TranscriptRecoveryCauseStreamGap {
		t.Fatalf("follow-up recovery cause = %q, want %q", followMsg.recoveryCause, clientui.TranscriptRecoveryCauseStreamGap)
	}
	if followMsg.syncCause != runtimeTranscriptSyncCauseDirtyFollowUp {
		t.Fatalf("follow-up sync cause = %q, want %q", followMsg.syncCause, runtimeTranscriptSyncCauseDirtyFollowUp)
	}
	updated := next.(*uiModel)
	if updated.runtimeTranscriptDirty {
		t.Fatal("expected dirty hydrate flag cleared once follow-up request starts")
	}
}
