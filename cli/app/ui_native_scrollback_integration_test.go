package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"builder/cli/tui"
	"builder/server/llm"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/tools"
	shelltool "builder/server/tools/shell"
	"builder/shared/clientui"
	"builder/shared/config"
	"builder/shared/transcript"
	"builder/shared/transcript/toolcodec"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
)

func closedAskEvents() <-chan askEvent {
	ch := make(chan askEvent)
	close(ch)
	return ch
}

func normalizedOutput(v string) string {
	return strings.Join(strings.Fields(xansi.Strip(v)), " ")
}

func waitForTestCondition(t *testing.T, timeout time.Duration, description string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if check() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", description)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForSubmitResult(t *testing.T, timeout time.Duration, submitDone <-chan error) {
	t.Helper()
	select {
	case err := <-submitDone:
		if err != nil {
			t.Fatalf("submit user message: %v", err)
		}
	case <-time.After(timeout):
		t.Fatal("timed out waiting for submit user message completion")
	}
}

type singleChunkStreamClient struct {
	delta string
}

type noopFinalStreamClient struct{}

type asyncLateDeltaStreamClient struct {
	initial string
	late    string
	delay   time.Duration
}

type gatedStreamClient struct {
	started chan struct{}
	release chan struct{}
	mu      sync.Mutex
	lastReq llm.Request
}

type deferredFinalQueuedInjectionStreamClient struct {
	mu    sync.Mutex
	calls int
	delay time.Duration
}

type queuedSteerDuringBlockingToolClient struct {
	mu    sync.Mutex
	calls int
}

type blockingShellTool struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

type reviewerNoSuggestionsClient struct{}

type staleTranscriptRuntimeClient struct {
	runtimeControlFakeClient
	loadCalls atomic.Int32
	page      clientui.TranscriptPage
}

type gatedRefreshRuntimeClient struct {
	runtimeControlFakeClient
	page           clientui.TranscriptPage
	refreshStarted chan struct{}
	releaseRefresh chan struct{}
	refreshOnce    sync.Once
}

func (c *staleTranscriptRuntimeClient) MainView() clientui.RuntimeMainView {
	if c.sessionView.SessionID == "" {
		c.sessionView.SessionID = "session-1"
	}
	return clientui.RuntimeMainView{Session: c.sessionView}
}

func (c *staleTranscriptRuntimeClient) RefreshMainView() (clientui.RuntimeMainView, error) {
	return c.MainView(), nil
}

func (c *staleTranscriptRuntimeClient) LoadTranscriptPage(req clientui.TranscriptPageRequest) (clientui.TranscriptPage, error) {
	_ = req
	c.loadCalls.Add(1)
	page := c.page
	if page.SessionID == "" {
		page.SessionID = "session-1"
	}
	return page, nil
}

func (c *staleTranscriptRuntimeClient) RefreshTranscriptPage(req clientui.TranscriptPageRequest) (clientui.TranscriptPage, error) {
	return c.LoadTranscriptPage(req)
}

func (c *staleTranscriptRuntimeClient) LoadCalls() int {
	if c == nil {
		return 0
	}
	return int(c.loadCalls.Load())
}

func (c *gatedRefreshRuntimeClient) LoadTranscriptPage(req clientui.TranscriptPageRequest) (clientui.TranscriptPage, error) {
	_ = req
	page := c.page
	if page.SessionID == "" {
		page.SessionID = "session-1"
	}
	return page, nil
}

func (c *gatedRefreshRuntimeClient) RefreshTranscriptPage(req clientui.TranscriptPageRequest) (clientui.TranscriptPage, error) {
	c.refreshOnce.Do(func() {
		close(c.refreshStarted)
	})
	<-c.releaseRefresh
	return c.LoadTranscriptPage(req)
}

func (c singleChunkStreamClient) Generate(_ context.Context, _ llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New("not implemented")
}

func (c singleChunkStreamClient) GenerateStream(_ context.Context, _ llm.Request, onDelta func(string)) (llm.Response, error) {
	if onDelta != nil {
		onDelta(c.delta)
	}
	return llm.Response{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: c.delta},
		Usage:     llm.Usage{WindowTokens: 200_000},
	}, nil
}

func (noopFinalStreamClient) Generate(_ context.Context, _ llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New("not implemented")
}

func (noopFinalStreamClient) GenerateStream(_ context.Context, _ llm.Request, onDelta func(string)) (llm.Response, error) {
	if onDelta != nil {
		onDelta("NO_OP")
	}
	return llm.Response{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "NO_OP", Phase: llm.MessagePhaseFinal},
		Usage:     llm.Usage{WindowTokens: 200_000},
	}, nil
}

func (c asyncLateDeltaStreamClient) Generate(_ context.Context, _ llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New("not implemented")
}

func (c asyncLateDeltaStreamClient) GenerateStream(_ context.Context, _ llm.Request, onDelta func(string)) (llm.Response, error) {
	if onDelta != nil {
		onDelta(c.initial)
	}
	if onDelta != nil && strings.TrimSpace(c.late) != "" {
		go func() {
			time.Sleep(c.delay)
			onDelta(c.late)
		}()
	}
	return llm.Response{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: c.initial},
		Usage:     llm.Usage{WindowTokens: 200_000},
	}, nil
}

func (c *gatedStreamClient) Generate(_ context.Context, _ llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New("not implemented")
}

func (c *gatedStreamClient) GenerateStream(_ context.Context, req llm.Request, onDelta func(string)) (llm.Response, error) {
	c.mu.Lock()
	c.lastReq = req
	c.mu.Unlock()
	close(c.started)
	<-c.release
	if onDelta != nil {
		onDelta("assistant")
	}
	return llm.Response{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "assistant"},
		Usage:     llm.Usage{WindowTokens: 200_000},
	}, nil
}

func (c *deferredFinalQueuedInjectionStreamClient) Generate(_ context.Context, _ llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New("not implemented")
}

func (c *queuedSteerDuringBlockingToolClient) Generate(_ context.Context, _ llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New("not implemented")
}

func (c *deferredFinalQueuedInjectionStreamClient) GenerateStream(_ context.Context, _ llm.Request, onDelta func(string)) (llm.Response, error) {
	c.mu.Lock()
	call := c.calls
	c.calls++
	delay := c.delay
	c.mu.Unlock()
	if call == 0 {
		if onDelta != nil {
			onDelta("foreground done")
		}
		if delay > 0 {
			time.Sleep(delay)
		}
		return llm.Response{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "foreground done", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200_000},
		}, nil
	}
	if onDelta != nil {
		onDelta("NO_OP")
	}
	return llm.Response{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "NO_OP", Phase: llm.MessagePhaseFinal},
		Usage:     llm.Usage{WindowTokens: 200_000},
	}, nil
}

func (c *queuedSteerDuringBlockingToolClient) GenerateStream(_ context.Context, _ llm.Request, onDelta func(string)) (llm.Response, error) {
	c.mu.Lock()
	call := c.calls
	c.calls++
	c.mu.Unlock()
	if call == 0 {
		if onDelta != nil {
			onDelta("working")
		}
		return llm.Response{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "working", Phase: llm.MessagePhaseCommentary},
			ToolCalls: []llm.ToolCall{{
				ID:    "call-1",
				Name:  string(tools.ToolShell),
				Input: json.RawMessage(`{"command":"sleep 1"}`),
				Presentation: toolcodec.EncodeToolCallMeta(transcript.ToolCallMeta{
					ToolName:    "shell",
					IsShell:     true,
					Command:     "sleep 1",
					CompactText: "sleep 1",
				}),
			}},
			Usage: llm.Usage{WindowTokens: 200_000},
		}, nil
	}
	if onDelta != nil {
		onDelta("after steer")
	}
	return llm.Response{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "after steer", Phase: llm.MessagePhaseFinal},
		Usage:     llm.Usage{WindowTokens: 200_000},
	}, nil
}

func (t *blockingShellTool) Name() tools.ID {
	return tools.ToolShell
}

func (t *blockingShellTool) Call(ctx context.Context, c tools.Call) (tools.Result, error) {
	t.once.Do(func() {
		close(t.started)
	})
	select {
	case <-t.release:
	case <-ctx.Done():
		return tools.Result{CallID: c.ID, Name: tools.ToolShell, IsError: true, Output: []byte(`{"error":"context canceled"}`)}, ctx.Err()
	}
	return tools.Result{CallID: c.ID, Name: tools.ToolShell, Output: []byte(`"/tmp"`)}, nil
}

func (reviewerNoSuggestionsClient) Generate(_ context.Context, _ llm.Request) (llm.Response, error) {
	return llm.Response{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: `{"suggestions":[]}`},
		Usage:     llm.Usage{WindowTokens: 200_000},
	}, nil
}

func TestNativeScrollbackProgramOutputContract(t *testing.T) {
	out := &bytes.Buffer{}
	model := newProjectedTestUIModel(
		nil,
		closedProjectedRuntimeEvents(),
		closedAskEvents(),
		WithUIInitialTranscript([]UITranscriptEntry{
			{Role: "user", Text: "first replay line"},
			{Role: "assistant", Text: "second replay line"},
		}),
	)

	program := tea.NewProgram(
		model,
		tea.WithInput(strings.NewReader("")),
		tea.WithOutput(out),
		tea.WithoutSignals(),
	)

	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()

	time.Sleep(40 * time.Millisecond)
	program.Send(nativeHistoryFlushMsg{Text: "delta replay line"})
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 32})
	time.Sleep(20 * time.Millisecond)
	program.Quit()

	select {
	case err := <-done:
		if err != nil && !strings.Contains(err.Error(), "context canceled") {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}

	raw := out.String()
	normalized := normalizedOutput(raw)
	if !strings.Contains(raw, "\x1b[2J") {
		t.Fatalf("expected startup clear-screen sequence in native mode output")
	}
	if strings.Contains(raw, "\x1b[?1049h") || strings.Contains(raw, "\x1b[?1049l") {
		t.Fatalf("did not expect alt-screen enter/leave sequences in native mode output")
	}
	if strings.Contains(raw, "\x1b[?1000h") || strings.Contains(raw, "\x1b[?1002h") || strings.Contains(raw, "\x1b[?1003h") || strings.Contains(raw, "\x1b[?1006h") {
		t.Fatalf("did not expect mouse-capture enable sequences in native mode output")
	}
	if strings.Count(normalized, "first replay line") != 1 {
		t.Fatalf("expected startup replay line exactly once, got %d", strings.Count(normalized, "first replay line"))
	}
	if strings.Count(normalized, "delta replay line") != 1 {
		t.Fatalf("expected delta replay exactly once, got %d", strings.Count(normalized, "delta replay line"))
	}
	if strings.Contains(raw, strings.Repeat(" ", 400)) {
		t.Fatalf("expected native mode to avoid frame-sized whitespace rewrites")
	}
	plain := xansi.Strip(raw)
	if occurrences := strings.Count(plain, "ongoing | "); occurrences > 12 {
		t.Fatalf("expected bounded status redraw output, got %d occurrences", occurrences)
	}
}

