package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"builder/internal/config"
	"builder/internal/llm"
	"builder/internal/runtime"
	"builder/internal/session"
	"builder/internal/tools"
	"builder/internal/transcript"
	"builder/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
)

func closedRuntimeEvents() <-chan runtime.Event {
	ch := make(chan runtime.Event)
	close(ch)
	return ch
}

func closedAskEvents() <-chan askEvent {
	ch := make(chan askEvent)
	close(ch)
	return ch
}

func normalizedOutput(v string) string {
	return strings.Join(strings.Fields(xansi.Strip(v)), " ")
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

func TestNativeScrollbackProgramOutputContract(t *testing.T) {
	out := &bytes.Buffer{}
	model := NewUIModel(
		nil,
		closedRuntimeEvents(),
		closedAskEvents(),
		WithUIInitialTranscript([]UITranscriptEntry{
			{Role: "user", Text: "first replay line"},
			{Role: "assistant", Text: "second replay line"},
		}),
	).(*uiModel)

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
		model := NewUIModel(
			nil,
			closedRuntimeEvents(),
			closedAskEvents(),
		).(*uiModel)

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
	model := NewUIModel(
		nil,
		closedRuntimeEvents(),
		closedAskEvents(),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "seed replay line"}}),
	).(*uiModel)
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
		{Width: 96, Height: 24},
		{Width: 110, Height: 28},
		{Width: 84, Height: 22},
	} {
		program.Send(size)
		time.Sleep(20 * time.Millisecond)
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

	raw := out.String()
	if count := strings.Count(raw, "\x1b[2J"); count < 2 {
		t.Fatalf("expected startup clear plus one debounced resize clear, got %d occurrences in %q", count, raw)
	}
	plain := xansi.Strip(raw)
	if strings.Count(normalizedOutput(raw), "seed replay line") < 2 {
		t.Fatalf("expected committed history replayed after debounced resize, got %q", normalizedOutput(raw))
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
	if borderLines > 16 {
		t.Fatalf("expected bounded border redraw count during resize, got %d", borderLines)
	}
	if strings.Count(plain, "ongoing | ") > 12 {
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
	model := NewUIModel(
		nil,
		closedRuntimeEvents(),
		closedAskEvents(),
	).(*uiModel)
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
	model := NewUIModel(
		nil,
		closedRuntimeEvents(),
		closedAskEvents(),
		WithUIAlternateScreenPolicy(config.TUIAlternateScreenAuto),
		WithUIInitialTranscript([]UITranscriptEntry{
			{Role: "user", Text: "u1"},
			{Role: "assistant", Text: "a1"},
			{Role: "user", Text: "u2"},
		}),
	).(*uiModel)

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
	model := NewUIModel(
		nil,
		closedRuntimeEvents(),
		closedAskEvents(),
		WithUIAlternateScreenPolicy(config.TUIAlternateScreenAuto),
	).(*uiModel)
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
	model := NewUIModel(
		nil,
		closedRuntimeEvents(),
		closedAskEvents(),
		WithUIAlternateScreenPolicy(config.TUIAlternateScreenNever),
	).(*uiModel)
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
	if strings.Contains(raw, "\x1b[?1049h") || strings.Contains(raw, "\x1b[?1049l") {
		t.Fatalf("did not expect /ps overlay to use alt-screen when detail alt-screen is disabled, got %q", raw)
	}
	sequenceLog := strings.Join(terminalSequences, "")
	if strings.Contains(sequenceLog, "\x1b[?1007h") || strings.Contains(sequenceLog, "\x1b[?1007l") {
		t.Fatalf("did not expect /ps overlay to toggle alternate scroll when detail alt-screen is disabled, got %q", sequenceLog)
	}
	if clearCount := strings.Count(raw, "\x1b[2J"); clearCount < 3 {
		t.Fatalf("expected startup + /ps open + /ps close clear-screen sequences, got %d in %q", clearCount, raw)
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
	model := NewUIModel(
		eng,
		runtimeEvents,
		closedAskEvents(),
	).(*uiModel)

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
	go func() {
		_, _ = eng.SubmitUserMessage(context.Background(), "trigger")
	}()
	time.Sleep(220 * time.Millisecond)
	program.Send(tea.KeyMsg{Type: tea.KeyCtrlC})

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
	model := NewUIModel(
		eng,
		runtimeEvents,
		closedAskEvents(),
	).(*uiModel)

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
	go func() {
		_, _ = eng.SubmitUserMessage(context.Background(), "trigger")
	}()
	time.Sleep(260 * time.Millisecond)
	program.Send(tea.KeyMsg{Type: tea.KeyCtrlC})

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
		t.Fatalf("expected final content in output, got %q", normalized)
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
	model := NewUIModel(
		eng,
		runtimeEvents,
		closedAskEvents(),
	).(*uiModel)

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
	go func() {
		_, _ = eng.SubmitUserMessage(context.Background(), "trigger")
	}()
	time.Sleep(220 * time.Millisecond)
	program.Send(tea.KeyMsg{Type: tea.KeyCtrlC})

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
	model := NewUIModel(
		nil,
		closedRuntimeEvents(),
		closedAskEvents(),
	).(*uiModel)

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
	model := NewUIModel(
		nil,
		closedRuntimeEvents(),
		closedAskEvents(),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "**bold** and `code`"}}),
	).(*uiModel)
	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 32})
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
	model := NewUIModel(
		nil,
		closedRuntimeEvents(),
		closedAskEvents(),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "prompt once"}}),
	).(*uiModel)
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

	program.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program run failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("program did not terminate")
	}
}

func TestNativeStreamingInterleavedWithStatusRedrawStaysCoherent(t *testing.T) {
	out := &bytes.Buffer{}
	model := NewUIModel(
		nil,
		closedRuntimeEvents(),
		closedAskEvents(),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "user", Text: "prompt once"}}),
	).(*uiModel)
	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 32})
	program.Send(runtimeEventMsg{event: runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: "line1\n"}})
	program.Send(spinnerTickMsg{})
	program.Send(runtimeEventMsg{event: runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: "line2\n"}})
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

func TestNativeAssistantDeltaSuppressedInDetailMode(t *testing.T) {
	out := &bytes.Buffer{}
	model := NewUIModel(
		nil,
		closedRuntimeEvents(),
		closedAskEvents(),
		WithUIInitialTranscript([]UITranscriptEntry{{Role: "assistant", Text: "seed"}}),
	).(*uiModel)
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
	program.Send(runtimeEventMsg{event: runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: "hidden-delta"}})
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
	model := NewUIModel(
		nil,
		closedRuntimeEvents(),
		closedAskEvents(),
	).(*uiModel)
	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 30})
	for _, delta := range []string{"he", "llo", " ", "wor", "ld", "\n"} {
		program.Send(runtimeEventMsg{event: runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: delta}})
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
	model := NewUIModel(
		nil,
		closedRuntimeEvents(),
		closedAskEvents(),
	).(*uiModel)
	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 30})
	for _, delta := range []string{"long", " paragraph", " without", " newline"} {
		program.Send(runtimeEventMsg{event: runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: delta}})
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
	model := NewUIModel(
		nil,
		closedRuntimeEvents(),
		closedAskEvents(),
	).(*uiModel)
	program := tea.NewProgram(model, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	program.Send(tea.WindowSizeMsg{Width: 120, Height: 20})
	program.Send(runtimeEventMsg{event: runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: "line1\nline2"}})
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
	model := NewUIModel(
		nil,
		closedRuntimeEvents(),
		closedAskEvents(),
	).(*uiModel)
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
		program.Send(runtimeEventMsg{event: runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: token + "\n"}})
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
