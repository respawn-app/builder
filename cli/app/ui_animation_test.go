package app

import (
	"testing"
	"time"

	"builder/server/runtime"
)

func TestFrameAnimationClockUsesElapsedFrameBoundaries(t *testing.T) {
	var clock frameAnimationClock
	anchor := time.Unix(1_700_000_000, 0)
	clock.Start(anchor)

	if got := clock.Frame(anchor.Add(-time.Millisecond), 8, 80*time.Millisecond); got != 0 {
		t.Fatalf("expected negative elapsed frame to clamp to 0, got %d", got)
	}
	if got := clock.Frame(anchor.Add(79*time.Millisecond), 8, 80*time.Millisecond); got != 0 {
		t.Fatalf("expected first frame before boundary, got %d", got)
	}
	if got := clock.Frame(anchor.Add(80*time.Millisecond), 8, 80*time.Millisecond); got != 1 {
		t.Fatalf("expected second frame at first boundary, got %d", got)
	}
	if got := clock.Frame(anchor.Add(640*time.Millisecond), 8, 80*time.Millisecond); got != 0 {
		t.Fatalf("expected frame index to wrap after full cycle, got %d", got)
	}
	if got := clock.NextDelay(anchor.Add(241*time.Millisecond), 80*time.Millisecond); got != 79*time.Millisecond {
		t.Fatalf("expected next delay aligned to next frame boundary, got %s", got)
	}
}

func TestHandleSpinnerTickJumpsFromElapsedTimeAndKeepsBoundaryAlignedDelay(t *testing.T) {
	oldInterval := spinnerTickInterval
	spinnerTickInterval = 10 * time.Millisecond
	t.Cleanup(func() { spinnerTickInterval = oldInterval })

	anchor := time.Unix(1_700_000_100, 0)
	m := newProjectedStaticUIModel()
	m.busy = true
	m.spinnerTickToken = 1
	m.spinnerGeneration = 1
	m.spinnerClock.Start(anchor)

	tickAt := anchor.Add(35 * time.Millisecond)
	next, cmd := m.inputController().handleSpinnerTick(spinnerTickMsg{token: 1, at: tickAt})
	updated := next.(*uiModel)
	if got, want := updated.spinnerFrame, 3; got != want {
		t.Fatalf("expected late tick to jump to frame %d from elapsed time, got %d", want, got)
	}
	if got, want := updated.spinnerClock.NextDelay(tickAt, spinnerTickInterval), 5*time.Millisecond; got != want {
		t.Fatalf("expected next delay %s after late tick, got %s", want, got)
	}
	if cmd == nil {
		t.Fatal("expected spinner tick to schedule next boundary-aligned tick")
	}
}

func TestReviewerOnlyRuntimeEventStartsAdvancesAndStopsSpinner(t *testing.T) {
	oldInterval := spinnerTickInterval
	oldNow := uiAnimationNow
	spinnerTickInterval = 10 * time.Millisecond
	anchor := time.Unix(1_700_000_200, 0)
	uiAnimationNow = func() time.Time { return anchor }
	t.Cleanup(func() {
		spinnerTickInterval = oldInterval
		uiAnimationNow = oldNow
	})

	m := newProjectedStaticUIModel()
	next, _ := m.Update(projectedRuntimeEventMsg(runtime.Event{Kind: runtime.EventReviewerStarted}))
	started := next.(*uiModel)
	if !started.reviewerRunning {
		t.Fatal("expected reviewer to start running")
	}
	if started.spinnerTickToken == 0 {
		t.Fatal("expected reviewer-only runtime event to start spinner ticking")
	}

	token := started.spinnerTickToken
	tickAt := anchor.Add(25 * time.Millisecond)
	next, _ = started.Update(spinnerTickMsg{token: token, at: tickAt})
	advanced := next.(*uiModel)
	if got, want := advanced.spinnerFrame, 2; got != want {
		t.Fatalf("expected reviewer-only spinner to advance to frame %d, got %d", want, got)
	}

	next, _ = advanced.Update(projectedRuntimeEventMsg(runtime.Event{Kind: runtime.EventReviewerCompleted}))
	completed := next.(*uiModel)
	if completed.reviewerRunning {
		t.Fatal("expected reviewer running state cleared on completion")
	}
	if completed.spinnerTickToken != 0 {
		t.Fatalf("expected reviewer completion to stop spinner ticking, got token %d", completed.spinnerTickToken)
	}
}