func TestNativeScrollbackInitClearsOnEachProgramRun(t *testing.T) {
	run := func() string {
		t.Helper()
		out := &bytes.Buffer{}
		model := newProjectedTestUIModel(nil, closedProjectedRuntimeEvents(), closedAskEvents())

		program := tea.NewProgram(
			model,
			tea.WithInput(strings.NewReader("")),
			tea.WithOutput(out),
			tea.WithoutSignals(),
		)

		done := make(chan error, 1)
		go func() {
			_, err := program.Run()
			done <- err
		}()

		time.Sleep(40 * time.Millisecond)
		program.Send(tea.WindowSizeMsg{Width: 120, Height: 32})
		time.Sleep(20 * time.Millisecond)
		program.Send(tea.KeyMsg{Type: tea.KeyCtrlC})

		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("program run failed: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("program did not terminate")
		}

		return out.String()
	}

	first := run()
	second := run()
	if !strings.Contains(first, "\x1b[2J") {
		t.Fatalf("expected first startup to clear screen, output=%q", first)
	}
	if !strings.Contains(second, "\x1b[2J") {
		t.Fatalf("expected second startup to clear screen, output=%q", second)
	}
}

func TestNativeResizeReplaysOngoingScreenAfterRealResize(t *testing.T) {
	previousDebounce := nativeResizeReplayDebounce
	nativeResizeReplayDebounce = 20 * time.Millisecond
	t.Cleanup(func() {
		nativeResizeReplayDebounce = previousDebounce
	})

	out := &bytes.Buffer{}
	model := newProjectedTestUIModel(
		nil,
		closedProjectedRuntimeEvents(),
		closedAskEvents(),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "seed replay line"}}),
	)
	model.input = "line one\nline two"

	program := tea.NewProgram(
		model,
		tea.WithInput(strings.NewReader("")),
		tea.WithOutput(out),
		tea.WithoutSignals(),
	)

	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()

	time.Sleep(40 * time.Millisecond)
	for _, size := range []tea.WindowSizeMsg{
		{Width: 120, Height: 30},
		{Width: 96, Height: 30},
		{Width: 110, Height: 30},
		{Width: 84, Height: 30},
	} {
		program.Send(size)
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	program.Quit()

	select {
	case err := <-done:
		if err != nil && !strings.Contains(err.Error(), "context canceled") {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}

	raw := out.String()
	if count := strings.Count(raw, "\x1b[2J"); count < 2 || count > 3 {
		t.Fatalf("expected startup clear plus 1-2 width-resize replay clears, got %d occurrences in %q", count, raw)
	}
	plain := xansi.Strip(raw)
	if count := strings.Count(normalizedOutput(raw), "seed replay line"); count < 2 || count > 3 {
		t.Fatalf("expected committed history to replay at least once after debounced width resize burst, got %q", normalizedOutput(raw))
	}
	for _, line := range strings.Split(plain, "\n") {
		if strings.Count(line, "ongoing | ") > 1 {
			t.Fatalf("expected no duplicated status segment in a single rendered line, got %q", line)
		}
	}
	borderLines := 0
	for _, line := range strings.Split(plain, "\n") {
		if strings.Contains(line, strings.Repeat("─", 12)) {
			borderLines++
		}
	}
	if borderLines > 24 {
		t.Fatalf("expected bounded border redraw count during resize, got %d", borderLines)
	}
	if strings.Count(plain, "ongoing | ") > 16 {
		t.Fatalf("expected bounded status redraw count during resize, got %d", strings.Count(plain, "ongoing | "))
	}
}

func TestNativeResizeClearWithoutHistoryRedrawsSingleLiveRegion(t *testing.T) {
	previousDebounce := nativeResizeReplayDebounce
	nativeResizeReplayDebounce = 20 * time.Millisecond
	t.Cleanup(func() {
		nativeResizeReplayDebounce = previousDebounce
	})

	out := &bytes.Buffer{}
	model := newProjectedTestUIModel(nil, closedProjectedRuntimeEvents(), closedAskEvents())
	model.input = "top\ncurrent\nbottom"

	program := tea.NewProgram(
		model,
		tea.WithInput(strings.NewReader("")),
		tea.WithOutput(out),
		tea.WithoutSignals(),
	)

	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()

	time.Sleep(40 * time.Millisecond)
	for _, size := range []tea.WindowSizeMsg{
		{Width: 120, Height: 30},
		{Width: 96, Height: 24},
		{Width: 110, Height: 28},
		{Width: 84, Height: 22},
	} {
		program.Send(size)
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(40 * time.Millisecond)
	program.Quit()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}

	raw := out.String()
	if count := strings.Count(raw, "\x1b[2J"); count < 1 {
		t.Fatalf("expected startup clear-screen sequence in no-history path, got %d occurrences in %q", count, raw)
	}
	plain := xansi.Strip(raw)
	if !strings.Contains(plain, "top") || !strings.Contains(plain, "current") || !strings.Contains(plain, "bottom") {
		t.Fatalf("expected multiline input to remain visible after repeated resizes, got %q", plain)
	}
	for _, line := range strings.Split(plain, "\n") {
		if strings.Count(line, "ongoing | ") > 1 {
			t.Fatalf("expected no duplicated status segment in a single rendered line, got %q", line)
		}
		if strings.Count(line, "› ") > 1 {
			t.Fatalf("expected no duplicated input prompt in a single rendered line, got %q", line)
		}
	}
	borderLines := 0
	for _, line := range strings.Split(plain, "\n") {
		if strings.Contains(line, strings.Repeat("─", 12)) {
			borderLines++
		}
	}
	if borderLines > 16 {
		t.Fatalf("expected bounded border redraw count in no-history resize path, got %d", borderLines)
	}
	if strings.Count(plain, "ongoing | ") > 12 {
		t.Fatalf("expected bounded status redraw count in no-history resize path, got %d", strings.Count(plain, "ongoing | "))
	}
}

func TestNativeRollbackOverlayCtrlCBalancesAltScreenAndAlternateScroll(t *testing.T) {
	var terminalSequences []string
	originalWriteTerminalSequence := writeTerminalSequence
	writeTerminalSequence = func(sequence string) {
		terminalSequences = append(terminalSequences, sequence)
	}
	defer func() {
		writeTerminalSequence = originalWriteTerminalSequence
	}()

	out := &bytes.Buffer{}
	model := newProjectedTestUIModel(
		nil,
		closedProjectedRuntimeEvents(),
		closedAskEvents(),
		WithUIAlternateScreenPolicy(config.TUIAlternateScreenAuto),
		WithUIInitialTranscript([]UITranscriptEntry{
			{Role: "user", Text: "u1"},
			{Role: "assistant", Text: "a1"},
			{Role: "user", Text: "u2"},
		}),
	)

	program := tea.NewProgram(
		model,
		tea.WithInput(strings.NewReader("")),
		tea.WithOutput(out),
		tea.WithoutSignals(),
	)

	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()

	time.Sleep(40 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 32})
	time.Sleep(20 * time.Millisecond)
	program.Send(tea.KeyMsg{Type: tea.KeyEsc})
	program.Send(tea.KeyMsg{Type: tea.KeyEsc})
	waitForTestCondition(t, 2*time.Second, "rollback overlay to open", func() bool {
		return model.rollback.isSelecting() && model.rollback.ownsTranscriptMode && model.view.Mode() == tui.ModeDetail
	})
	program.Send(tea.KeyMsg{Type: tea.KeyCtrlC})

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}

	raw := out.String()
	enterAlt := strings.Count(raw, "\x1b[?1049h")
	exitAlt := strings.Count(raw, "\x1b[?1049l")
	if enterAlt != exitAlt {
		t.Fatalf("expected balanced alt-screen enter/exit sequences, enter=%d exit=%d", enterAlt, exitAlt)
	}
	if enterAlt == 0 {
		t.Fatal("expected rollback overlay in native mode to enter alt-screen under auto policy")
	}
	sequenceLog := strings.Join(terminalSequences, "")
	enableAltScroll := strings.Count(sequenceLog, "\x1b[?1007h")
	disableAltScroll := strings.Count(sequenceLog, "\x1b[?1007l")
	if enableAltScroll != disableAltScroll {
		t.Fatalf("expected balanced alternate-scroll enable/disable sequences, enable=%d disable=%d", enableAltScroll, disableAltScroll)
	}
	if enableAltScroll == 0 {
		t.Fatal("expected rollback overlay in native mode to enable alternate scroll under auto policy")
	}
}

