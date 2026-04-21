package app

import (
	"strings"
	"testing"
	"time"

	"builder/server/runtime"
	"builder/shared/clientui"

	"github.com/charmbracelet/lipgloss"
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

func TestRenderProcessStateIndicatorKeepsStableWidthAcrossStates(t *testing.T) {
	running := renderProcessStateIndicator(clientui.BackgroundProcess{State: "running", Running: true}, 0)
	completed := renderProcessStateIndicator(clientui.BackgroundProcess{State: "completed"}, 0)

	if got, want := lipgloss.Width(running), pendingToolSpinnerWidth(); got != want {
		t.Fatalf("expected running indicator width %d, got %d (%q)", want, got, running)
	}
	if got, want := lipgloss.Width(completed), lipgloss.Width(running); got != want {
		t.Fatalf("expected completed indicator width %d, got %d (%q)", want, got, completed)
	}
}

func TestPendingToolSpinnerFramesStayTwoCellsWide(t *testing.T) {
	hasLeadingSpaceFrame := false
	for idx, frame := range pendingToolSpinner.Frames {
		if got := lipgloss.Width(frame); got != 2 {
			t.Fatalf("expected spinner frame %d to stay 2 cells wide, got %d (%q)", idx, got, frame)
		}
		if len(frame) > 0 && frame[0] == ' ' {
			hasLeadingSpaceFrame = true
		}
	}
	if !hasLeadingSpaceFrame {
		t.Fatal("expected spinner frames to include leading-space frame")
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

func TestRunningStatusLineSpinnerFitsNarrowWidth(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 18
	m.termHeight = 20
	m.windowSizeKnown = true
	m.activity = uiActivityRunning
	m.busy = true
	m.spinnerFrame = 0

	line := m.renderStatusLine(m.termWidth, uiThemeStyles("dark"))
	if got := lipgloss.Width(line); got > m.termWidth {
		t.Fatalf("expected running status line to fit width %d, got %d in %q", m.termWidth, got, stripANSIAndTrimRight(line))
	}
	if plain := stripANSIPreserve(line); !strings.Contains(plain, pendingToolSpinnerFrame(0)) {
		t.Fatalf("expected running status line to contain spinner frame %q, got %q", pendingToolSpinnerFrame(0), plain)
	}
}

func TestNonRunningIndicatorGlyphMatchesStatuslineAndProcessList(t *testing.T) {
	statusIndicator := stripANSIPreserve(renderStatusDot("dark", uiActivityIdle, 0))
	processIndicator := renderProcessStateIndicator(clientui.BackgroundProcess{State: "completed"}, 0)

	if statusIndicator != statusStateCircleGlyph {
		t.Fatalf("expected statusline idle indicator %q, got %q", statusStateCircleGlyph, statusIndicator)
	}
	if got := lipgloss.Width(statusIndicator); got != lipgloss.Width(statusStateCircleGlyph) {
		t.Fatalf("expected statusline idle indicator width %d, got %d (%q)", lipgloss.Width(statusStateCircleGlyph), got, statusIndicator)
	}
	if processIndicator != padSpinnerIndicator(statusStateCircleGlyph) {
		t.Fatalf("expected /ps non-running indicator %q, got %q", padSpinnerIndicator(statusStateCircleGlyph), processIndicator)
	}
	if got := lipgloss.Width(processIndicator); got != pendingToolSpinnerWidth() {
		t.Fatalf("expected /ps non-running indicator width %d, got %d (%q)", pendingToolSpinnerWidth(), got, processIndicator)
	}
	if strings.TrimSpace(processIndicator) != statusStateCircleGlyph {
		t.Fatalf("expected /ps non-running indicator glyph %q, got %q", statusStateCircleGlyph, processIndicator)
	}
}

func TestStatusIndicatorContracts(t *testing.T) {
	staticCases := []struct {
		name     string
		activity uiActivity
	}{
		{name: "idle", activity: uiActivityIdle},
		{name: "interrupted", activity: uiActivityInterrupted},
		{name: "error", activity: uiActivityError},
	}
	for _, tc := range staticCases {
		t.Run(tc.name, func(t *testing.T) {
			indicator := stripANSIPreserve(renderStatusDot("dark", tc.activity, 0))
			if indicator != statusStateCircleGlyph {
				t.Fatalf("expected %s indicator glyph %q, got %q", tc.name, statusStateCircleGlyph, indicator)
			}
			if got := lipgloss.Width(indicator); got != lipgloss.Width(statusStateCircleGlyph) {
				t.Fatalf("expected %s indicator width %d, got %d (%q)", tc.name, lipgloss.Width(statusStateCircleGlyph), got, indicator)
			}
		})
	}

	reviewer := stripANSIPreserve(renderReviewerStatus(0))
	if !strings.Contains(reviewer, pendingToolSpinnerFrame(0)) {
		t.Fatalf("expected reviewer indicator to use spinner frame %q, got %q", pendingToolSpinnerFrame(0), reviewer)
	}
	if !strings.Contains(reviewer, "reviewing") {
		t.Fatalf("expected reviewer indicator to keep label, got %q", reviewer)
	}

	compaction := stripANSIPreserve(renderCompactionStatus(0))
	if !strings.Contains(compaction, pendingToolSpinnerFrame(0)) {
		t.Fatalf("expected compaction indicator to use spinner frame %q, got %q", pendingToolSpinnerFrame(0), compaction)
	}
	if !strings.Contains(compaction, "compacting") {
		t.Fatalf("expected compaction indicator to keep label, got %q", compaction)
	}
}

func TestQueuedBookkeepingDoesNotChangeStatusIndicator(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 80
	m.termHeight = 20
	m.windowSizeKnown = true
	m.activity = uiActivityIdle
	style := uiThemeStyles("dark")
	before := m.renderStatusLine(m.termWidth, style)
	beforePlain := stripANSIPreserve(before)
	beforeWidth := lipgloss.Width(before)

	m.queueInput("follow up")
	if !m.enqueueInjectedInput("steering") {
		t.Fatal("expected bookkeeping injection to succeed")
	}
	if m.activity != uiActivityIdle {
		t.Fatalf("expected queue bookkeeping to preserve idle activity, got %v", m.activity)
	}

	after := m.renderStatusLine(m.termWidth, style)
	afterPlain := stripANSIPreserve(after)
	afterWidth := lipgloss.Width(after)
	if beforePlain != afterPlain {
		t.Fatalf("expected queue bookkeeping to leave statusline text unchanged, before=%q after=%q", beforePlain, afterPlain)
	}
	if beforeWidth != afterWidth {
		t.Fatalf("expected queue bookkeeping to preserve statusline width %d, got %d", beforeWidth, afterWidth)
	}
	indicator := stripANSIPreserve(renderStatusDot("dark", m.activity, 0))
	if indicator != statusStateCircleGlyph {
		t.Fatalf("expected queue bookkeeping to keep idle indicator %q, got %q", statusStateCircleGlyph, indicator)
	}
	if got := lipgloss.Width(indicator); got != lipgloss.Width(statusStateCircleGlyph) {
		t.Fatalf("expected queue bookkeeping indicator width %d, got %d (%q)", lipgloss.Width(statusStateCircleGlyph), got, indicator)
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
	if got := stripANSIPreserve(started.renderStatusLine(120, uiThemeStyles("dark"))); !strings.Contains(got, pendingToolSpinnerFrame(0)) {
		t.Fatalf("expected reviewer status line to start on spinner frame 0, got %q", got)
	}

	token := started.spinnerTickToken
	tickAt := anchor.Add(25 * time.Millisecond)
	next, _ = started.Update(spinnerTickMsg{token: token, at: tickAt})
	advanced := next.(*uiModel)
	if got, want := advanced.spinnerFrame, 2; got != want {
		t.Fatalf("expected reviewer-only spinner to advance to frame %d, got %d", want, got)
	}
	if got := stripANSIPreserve(advanced.renderStatusLine(120, uiThemeStyles("dark"))); !strings.Contains(got, pendingToolSpinnerFrame(2)) {
		t.Fatalf("expected reviewer status line to advance to frame %q, got %q", pendingToolSpinnerFrame(2), got)
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

func TestCompactionStatusLineSpinnerFitsNarrowWidth(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 18
	m.termHeight = 20
	m.windowSizeKnown = true
	m.compacting = true
	m.spinnerFrame = 0

	line := m.renderStatusLine(m.termWidth, uiThemeStyles("dark"))
	if got := lipgloss.Width(line); got > m.termWidth {
		t.Fatalf("expected compaction status line to fit width %d, got %d in %q", m.termWidth, got, stripANSIAndTrimRight(line))
	}
	plain := stripANSIPreserve(line)
	if !strings.Contains(plain, pendingToolSpinnerFrame(0)) {
		t.Fatalf("expected compaction status line to contain spinner frame %q, got %q", pendingToolSpinnerFrame(0), plain)
	}
	if !strings.Contains(plain, "comp") {
		t.Fatalf("expected compaction status line to retain compacting label, got %q", plain)
	}
	if strings.Contains(plain, "⚠") {
		t.Fatalf("did not expect legacy compaction warning glyph, got %q", plain)
	}
}
