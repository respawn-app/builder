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
	})
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

	firstCmd := m.runtimeAdapter().handleProjectedRuntimeEvent(evt)
	if firstCmd == nil {
		t.Fatal("expected first authoritative refresh command")
	}
	firstRefresh, ok := firstCmd().(runtimeTranscriptRefreshedMsg)
	if !ok {
		t.Fatalf("expected runtimeTranscriptRefreshedMsg, got %T", firstCmd())
	}
	next, retryCmd := m.Update(firstRefresh)
	if retryCmd == nil {
		t.Fatal("expected retry command after first refresh failure")
	}
	retryMsg, ok := retryCmd().(runtimeTranscriptRetryMsg)
	if !ok {
		t.Fatalf("expected runtimeTranscriptRetryMsg, got %T", retryCmd())
	}

	next, secondCmd := next.(*uiModel).Update(retryMsg)
	if secondCmd == nil {
		t.Fatal("expected second authoritative refresh command after retry tick")
	}
	secondRefresh, ok := secondCmd().(runtimeTranscriptRefreshedMsg)
	if !ok {
		t.Fatalf("expected runtimeTranscriptRefreshedMsg, got %T", secondCmd())
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