func TestNativePSOverlayEscBalancesAltScreenAndAlternateScroll(t *testing.T) {
	var terminalSequences []string
	originalWriteTerminalSequence := writeTerminalSequence
	writeTerminalSequence = func(sequence string) {
		terminalSequences = append(terminalSequences, sequence)
	}
	defer func() {
		writeTerminalSequence = originalWriteTerminalSequence
	}()

	out := &bytes.Buffer{}
	model := newProjectedTestUIModel(
		nil,
		closedProjectedRuntimeEvents(),
		closedAskEvents(),
		WithUIAlternateScreenPolicy(config.TUIAlternateScreenAuto),
	)
	model.input = "/ps"

	program := tea.NewProgram(
		model,
		tea.WithInput(strings.NewReader("")),
		tea.WithOutput(out),
		tea.WithoutSignals(),
	)

	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()

	time.Sleep(40 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 32})
	time.Sleep(20 * time.Millisecond)
	program.Send(tea.KeyMsg{Type: tea.KeyEnter})
	time.Sleep(20 * time.Millisecond)
	program.Send(tea.KeyMsg{Type: tea.KeyEsc})
	waitForTestCondition(t, 2*time.Second, "/ps overlay to close", func() bool {
		return !model.processList.isOpen() && !model.processList.ownsTranscriptMode && model.view.Mode() == tui.ModeOngoing
	})
	waitForTestCondition(t, 2*time.Second, "/ps alternate scroll to disable", func() bool {
		return strings.Count(strings.Join(terminalSequences, ""), "\x1b[?1007l") > 0
	})
	program.Quit()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}

	raw := out.String()
	enterAlt := strings.Count(raw, "\x1b[?1049h")
	exitAlt := strings.Count(raw, "\x1b[?1049l")
	if enterAlt != exitAlt {
		t.Fatalf("expected balanced /ps alt-screen enter/exit sequences, enter=%d exit=%d", enterAlt, exitAlt)
	}
	if enterAlt == 0 {
		t.Fatal("expected /ps overlay in native mode to enter alt-screen under auto policy")
	}
	sequenceLog := strings.Join(terminalSequences, "")
	enableAltScroll := strings.Count(sequenceLog, "\x1b[?1007h")
	disableAltScroll := strings.Count(sequenceLog, "\x1b[?1007l")
	if enableAltScroll != disableAltScroll {
		t.Fatalf("expected balanced /ps alternate-scroll enable/disable sequences, enable=%d disable=%d", enableAltScroll, disableAltScroll)
	}
	if enableAltScroll == 0 {
		t.Fatal("expected /ps overlay in native mode to enable alternate scroll under auto policy")
	}
	if !strings.Contains(normalizedOutput(raw), "Background Processes") {
		t.Fatalf("expected /ps overlay content in output, got %q", normalizedOutput(raw))
	}
}

func TestNativePSOverlayUsesClearScreenWhenAltScreenNever(t *testing.T) {
	var terminalSequences []string
	originalWriteTerminalSequence := writeTerminalSequence
	writeTerminalSequence = func(sequence string) {
		terminalSequences = append(terminalSequences, sequence)
	}
	defer func() {
		writeTerminalSequence = originalWriteTerminalSequence
	}()

	out := &bytes.Buffer{}
	model := newProjectedTestUIModel(
		nil,
		closedProjectedRuntimeEvents(),
		closedAskEvents(),
		WithUIAlternateScreenPolicy(config.TUIAlternateScreenNever),
	)
	model.input = "/ps"

	program := tea.NewProgram(
		model,
		tea.WithInput(strings.NewReader("")),
		tea.WithOutput(out),
		tea.WithoutSignals(),
	)

	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()

	time.Sleep(40 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 32})
	time.Sleep(20 * time.Millisecond)
	program.Send(tea.KeyMsg{Type: tea.KeyEnter})
	time.Sleep(20 * time.Millisecond)
	program.Send(tea.KeyMsg{Type: tea.KeyEsc})
	time.Sleep(20 * time.Millisecond)
	program.Quit()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}

	raw := out.String()
	if strings.Contains(raw, "\x1b[?1049h") || strings.Contains(raw, "\x1b[?1049l") {
		t.Fatalf("did not expect /ps overlay to use alt-screen when detail alt-screen is disabled, got %q", raw)
	}
	sequenceLog := strings.Join(terminalSequences, "")
	if strings.Contains(sequenceLog, "\x1b[?1007h") || strings.Contains(sequenceLog, "\x1b[?1007l") {
		t.Fatalf("did not expect /ps overlay to toggle alternate scroll when detail alt-screen is disabled, got %q", sequenceLog)
	}
	if clearCount := strings.Count(raw, "\x1b[2J"); clearCount < 2 {
		t.Fatalf("expected startup + /ps open clear-screen sequences, got %d in %q", clearCount, raw)
	}
	if !strings.Contains(normalizedOutput(raw), "Background Processes") {
		t.Fatalf("expected /ps overlay content in output, got %q", normalizedOutput(raw))
	}
}

func TestNativeFinalizeDoesNotBlinkDuplicateTailTokens(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	runtimeEvents := make(chan runtime.Event, 256)
	eng, err := runtime.New(
		store,
		singleChunkStreamClient{delta: "TAIL-ONCE"},
		tools.NewRegistry(),
		runtime.Config{
			Model: "gpt-5",
			OnEvent: func(evt runtime.Event) {
				runtimeEvents <- evt
			},
		},
	)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	out := &bytes.Buffer{}
	model := newProjectedTestUIModel(newUIRuntimeClient(eng), projectRuntimeEventChannel(runtimeEvents, nil, nil), closedAskEvents())

	program := tea.NewProgram(
		model,
		tea.WithInput(strings.NewReader("")),
		tea.WithOutput(out),
		tea.WithoutSignals(),
	)
	done := make(chan error, 1)
	go func() {
		_, runErr := program.Run()
		done <- runErr
	}()

	time.Sleep(40 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 32})
	submitDone := make(chan error, 1)
	go func() {
		_, err := eng.SubmitUserMessage(context.Background(), "trigger")
		submitDone <- err
	}()
	waitForTestCondition(t, 2*time.Second, "noop final to clear ongoing state", func() bool {
		if strings.TrimSpace(model.view.OngoingStreamingText()) != "" {
			return false
		}
		if model.sawAssistantDelta {
			return false
		}
		for _, entry := range eng.ChatSnapshot().Entries {
			if strings.Contains(entry.Text, "NO_OP") {
				return false
			}
		}
		return true
	})
	waitForSubmitResult(t, 2*time.Second, submitDone)
	program.Quit()

	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("program run failed: %v", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}

	plain := xansi.Strip(out.String())
	if strings.Contains(plain, "TAIL-ONCETAIL-ONCE") {
		t.Fatalf("expected no duplicated tail token blink pattern, got %q", plain)
	}
}

func TestNativeFinalizeSuppressesLateAsyncDeltaArtifacts(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	runtimeEvents := make(chan runtime.Event, 256)
	eng, err := runtime.New(
		store,
		asyncLateDeltaStreamClient{initial: "FINAL-CONTENT", late: "LATE-BLINK", delay: 25 * time.Millisecond},
		tools.NewRegistry(),
		runtime.Config{
			Model: "gpt-5",
			OnEvent: func(evt runtime.Event) {
				runtimeEvents <- evt
			},
		},
	)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	out := &bytes.Buffer{}
	model := newProjectedTestUIModel(newUIRuntimeClient(eng), projectRuntimeEventChannel(runtimeEvents, nil, nil), closedAskEvents())

	program := tea.NewProgram(
		model,
		tea.WithInput(strings.NewReader("")),
		tea.WithOutput(out),
		tea.WithoutSignals(),
	)
	done := make(chan error, 1)
	go func() {
		_, runErr := program.Run()
		done <- runErr
	}()

	time.Sleep(40 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 32})
	submitDone := make(chan error, 1)
	go func() {
		_, err := eng.SubmitUserMessage(context.Background(), "trigger")
		submitDone <- err
	}()
	time.Sleep(260 * time.Millisecond)
	waitForSubmitResult(t, 2*time.Second, submitDone)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if strings.TrimSpace(model.view.OngoingStreamingText()) == "" && !model.sawAssistantDelta {
			break
		}
		if time.Now().After(deadline) {
			snapshot := eng.ChatSnapshot()
			t.Fatalf("timed out waiting for final commit to clear ongoing state output=%q flush_seq=%d flushed_seq=%d pending_flushes=%d runtime_transcript=%+v ui_transcript=%+v native_projection=%+v native_rendered_projection=%+v native_snapshot=%q ongoing=%q", normalizedOutput(out.String()), model.nativeFlushSequence, model.nativeFlushedSequence, len(model.nativePendingFlushes), snapshot.Entries, model.transcriptEntries, model.nativeProjection, model.nativeRenderedProjection, model.nativeRenderedSnapshot, stripANSIAndTrimRight(model.view.OngoingSnapshot()))
		}
		time.Sleep(10 * time.Millisecond)
	}
	program.Quit()

	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("program run failed: %v", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}

	normalized := normalizedOutput(out.String())
	if !strings.Contains(normalized, "FINAL-CONTENT") {
		snapshot := eng.ChatSnapshot()
		t.Fatalf("expected final content in output, got output=%q flush_seq=%d flushed_seq=%d pending_flushes=%d runtime_transcript=%+v ui_transcript=%+v native_projection=%+v native_rendered_projection=%+v native_snapshot=%q ongoing=%q", normalized, model.nativeFlushSequence, model.nativeFlushedSequence, len(model.nativePendingFlushes), snapshot.Entries, model.transcriptEntries, model.nativeProjection, model.nativeRenderedProjection, model.nativeRenderedSnapshot, stripANSIAndTrimRight(model.view.OngoingSnapshot()))
	}
	if strings.Contains(normalized, "LATE-BLINK") {
		t.Fatalf("expected late async delta to be suppressed after finalize, got %q", normalized)
	}
	if strings.TrimSpace(model.view.OngoingStreamingText()) != "" {
		t.Fatalf("expected live streaming buffer cleared after commit, got %q", model.view.OngoingStreamingText())
	}
	if model.sawAssistantDelta {
		t.Fatal("expected sawAssistantDelta cleared after finalize commit")
	}
}

func TestNativeDeferredFinalWithQueuedInjectionKeepsAssistantBeforeQueuedUserInScrollback(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	runtimeEvents := make(chan runtime.Event, 256)
	eng, err := runtime.New(
		store,
		&deferredFinalQueuedInjectionStreamClient{delay: 120 * time.Millisecond},
		tools.NewRegistry(),
		runtime.Config{
			Model: "gpt-5",
			Reviewer: runtime.ReviewerConfig{
				Frequency:     "all",
				Model:         "gpt-5",
				ThinkingLevel: "low",
				Client:        reviewerNoSuggestionsClient{},
			},
			OnEvent: func(evt runtime.Event) {
				runtimeEvents <- evt
			},
		},
	)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	eng.QueueUserMessage("steer now")

	out := &bytes.Buffer{}
	model := newProjectedTestUIModel(newUIRuntimeClient(eng), projectRuntimeEventChannel(runtimeEvents, nil, nil), closedAskEvents())

	program := tea.NewProgram(
		model,
		tea.WithInput(strings.NewReader("")),
		tea.WithOutput(out),
		tea.WithoutSignals(),
	)
	done := make(chan error, 1)
	go func() {
		_, runErr := program.Run()
		done <- runErr
	}()

	time.Sleep(40 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 32})
	submitDone := make(chan error, 1)
	go func() {
		_, err := eng.SubmitUserMessage(context.Background(), "run task")
		submitDone <- err
	}()

	waitForTestCondition(t, 2*time.Second, "live deferred final delta visible", func() bool {
		return strings.Contains(model.view.OngoingStreamingText(), "foreground done")
	})

	waitForSubmitResult(t, 2*time.Second, submitDone)
	waitForTestCondition(t, 2*time.Second, "deferred final committed before queued user flush in output", func() bool {
		if strings.TrimSpace(model.view.OngoingStreamingText()) != "" || model.sawAssistantDelta {
			return false
		}
		return containsInOrder(normalizedOutput(out.String()), "run task", "foreground done", "steer now")
	})

	program.Quit()
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("program run failed: %v", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}

	normalized := normalizedOutput(out.String())
	if !containsInOrder(normalized, "run task", "foreground done", "steer now") {
		t.Fatalf("expected deferred final before queued injected user in ongoing scrollback, got %q", normalized)
	}
	if strings.TrimSpace(model.view.OngoingStreamingText()) != "" {
		t.Fatalf("expected live streaming buffer cleared after deferred final commit, got %q", model.view.OngoingStreamingText())
	}
	if model.sawAssistantDelta {
		t.Fatal("expected sawAssistantDelta cleared after deferred final commit")
	}
}

func TestNativeQueuedSteerDuringBlockingToolAppearsInScrollback(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	runtimeEvents := make(chan runtime.Event, 256)
	blockingTool := &blockingShellTool{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	eng, err := runtime.New(
		store,
		&queuedSteerDuringBlockingToolClient{},
		tools.NewRegistry(blockingTool),
		runtime.Config{
			Model: "gpt-5",
			OnEvent: func(evt runtime.Event) {
				runtimeEvents <- evt
			},
		},
	)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	out := &bytes.Buffer{}
	model := newProjectedTestUIModel(newUIRuntimeClient(eng), projectRuntimeEventChannel(runtimeEvents, nil, nil), closedAskEvents())

	program := tea.NewProgram(
		model,
		tea.WithInput(strings.NewReader("")),
		tea.WithOutput(out),
		tea.WithoutSignals(),
	)
	done := make(chan error, 1)
	go func() {
		_, runErr := program.Run()
		done <- runErr
	}()

	time.Sleep(40 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 32})
	submitDone := make(chan error, 1)
	go func() {
		_, err := eng.SubmitUserMessage(context.Background(), "run task")
		submitDone <- err
	}()

	select {
	case <-blockingTool.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for blocking tool to start")
	}
	eng.QueueUserMessage("steer now")
	close(blockingTool.release)

	waitForSubmitResult(t, 2*time.Second, submitDone)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if containsInOrder(normalizedOutput(out.String()), "run task", "after steer") {
			break
		}
		if time.Now().After(deadline) {
			snapshot := eng.ChatSnapshot()
			t.Fatalf("timed out waiting for follow-up assistant resolves after blocking tool output=%q flush_seq=%d flushed_seq=%d pending_flushes=%d runtime_transcript=%+v ui_transcript=%+v native_projection=%+v native_rendered_projection=%+v native_snapshot=%q ongoing=%q", normalizedOutput(out.String()), model.nativeFlushSequence, model.nativeFlushedSequence, len(model.nativePendingFlushes), snapshot.Entries, model.transcriptEntries, model.nativeProjection, model.nativeRenderedProjection, model.nativeRenderedSnapshot, stripANSIAndTrimRight(model.view.OngoingSnapshot()))
		}
		time.Sleep(10 * time.Millisecond)
	}

	snapshot := eng.ChatSnapshot()
	hasQueuedUser := false
	for _, entry := range snapshot.Entries {
		if entry.Role == string(llm.RoleUser) && entry.Text == "steer now" {
			hasQueuedUser = true
			break
		}
	}
	if !hasQueuedUser {
		t.Fatalf("expected runtime transcript to contain queued steer, got %+v", snapshot.Entries)
	}
	if normalized := normalizedOutput(out.String()); !containsInOrder(normalized, "run task", "steer now", "after steer") {
		t.Fatalf("expected queued steer visible in ongoing scrollback, got run=%d steer=%d after=%d flush_seq=%d flushed_seq=%d pending_flushes=%d output=%q runtime_transcript=%+v ui_transcript=%+v native_snapshot=%q ongoing=%q", strings.Index(normalized, "run task"), strings.Index(normalized, "steer now"), strings.Index(normalized, "after steer"), model.nativeFlushSequence, model.nativeFlushedSequence, len(model.nativePendingFlushes), normalized, snapshot.Entries, model.transcriptEntries, model.nativeRenderedSnapshot, stripANSIAndTrimRight(model.view.OngoingSnapshot()))
	}

	program.Quit()
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("program run failed: %v", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}

	if normalized := normalizedOutput(out.String()); !containsInOrder(normalized, "run task", "steer now", "after steer") {
		t.Fatalf("expected queued steer visible in ongoing scrollback, got %q", normalized)
	}
}

func TestNativeNoopFinalNeverAppearsOnScreen(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	runtimeEvents := make(chan runtime.Event, 256)
	eng, err := runtime.New(
		store,
		noopFinalStreamClient{},
		tools.NewRegistry(),
		runtime.Config{
			Model: "gpt-5",
			OnEvent: func(evt runtime.Event) {
				runtimeEvents <- evt
			},
		},
	)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	out := &bytes.Buffer{}
	model := newProjectedTestUIModel(newUIRuntimeClient(eng), projectRuntimeEventChannel(runtimeEvents, nil, nil), closedAskEvents())

	program := tea.NewProgram(
		model,
		tea.WithInput(strings.NewReader("")),
		tea.WithOutput(out),
		tea.WithoutSignals(),
	)
	done := make(chan error, 1)
	go func() {
		_, runErr := program.Run()
		done <- runErr
	}()

	time.Sleep(40 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 32})
	submitDone := make(chan error, 1)
	go func() {
		_, err := eng.SubmitUserMessage(context.Background(), "trigger")
		submitDone <- err
	}()
	waitForTestCondition(t, 2*time.Second, "noop final to clear ongoing state", func() bool {
		if strings.TrimSpace(model.view.OngoingStreamingText()) != "" {
			return false
		}
		if model.sawAssistantDelta {
			return false
		}
		for _, entry := range eng.ChatSnapshot().Entries {
			if strings.Contains(entry.Text, "NO_OP") {
				return false
			}
		}
		return true
	})
	waitForSubmitResult(t, 2*time.Second, submitDone)
	program.Quit()

	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("program run failed: %v", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}

	plain := xansi.Strip(out.String())
	if strings.Contains(plain, "NO_OP") {
		t.Fatalf("expected NO_OP to stay invisible in native ongoing output, got %q", plain)
	}
	if strings.TrimSpace(model.view.OngoingStreamingText()) != "" {
		t.Fatalf("expected live streaming buffer cleared after noop final, got %q", model.view.OngoingStreamingText())
	}
	if model.sawAssistantDelta {
		t.Fatal("expected sawAssistantDelta cleared after noop final")
	}
	for _, entry := range eng.ChatSnapshot().Entries {
		if strings.Contains(entry.Text, "NO_OP") {
			t.Fatalf("expected NO_OP to stay out of transcript entries, got %+v", eng.ChatSnapshot().Entries)
		}
	}
}

func TestNativeSubmitAndFlushDoesNotDuplicateStatusLines(t *testing.T) {
	out := &bytes.Buffer{}
	model := newProjectedTestUIModel(nil, closedProjectedRuntimeEvents(), closedAskEvents())

	program := tea.NewProgram(
		model,
		tea.WithInput(strings.NewReader("")),
		tea.WithOutput(out),
		tea.WithoutSignals(),
	)

	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()

	time.Sleep(40 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 32})
	program.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line one")})
	program.Send(tea.KeyMsg{Type: tea.KeyCtrlJ})
	program.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line two")})
	program.Send(tea.KeyMsg{Type: tea.KeyEnter})
	time.Sleep(50 * time.Millisecond)
	program.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("next input")})
	time.Sleep(20 * time.Millisecond)
	program.Send(nativeHistoryFlushMsg{Text: "post-submit replay\n"})
	time.Sleep(20 * time.Millisecond)
	program.Send(tea.KeyMsg{Type: tea.KeyCtrlC})

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}

	raw := out.String()
	normalized := normalizedOutput(raw)
	plain := xansi.Strip(raw)
	if strings.Count(normalized, "post-submit replay") != 1 {
		t.Fatalf("expected post-submit flush exactly once, got %d", strings.Count(normalized, "post-submit replay"))
	}
	for _, line := range strings.Split(plain, "\n") {
		if strings.Count(line, "ongoing | ") > 1 {
			t.Fatalf("expected no duplicated status segment in a single rendered line, got %q", line)
		}
	}
	if occurrences := strings.Count(plain, "ongoing | "); occurrences > 16 {
		t.Fatalf("expected bounded status redraw count after submit+flush, got %d", occurrences)
	}
}

func TestNativeReplayOutputContainsMarkdownStyling(t *testing.T) {
	out := &bytes.Buffer{}
	model := newProjectedTestUIModel(
		nil,
		closedProjectedRuntimeEvents(),
		closedAskEvents(),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "**bold** and `code`"}}),
	)
	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 32})
	time.Sleep(40 * time.Millisecond)
	program.Quit()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}
	raw := out.String()
	if !strings.Contains(raw, "\x1b[") {
		t.Fatalf("expected ansi styling sequences in native replay output, got %q", raw)
	}
	if strings.Contains(raw, "**bold**") {
		t.Fatalf("expected markdown transformed in replay output, got literal markdown: %q", raw)
	}
	plain := normalizedOutput(raw)
	if !strings.Contains(plain, "bold") || !strings.Contains(plain, "code") {
		t.Fatalf("expected styled replay to include content, got %q", plain)
	}
}

func TestNativeProgramKeepsPendingToolTailLiveOnlyUntilCompletion(t *testing.T) {
	out := &bytes.Buffer{}
	model := newProjectedTestUIModel(
		nil,
		closedProjectedRuntimeEvents(),
		closedAskEvents(),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "prompt once"}}),
	)
	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()

	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 30})
	time.Sleep(40 * time.Millisecond)
	baselineRaw := out.String()
	baselineNormalized := normalizedOutput(baselineRaw)
	if strings.Count(baselineNormalized, "prompt once") != 1 {
		t.Fatalf("expected prompt once in baseline startup output, got %q", baselineNormalized)
	}

	call := tui.TranscriptEntry{
		Role:       "tool_call",
		Text:       "pwd",
		ToolCallID: "call_1",
		ToolCall:   &transcript.ToolCallMeta{ToolName: "shell", IsShell: true, Command: "pwd"},
	}
	model.transcriptEntries = append(model.transcriptEntries, call)
	model.forwardToView(tui.SetConversationMsg{Entries: model.transcriptEntries})
	model.syncViewport()
	if cmd := model.syncNativeHistoryFromTranscript(); cmd != nil {
		t.Fatalf("expected pending tool call not to flush committed history, got %T", cmd())
	}
	program.Send(spinnerTickMsg{})
	time.Sleep(40 * time.Millisecond)
	pendingDelta := out.String()[len(baselineRaw):]
	pendingNormalized := normalizedOutput(pendingDelta)
	if strings.Contains(pendingNormalized, "prompt once") {
		t.Fatalf("expected no prompt replay while tool call is pending, got %q", pendingNormalized)
	}
	pendingPlain := xansi.Strip(pendingDelta)
	hasDotFrame := false
	for _, frame := range pendingToolSpinner.Frames {
		if strings.Contains(pendingPlain, strings.TrimSpace(frame)+" pwd") {
			hasDotFrame = true
			break
		}
	}
	if !hasDotFrame {
		t.Fatalf("expected pending tool call visible in live region output, got %q", xansi.Strip(pendingDelta))
	}

	result := tui.TranscriptEntry{Role: "tool_result_ok", Text: "/tmp", ToolCallID: "call_1"}
	model.transcriptEntries = append(model.transcriptEntries, result)
	model.forwardToView(tui.SetConversationMsg{Entries: model.transcriptEntries})
	model.syncViewport()
	cmd := model.syncNativeHistoryFromTranscript()
	if cmd == nil {
		t.Fatal("expected finalized tool block flush")
	}
	program.Send(cmd())
	time.Sleep(40 * time.Millisecond)
	finalDelta := out.String()[len(baselineRaw)+len(pendingDelta):]
	finalNormalized := normalizedOutput(finalDelta)
	if strings.Contains(finalNormalized, "prompt once") {
		t.Fatalf("expected finalized flush without prompt replay, got %q", finalNormalized)
	}
	if strings.Count(finalNormalized, "pwd") != 1 {
		t.Fatalf("expected finalized tool call exactly once in append output, got %q", finalNormalized)
	}
	if strings.Contains(finalNormalized, "/tmp") {
		t.Fatalf("did not expect native ongoing scrollback to append shell output inline, got %q", finalNormalized)
	}
	assertContainsColoredShellSymbol(t, finalDelta, "dark success", transcriptToolSuccessColorHex("dark"))
	assertNoColoredShellSymbol(t, finalDelta, "dark pending", transcriptToolPendingColorHex("dark"))

	program.Quit()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}
}

func TestNativeProgramRendersMixedRuntimeEventsFromChannelInRealtime(t *testing.T) {
	out := &bytes.Buffer{}
	runtimeEvents := make(chan clientui.Event, 16)
	model := newProjectedTestUIModel(
		nil,
		runtimeEvents,
		closedAskEvents(),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "seed"}}),
	)
	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()

	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 30})
	waitForTestCondition(t, 2*time.Second, "startup replay", func() bool {
		return strings.Contains(normalizedOutput(out.String()), "seed")
	})

	callMeta := transcript.ToolCallMeta{ToolName: "shell", Command: "pwd", CompactText: "pwd", IsShell: true}
	runtimeEvents <- projectRuntimeEvent(runtime.Event{Kind: runtime.EventRunStateChanged, RunState: &runtime.RunState{Busy: true}})
	runtimeEvents <- projectRuntimeEvent(runtime.Event{Kind: runtime.EventUserMessageFlushed, StepID: "step-1", UserMessage: "say hi"})
	runtimeEvents <- projectRuntimeEvent(runtime.Event{Kind: runtime.EventReviewerCompleted, StepID: "step-1", Reviewer: &runtime.ReviewerStatus{Outcome: "applied", SuggestionsCount: 2}})
	runtimeEvents <- projectRuntimeEvent(runtime.Event{Kind: runtime.EventBackgroundUpdated, StepID: "step-1", Background: &runtime.BackgroundShellEvent{Type: "completed", ID: "1000", State: "completed", NoticeText: "Background shell 1000 completed.\nOutput:\nhello", CompactText: "Background shell 1000 completed"}})
	runtimeEvents <- projectRuntimeEvent(runtime.Event{Kind: runtime.EventToolCallStarted, StepID: "step-1", ToolCall: &llm.ToolCall{ID: "call_1", Name: string(tools.ToolShell), Presentation: toolcodec.EncodeToolCallMeta(callMeta)}})

	lastTranscript := ""
	lastNormalized := ""
	firstBatchDeadline := time.Now().Add(2 * time.Second)
	firstBatchReady := false
	for time.Now().Before(firstBatchDeadline) {
		transcriptText := strings.Builder{}
		for _, entry := range model.transcriptEntries {
			transcriptText.WriteString(entry.Text)
			transcriptText.WriteString("\n")
			if strings.TrimSpace(entry.OngoingText) != "" {
				transcriptText.WriteString(entry.OngoingText)
				transcriptText.WriteString("\n")
			}
		}
		lastTranscript = transcriptText.String()
		if !containsInOrder(lastTranscript, "say hi", "Supervisor ran", "Background shell 1000 completed") {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		lastNormalized = normalizedOutput(out.String())
		if strings.Contains(lastNormalized, "pwd") && strings.Contains(strings.ToLower(lastNormalized), "background shell 1000 completed") {
			firstBatchReady = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !firstBatchReady {
		lastNormalized = normalizedOutput(out.String())
		t.Fatalf(
			"expected mixed realtime terminal order after first batch, transcript=%q output=%q committed=%q nativeProjection=%q nativeRendered=%q",
			lastTranscript,
			lastNormalized,
			model.view.CommittedOngoingProjection().Render(tui.TranscriptDivider),
			model.nativeProjection.Render(tui.TranscriptDivider),
			model.nativeRenderedProjection.Render(tui.TranscriptDivider),
		)
	}

	runtimeEvents <- projectRuntimeEvent(runtime.Event{Kind: runtime.EventToolCallCompleted, StepID: "step-1", ToolResult: &tools.Result{CallID: "call_1", Name: tools.ToolShell, Output: []byte("/tmp")}})
	runtimeEvents <- projectRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantMessage, StepID: "step-1", Message: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal}})
	runtimeEvents <- projectRuntimeEvent(runtime.Event{Kind: runtime.EventRunStateChanged, RunState: &runtime.RunState{Busy: false}})

	waitForTestCondition(t, 2*time.Second, "assistant completion after mixed realtime events", func() bool {
		normalized := normalizedOutput(out.String())
		return containsInOrder(normalized, "say hi", "Supervisor ran", "Background shell 1000 completed", "pwd", "done")
	})

	program.Quit()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}
	transcriptText := strings.Builder{}
	for _, entry := range model.transcriptEntries {
		transcriptText.WriteString(entry.Text)
		transcriptText.WriteString("\n")
		if strings.TrimSpace(entry.OngoingText) != "" {
			transcriptText.WriteString(entry.OngoingText)
			transcriptText.WriteString("\n")
		}
	}
	if !containsInOrder(transcriptText.String(), "say hi", "Supervisor ran", "Background shell 1000 completed", "pwd", "done") {
		t.Fatalf("expected mixed runtime event transcript sequence in projected transcript state, got %q", transcriptText.String())
	}
	if normalized := normalizedOutput(out.String()); !containsInOrder(normalized, "seed", "say hi", "Supervisor ran", "Background shell 1000 completed", "pwd", "done") {
		t.Fatalf("expected mixed runtime event terminal sequence, got %q", normalized)
	}
}

func TestNativeProgramDoesNotDuplicateSupervisorFollowUpAfterHydration(t *testing.T) {
	out := &bytes.Buffer{}
	runtimeEvents := make(chan clientui.Event, 8)
	client := &staleTranscriptRuntimeClient{}
	client.sessionView = clientui.RuntimeSessionView{SessionID: "session-1"}
	client.transcript = clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     1,
		TotalEntries: 1,
		Entries: []clientui.ChatEntry{{
			Role:  "assistant",
			Text:  "seed",
			Phase: string(llm.MessagePhaseFinal),
		}},
	}
	client.page = clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     3,
		TotalEntries: 3,
		Entries: []clientui.ChatEntry{
			{Role: "assistant", Text: "seed", Phase: string(llm.MessagePhaseFinal)},
			{Role: "assistant", Text: "follow-up final unique", Phase: string(llm.MessagePhaseFinal)},
			{Role: "reviewer_status", Text: "Supervisor ran: 2 suggestions, applied."},
		},
	}
	model := newProjectedTestUIModel(client, runtimeEvents, closedAskEvents())
	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()

	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 30})
	waitForTestCondition(t, 2*time.Second, "startup replay", func() bool {
		return strings.Contains(normalizedOutput(out.String()), "seed")
	})
	baselineLoadCalls := client.LoadCalls()

	runtimeEvents <- clientui.Event{
		Kind:                clientui.EventAssistantMessage,
		StepID:              "step-1",
		TranscriptRevision:  2,
		CommittedEntryCount: 2,
		TranscriptEntries: []clientui.ChatEntry{{
			Role:  "assistant",
			Text:  "follow-up final unique",
			Phase: string(llm.MessagePhaseFinal),
		}},
	}
	runtimeEvents <- clientui.Event{
		Kind:                clientui.EventReviewerCompleted,
		StepID:              "step-1",
		TranscriptRevision:  3,
		CommittedEntryCount: 3,
		TranscriptEntries: []clientui.ChatEntry{{
			Role: "reviewer_status",
			Text: "Supervisor ran: 2 suggestions, applied.",
		}},
	}
	runtimeEvents <- clientui.Event{
		Kind:                clientui.EventConversationUpdated,
		StepID:              "step-1",
		TranscriptRevision:  3,
		CommittedEntryCount: 3,
	}

	waitForTestCondition(t, 2*time.Second, "hydrated supervisor follow-up remains single and ordered", func() bool {
		normalized := normalizedOutput(out.String())
		return client.LoadCalls() > baselineLoadCalls &&
			containsInOrder(normalized, "seed", "follow-up final unique", "Supervisor ran: 2 suggestions, applied.") &&
			strings.Count(normalized, "follow-up final unique") == 1 &&
			strings.Count(normalized, "Supervisor ran: 2 suggestions, applied.") == 1
	})

	program.Quit()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}

	if got := len(model.transcriptEntries); got != 3 {
		t.Fatalf("expected authoritative transcript tail without duplication, got %+v", model.transcriptEntries)
	}
	if got := model.transcriptEntries[1].Text; got != "follow-up final unique" {
		t.Fatalf("expected follow-up assistant before reviewer status, got %+v", model.transcriptEntries)
	}
	if got := model.transcriptEntries[2].Text; got != "Supervisor ran: 2 suggestions, applied." {
		t.Fatalf("expected reviewer status at transcript tail, got %+v", model.transcriptEntries)
	}
	if normalized := normalizedOutput(out.String()); strings.Count(normalized, "follow-up final unique") != 1 || strings.Count(normalized, "Supervisor ran: 2 suggestions, applied.") != 1 {
		t.Fatalf("expected follow-up assistant and reviewer status exactly once in terminal output, got %q", normalized)
	}
}

func TestNativeProgramRendersSingleBackgroundCompletionFromChannelWhileIdle(t *testing.T) {
	out := &bytes.Buffer{}
	runtimeEvents := make(chan clientui.Event, 4)
	model := newProjectedTestUIModel(
		nil,
		runtimeEvents,
		closedAskEvents(),
	)
	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()

	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 30})

	runtimeEvents <- projectRuntimeEvent(runtime.Event{
		Kind: runtime.EventBackgroundUpdated,
		Background: &runtime.BackgroundShellEvent{
			Type:        "completed",
			ID:          "1000",
			State:       "completed",
			NoticeText:  "Background shell 1000 completed.\nOutput:\nhello",
			CompactText: "Background shell 1000 completed",
		},
	})

	waitForTestCondition(t, 2*time.Second, "single background completion projected into transcript state", func() bool {
		return len(model.transcriptEntries) == 1 && strings.Contains(model.transcriptEntries[0].Text, "Background shell 1000 completed")
	})
	waitForTestCondition(t, 2*time.Second, "single background completion rendered into native output", func() bool {
		return strings.Contains(strings.ToLower(normalizedOutput(out.String())), "background shell 1000 completed")
	})

	program.Quit()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}

	if normalized := normalizedOutput(out.String()); !containsInOrder(strings.ToLower(normalized), "background shell 1000 completed") {
		t.Fatalf("expected single background completion visible in terminal output, got %q", normalized)
	}
}

func TestNativeProgramRendersBackgroundCompletionFromEmbeddedRuntimeWhileIdle(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "sk-test")
	registerAppWorkspace(t, workspace)

	server, err := startEmbeddedServer(context.Background(), Options{WorkspaceRoot: workspace}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("start embedded server: %v", err)
	}
	defer func() { _ = server.Close() }()

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(context.Background(), plan, &bytes.Buffer{}, "test background completion while idle")
	if err != nil {
		t.Fatalf("prepare runtime: %v", err)
	}
	defer runtimePlan.Close()

	out := &bytes.Buffer{}
	programCtx, cancelProgram := context.WithCancel(context.Background())
	defer cancelProgram()
	model := newProjectedTestUIModel(
		runtimePlan.Wiring.runtimeClient,
		runtimePlan.Wiring.runtimeEvents,
		runtimePlan.Wiring.askEvents,
	)
	program := tea.NewProgram(model, tea.WithContext(programCtx), tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()

	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 30})

	server.inner.BackgroundRouter().Handle(shelltool.Event{
		Type: shelltool.EventCompleted,
		Snapshot: shelltool.Snapshot{
			ID:             "bg-1000",
			OwnerSessionID: plan.SessionID,
			State:          "completed",
			Command:        "sleep 1; printf done",
			Workdir:        workspace,
			LogPath:        "/tmp/bg-1000.log",
		},
		Preview: "done",
	})

	waitForTestCondition(t, 5*time.Second, "embedded background completion projected into transcript state", func() bool {
		for _, entry := range model.transcriptEntries {
			if strings.Contains(entry.Text, "Background shell bg-1000 completed") {
				return true
			}
		}
		return false
	})
	waitForTestCondition(t, 5*time.Second, "embedded background completion rendered into native output", func() bool {
		return strings.Contains(strings.ToLower(normalizedOutput(out.String())), "background shell bg-1000 completed")
	})

	cancelProgram()
	select {
	case err := <-done:
		if err != nil && !strings.Contains(err.Error(), "context canceled") {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}

	if normalized := normalizedOutput(out.String()); !containsInOrder(strings.ToLower(normalized), "background shell bg-1000 completed") {
		t.Fatalf("expected embedded background completion visible in terminal output, got %q", normalized)
	}
}

func TestNativeProgramRendersBackgroundCompletionFromShellManagerWhileIdle(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "sk-test")
	registerAppWorkspace(t, workspace)

	server, err := startEmbeddedServer(context.Background(), Options{WorkspaceRoot: workspace}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("start embedded server: %v", err)
	}
	defer func() { _ = server.Close() }()

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(context.Background(), plan, &bytes.Buffer{}, "test shell-manager background completion while idle")
	if err != nil {
		t.Fatalf("prepare runtime: %v", err)
	}
	defer runtimePlan.Close()

	manager := server.inner.Background()
	if manager == nil {
		t.Fatal("expected server background manager")
	}
	manager.SetMinimumExecToBgTime(25 * time.Millisecond)

	out := &bytes.Buffer{}
	programCtx, cancelProgram := context.WithCancel(context.Background())
	defer cancelProgram()
	model := newProjectedTestUIModel(
		runtimePlan.Wiring.runtimeClient,
		runtimePlan.Wiring.runtimeEvents,
		runtimePlan.Wiring.askEvents,
	)
	program := tea.NewProgram(model, tea.WithContext(programCtx), tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()

	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 30})

	result, err := manager.Start(context.Background(), shelltool.ExecRequest{
		Command:        []string{"sh", "-c", "sleep 0.05; printf done"},
		DisplayCommand: "bg-notify",
		OwnerSessionID: plan.SessionID,
		OwnerRunID:     "run-1",
		OwnerStepID:    "step-1",
		Workdir:        workspace,
		YieldTime:      25 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start background process: %v", err)
	}
	if !result.Backgrounded {
		t.Fatalf("expected backgrounded process, got %+v", result)
	}
	want := "background shell " + result.SessionID + " completed"

	waitForTestCondition(t, 5*time.Second, "shell-manager background completion projected into transcript state", func() bool {
		for _, entry := range model.transcriptEntries {
			if strings.Contains(strings.ToLower(entry.Text), want) {
				return true
			}
		}
		return false
	})
	waitForTestCondition(t, 5*time.Second, "shell-manager background completion rendered into native output", func() bool {
		return strings.Contains(normalizedOutput(strings.ToLower(out.String())), want)
	})

	cancelProgram()
	select {
	case err := <-done:
		if err != nil && !strings.Contains(err.Error(), "context canceled") {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}

	if normalized := normalizedOutput(strings.ToLower(out.String())); !containsInOrder(normalized, want) {
		t.Fatalf("expected shell-manager background completion visible in terminal output, got %q", normalized)
	}
}

func TestNativeProgramUserFlushDoesNotTriggerTranscriptSyncThatDropsCommentary(t *testing.T) {
	out := &bytes.Buffer{}
	runtimeEvents := make(chan clientui.Event, 16)
	client := &staleTranscriptRuntimeClient{
		page: clientui.TranscriptPage{
			SessionID: "session-1",
			Entries:   []clientui.ChatEntry{{Role: "assistant", Text: "seed", Phase: string(llm.MessagePhaseFinal)}},
		},
	}
	model := newProjectedTestUIModel(
		client,
		runtimeEvents,
		closedAskEvents(),
	)
	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()

	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 30})
	waitForTestCondition(t, 2*time.Second, "startup replay", func() bool {
		return strings.Contains(normalizedOutput(out.String()), "seed")
	})
	baselineLoadCalls := client.LoadCalls()

	callMeta := transcript.ToolCallMeta{ToolName: "shell", Command: "pwd", CompactText: "pwd", IsShell: true}
	runtimeEvents <- clientui.Event{
		Kind:              clientui.EventUserMessageFlushed,
		StepID:            "step-1",
		UserMessage:       "say hi",
		TranscriptEntries: []clientui.ChatEntry{{Role: "user", Text: "say hi"}},
	}
	runtimeEvents <- clientui.Event{Kind: clientui.EventAssistantDelta, StepID: "step-1", AssistantDelta: "working"}

	waitForTestCondition(t, 2*time.Second, "live commentary after user flush", func() bool {
		normalized := normalizedOutput(out.String())
		return containsInOrder(normalized, "seed", "say hi", "working")
	})
	if currentLoadCalls := client.LoadCalls(); currentLoadCalls != baselineLoadCalls {
		t.Fatalf("expected flushed user message to avoid extra transcript syncs before commentary, baseline=%d current=%d", baselineLoadCalls, currentLoadCalls)
	}

	runtimeEvents <- clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-1", TranscriptEntries: []clientui.ChatEntry{{Role: "tool_call", Text: "pwd", ToolCallID: "call_1", ToolCall: transcriptToolCallMetaClient(&callMeta)}}}
	runtimeEvents <- clientui.Event{Kind: clientui.EventToolCallCompleted, StepID: "step-1", TranscriptEntries: []clientui.ChatEntry{{Role: "tool_result_ok", Text: "$ pwd\n/tmp", ToolCallID: "call_1"}}}
	runtimeEvents <- clientui.Event{Kind: clientui.EventAssistantMessage, StepID: "step-1", TranscriptEntries: []clientui.ChatEntry{{Role: "assistant", Text: "done", Phase: string(llm.MessagePhaseFinal)}}}

	waitForTestCondition(t, 2*time.Second, "tool and final after user flush", func() bool {
		normalized := normalizedOutput(out.String())
		return containsInOrder(normalized, "seed", "say hi", "pwd", "done")
	})
	if currentLoadCalls := client.LoadCalls(); currentLoadCalls != baselineLoadCalls {
		t.Fatalf("expected flushed user message to avoid extra transcript syncs, baseline=%d current=%d", baselineLoadCalls, currentLoadCalls)
	}

	program.Quit()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}
	if normalized := normalizedOutput(out.String()); !containsInOrder(normalized, "seed", "say hi", "pwd", "done") {
		t.Fatalf("expected realtime terminal sequence after user flush, got %q", normalized)
	}
}

func TestNativeProgramConversationRefreshHydratesCommittedTranscriptWithoutReplayDuplication(t *testing.T) {
	out := &bytes.Buffer{}
	runtimeEvents := make(chan clientui.Event, 8)
	client := &startupTranscriptRuntimeClient{
		view: clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{SessionID: "session-1", SessionName: "incident triage"}},
		page: clientui.TranscriptPage{
			SessionID:    "session-1",
			SessionName:  "incident triage",
			TotalEntries: 1,
			Entries: []clientui.ChatEntry{{
				Role:  "assistant",
				Text:  "already visible",
				Phase: string(llm.MessagePhaseFinal),
			}},
		},
	}
	model := newProjectedTestUIModel(client, runtimeEvents, closedAskEvents())
	model.startupCmds = nil
	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()

	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 30})
	waitForTestCondition(t, 2*time.Second, "startup committed transcript", func() bool {
		return strings.Contains(normalizedOutput(out.String()), "already visible")
	})
	baselineLen := out.Len()
	client.page = clientui.TranscriptPage{
		SessionID:    "session-1",
		SessionName:  "incident triage",
		TotalEntries: 2,
		Entries: []clientui.ChatEntry{
			{Role: "assistant", Text: "already visible", Phase: string(llm.MessagePhaseFinal)},
			{Role: "assistant", Text: "restored after reconnect", Phase: string(llm.MessagePhaseFinal)},
		},
	}

	runtimeEvents <- clientui.Event{Kind: clientui.EventConversationUpdated}
	waitForTestCondition(t, 2*time.Second, "recovered committed transcript", func() bool {
		return strings.Contains(normalizedOutput(out.String()), "restored after reconnect")
	})

	program.Quit()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}

	raw := out.String()
	if strings.Contains(raw[baselineLen:], "\x1b[2J") {
		t.Fatalf("expected reconnect hydration to avoid clearing the session, got %q", raw[baselineLen:])
	}
	normalized := normalizedOutput(raw)
	if strings.Count(normalized, "already visible") != 1 {
		t.Fatalf("expected previously visible committed entry exactly once after hydration, got %d in %q", strings.Count(normalized, "already visible"), normalized)
	}
	if strings.Count(normalized, "restored after reconnect") != 1 {
		t.Fatalf("expected recovered committed entry exactly once, got %d in %q", strings.Count(normalized, "restored after reconnect"), normalized)
	}
	if model.sessionName != "incident triage" {
		t.Fatalf("expected session name preserved across hydration, got %q", model.sessionName)
	}
	if len(client.loadRequests) != 1 {
		t.Fatalf("transcript load calls = %d, want 1", len(client.loadRequests))
	}
}

func TestNativeStreamingInterleavedWithStatusRedrawStaysCoherent(t *testing.T) {
	out := &bytes.Buffer{}
	model := newProjectedTestUIModel(
		nil,
		closedProjectedRuntimeEvents(),
		closedAskEvents(),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "prompt once"}}),
	)
	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 32})
	program.Send(projectedRuntimeEventMsg(runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: "line1\n"}))
	program.Send(spinnerTickMsg{})
	program.Send(projectedRuntimeEventMsg(runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: "line2\n"}))
	program.Send(spinnerTickMsg{})
	time.Sleep(40 * time.Millisecond)
	program.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}
	raw := out.String()
	plain := xansi.Strip(raw)
	if strings.Count(normalizedOutput(raw), "prompt once") != 1 {
		t.Fatalf("expected prompt once in output, got %d", strings.Count(normalizedOutput(raw), "prompt once"))
	}
	line1Count := strings.Count(normalizedOutput(raw), "line1")
	line2Count := strings.Count(normalizedOutput(raw), "line2")
	if line1Count < 1 || line2Count < 1 || line1Count > 2 || line2Count > 2 {
		t.Fatalf("expected bounded streamed line visibility under redraw pressure, got line1=%d line2=%d output=%q", line1Count, line2Count, normalizedOutput(raw))
	}
	normalized := normalizedOutput(raw)
	if strings.Index(normalized, "line1") > strings.Index(normalized, "line2") {
		t.Fatalf("expected streamed line order preserved, got %q", normalized)
	}
	for _, line := range strings.Split(plain, "\n") {
		if strings.Count(line, "ongoing | ") > 1 {
			t.Fatalf("expected no duplicated status segment in a single rendered line, got %q", line)
		}
	}
}

func TestQueuedFollowUpWaitsForFinalTranscriptCatchUpBeforeNativeScrollbackAppend(t *testing.T) {
	out := &bytes.Buffer{}
	client := &gatedRefreshRuntimeClient{
		runtimeControlFakeClient: runtimeControlFakeClient{
			sessionView: clientui.RuntimeSessionView{SessionID: "session-1"},
		},
		page: clientui.TranscriptPage{
			SessionID:    "session-1",
			TotalEntries: 1,
			Entries: []clientui.ChatEntry{{
				Role:  "assistant",
				Text:  "final answer",
				Phase: string(llm.MessagePhaseFinal),
			}},
		},
		refreshStarted: make(chan struct{}),
		releaseRefresh: make(chan struct{}),
	}
	model := newProjectedTestUIModel(client, closedProjectedRuntimeEvents(), closedAskEvents())
	model.startupCmds = nil
	model.busy = true
	model.activity = uiActivityRunning
	model.queued = []string{"follow up"}
	model.sawAssistantDelta = true
	model.forwardToView(tui.SetConversationMsg{Ongoing: "working"})

	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()

	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 30})
	waitForTestCondition(t, 2*time.Second, "live assistant streaming visible", func() bool {
		return strings.Contains(normalizedOutput(out.String()), "working")
	})

	program.Send(submitDoneMsg{message: "ignored by runtime-backed flow"})
	select {
	case <-client.refreshStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for transcript catch-up refresh to start")
	}
	time.Sleep(80 * time.Millisecond)
	if client.shouldCompactText != "" || client.submitText != "" {
		t.Fatalf("expected queued follow-up to wait for transcript catch-up, compact=%q submit=%q", client.shouldCompactText, client.submitText)
	}

	close(client.releaseRefresh)
	waitForTestCondition(t, 2*time.Second, "final answer committed before queued follow-up starts", func() bool {
		normalized := normalizedOutput(out.String())
		return strings.Contains(normalized, "final answer") && client.shouldCompactText == "follow up"
	})
	if strings.TrimSpace(model.view.OngoingStreamingText()) != "" {
		t.Fatalf("expected live streaming buffer cleared after final catch-up, got %q", model.view.OngoingStreamingText())
	}

	program.Send(runtimeEventMsg{event: clientui.Event{
		Kind:        clientui.EventUserMessageFlushed,
		StepID:      "step-2",
		UserMessage: "follow up",
		TranscriptEntries: []clientui.ChatEntry{{
			Role: "user",
			Text: "follow up",
		}},
	}})
	waitForTestCondition(t, 2*time.Second, "queued follow-up appended after final answer", func() bool {
		return containsInOrder(normalizedOutput(out.String()), "final answer", "follow up")
	})

	program.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}

	normalized := normalizedOutput(out.String())
	if !containsInOrder(normalized, "final answer", "follow up") {
		t.Fatalf("expected final answer before queued follow-up in scrollback, got %q", normalized)
	}
	if strings.Count(normalized, "final answer") != 1 {
		t.Fatalf("expected final answer appended exactly once, got %d in %q", strings.Count(normalized, "final answer"), normalized)
	}
}

func TestRuntimeHydrationRewriteRecoversOngoingScrollbackAndLaterAssistantAppend(t *testing.T) {
	out := &bytes.Buffer{}
	runtimeEvents := make(chan clientui.Event, 16)
	client := &runtimeControlFakeClient{
		sessionView: clientui.RuntimeSessionView{SessionID: "session-1"},
		transcript: clientui.TranscriptPage{
			SessionID:    "session-1",
			Revision:     1,
			TotalEntries: 2,
			Entries: []clientui.ChatEntry{
				{Role: "user", Text: "commit/push"},
				{Role: "assistant", Text: "before"},
			},
		},
	}
	model := newProjectedTestUIModel(
		client,
		runtimeEvents,
		closedAskEvents(),
	)
	model.startupCmds = nil
	model.runtimeTranscriptBusy = true
	model.runtimeTranscriptToken = 1
	model.busy = true
	model.activity = uiActivityRunning
	model.sawAssistantDelta = true
	model.forwardToView(tui.SetConversationMsg{Entries: model.transcriptEntries, Ongoing: "working"})

	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()

	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 30})
	waitForTestCondition(t, 2*time.Second, "initial ongoing output visible", func() bool {
		normalized := normalizedOutput(out.String())
		return strings.Contains(normalized, "before") && strings.Contains(normalized, "working")
	})

	program.Send(runtimeTranscriptRefreshedMsg{token: 1, transcript: clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     2,
		TotalEntries: 2,
		Entries: []clientui.ChatEntry{
			{Role: "user", Text: "commit/push"},
			{Role: "assistant", Text: "after"},
		},
	}})
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(normalizedOutput(out.String()), "after") {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for authoritative rewrite appended to ongoing scrollback output=%q transcript=%+v native_projection=%+v native_rendered_projection=%+v native_snapshot=%q busy=%t runtime_busy=%t token=%d ongoing=%q", normalizedOutput(out.String()), model.transcriptEntries, model.nativeProjection, model.nativeRenderedProjection, model.nativeRenderedSnapshot, model.busy, model.runtimeTranscriptBusy, model.runtimeTranscriptToken, stripANSIAndTrimRight(model.view.OngoingSnapshot()))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := len(model.transcriptEntries); got != 2 {
		t.Fatalf("expected hydration rewrite to replace transcript tail, got %d entries", got)
	}
	if got := model.transcriptEntries[1].Text; got != "after" {
		t.Fatalf("expected authoritative assistant rewrite after hydration, got %q", got)
	}
	if strings.TrimSpace(model.view.OngoingStreamingText()) != "" {
		t.Fatalf("expected hydration rewrite to clear stale streaming text, got %q", model.view.OngoingStreamingText())
	}

	program.Send(runtimeEventMsg{event: clientui.Event{
		Kind:                clientui.EventConversationUpdated,
		StepID:              "step-2",
		TranscriptRevision:  3,
		CommittedEntryCount: 3,
		TranscriptEntries: []clientui.ChatEntry{{
			Role:  "assistant",
			Text:  "next answer",
			Phase: string(llm.MessagePhaseFinal),
		}},
	}})
	waitForTestCondition(t, 2*time.Second, "later assistant append resumes after hydration rewrite", func() bool {
		return containsInOrder(normalizedOutput(out.String()), "after", "next answer")
	})

	program.Quit()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}

	normalized := normalizedOutput(out.String())
	if !containsInOrder(normalized, "before", "after", "next answer") {
		t.Fatalf("expected ongoing scrollback to show initial stale tail, recovered authoritative tail, then later assistant append, got %q", normalized)
	}
	if strings.Count(normalized, "next answer") != 1 {
		t.Fatalf("expected later assistant append exactly once, got %d in %q", strings.Count(normalized, "next answer"), normalized)
	}

	next, detailCmd := model.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	model = next.(*uiModel)
	_ = collectCmdMessages(t, detailCmd)
	if model.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected detail mode after toggle, got %q", model.view.Mode())
	}
	detail := stripANSIAndTrimRight(model.View())
	if !strings.Contains(detail, "after") || !strings.Contains(detail, "next answer") {
		t.Fatalf("expected detail mode to reflect authoritative transcript tail, got %q", detail)
	}
	if strings.Contains(detail, "before") {
		t.Fatalf("expected detail mode to exclude stale assistant tail after hydration rewrite, got %q", detail)
	}
}

func TestNativeHistoryFlushesPreserveScheduledOrderWhenDeliveredOutOfOrder(t *testing.T) {
	out := &bytes.Buffer{}
	model := newProjectedStaticUIModel()
	firstCmd := model.emitNativeRenderedText("assistant final\n")
	secondCmd := model.emitNativeRenderedText("queued user\n")
	if firstCmd == nil || secondCmd == nil {
		t.Fatal("expected native history flush commands")
	}
	firstMsg, ok := firstCmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected first nativeHistoryFlushMsg, got %T", firstCmd())
	}
	secondMsg, ok := secondCmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected second nativeHistoryFlushMsg, got %T", secondCmd())
	}
	if secondMsg.Sequence != firstMsg.Sequence+1 {
		t.Fatalf("expected consecutive native flush sequence numbers, first=%d second=%d", firstMsg.Sequence, secondMsg.Sequence)
	}

	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()

	time.Sleep(30 * time.Millisecond)
	program.Send(secondMsg)
	time.Sleep(30 * time.Millisecond)
	if strings.Contains(normalizedOutput(out.String()), "queued user") {
		t.Fatalf("expected later native flush buffered until earlier flush arrives, got %q", normalizedOutput(out.String()))
	}
	program.Send(firstMsg)
	waitForTestCondition(t, 2*time.Second, "ordered native flush replay", func() bool {
		return containsInOrder(normalizedOutput(out.String()), "assistant final", "queued user")
	})

	program.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}

	if normalized := normalizedOutput(out.String()); !containsInOrder(normalized, "assistant final", "queued user") {
		t.Fatalf("expected native history flushes to preserve scheduled order, got %q", normalized)
	}
}

func TestNativeAssistantDeltaSuppressedInDetailMode(t *testing.T) {
	out := &bytes.Buffer{}
	model := newProjectedTestUIModel(
		nil,
		closedProjectedRuntimeEvents(),
		closedAskEvents(),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "seed"}}),
	)
	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 30})
	program.Send(tea.KeyMsg{Type: tea.KeyShiftTab})
	time.Sleep(20 * time.Millisecond)
	program.Send(projectedRuntimeEventMsg(runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: "hidden-delta"}))
	time.Sleep(20 * time.Millisecond)
	program.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}
	if strings.Contains(normalizedOutput(out.String()), "hidden-delta") {
		t.Fatalf("expected assistant delta to stay suppressed while in detail mode, got %q", normalizedOutput(out.String()))
	}
}

func TestNativeStreamingTinyDeltasRemainContiguous(t *testing.T) {
	out := &bytes.Buffer{}
	model := newProjectedTestUIModel(nil, closedProjectedRuntimeEvents(), closedAskEvents())
	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 30})
	for _, delta := range []string{"he", "llo", " ", "wor", "ld", "\n"} {
		program.Send(projectedRuntimeEventMsg(runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: delta}))
	}
	time.Sleep(40 * time.Millisecond)
	program.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}
	plain := xansi.Strip(out.String())
	if !strings.Contains(plain, "hello world") {
		t.Fatalf("expected contiguous streamed text from tiny deltas, got %q", plain)
	}
	if strings.Contains(plain, "he\nllo") || strings.Contains(plain, "wor\nld") {
		t.Fatalf("expected no per-delta forced newlines in streamed text, got %q", plain)
	}
}

func TestNativeStreamingWithoutNewlineStillVisible(t *testing.T) {
	out := &bytes.Buffer{}
	model := newProjectedTestUIModel(nil, closedProjectedRuntimeEvents(), closedAskEvents())
	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 30})
	for _, delta := range []string{"long", " paragraph", " without", " newline"} {
		program.Send(projectedRuntimeEventMsg(runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: delta}))
	}
	time.Sleep(40 * time.Millisecond)
	program.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}
	if !strings.Contains(xansi.Strip(out.String()), "long paragraph without newline") {
		t.Fatalf("expected non-newline streaming text to still become visible, got %q", xansi.Strip(out.String()))
	}
}

func TestNativeProgramClearsResidualLivePadAfterStreamingCommit(t *testing.T) {
	out := &bytes.Buffer{}
	model := newProjectedTestUIModel(nil, closedProjectedRuntimeEvents(), closedAskEvents())
	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 20})
	program.Send(projectedRuntimeEventMsg(runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: "line1\nline2"}))
	time.Sleep(30 * time.Millisecond)
	program.Send(tui.SetConversationMsg{Entries: []tui.TranscriptEntry{}, Ongoing: ""})
	time.Sleep(30 * time.Millisecond)
	program.Send(tea.KeyMsg{Type: tea.KeyCtrlC})

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}

	if model.nativeLiveRegionPad <= 0 {
		t.Fatalf("expected fresh conversation to restore native live region pad after streaming commit, got %d", model.nativeLiveRegionPad)
	}
	if model.nativeStreamingActive {
		t.Fatal("expected native streaming active flag cleared after commit")
	}
}

func TestNativeStreamingInterleavedRendersKeepsLinesLeftAligned(t *testing.T) {
	out := &bytes.Buffer{}
	model := newProjectedTestUIModel(nil, closedProjectedRuntimeEvents(), closedAskEvents())
	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 30})
	expected := []string{"LADDER-01", "LADDER-02", "LADDER-03", "LADDER-04"}
	for _, token := range expected {
		program.Send(projectedRuntimeEventMsg(runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: token + "\n"}))
		program.Send(spinnerTickMsg{})
	}
	time.Sleep(50 * time.Millisecond)
	program.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}
	plain := xansi.Strip(out.String())
	normalized := strings.ReplaceAll(strings.ReplaceAll(plain, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(normalized, "\n")
	for index, token := range expected {
		prefix := "  "
		if index == 0 {
			prefix = "❮ "
		}
		expectedLine := prefix + token
		matched := false
		for _, line := range lines {
			trimmedRight := strings.TrimRight(line, " ")
			if trimmedRight == expectedLine {
				matched = true
				break
			}
		}
		if !matched {
			t.Fatalf("expected streamed line %q to render as %q, got %q", token, expectedLine, normalized)
		}
	}
}
