package app

import (
	"builder/cli/tui"
	"builder/server/llm"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/tools"
	sharedclient "builder/shared/client"
	"builder/shared/config"
	"bytes"
	"context"
	"errors"
	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
	"strings"
	"testing"
	"time"
)

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
		if strings.Count(model.nativeRenderedSnapshot, "TAIL-ONCE") != 1 {
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

	if count := strings.Count(model.nativeRenderedSnapshot, "TAIL-ONCE"); count != 1 {
		t.Fatalf("expected native rendered snapshot to contain tail token once, count=%d snapshot=%q", count, model.nativeRenderedSnapshot)
	}
	if strings.Contains(xansi.Strip(out.String()), "TAIL-ONCETAIL-ONCE") {
		t.Fatalf("expected no adjacent duplicate tail token writes, got %q", normalizedOutput(out.String()))
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

func TestNativeSubmitErrorFallbackAppendsToScrollbackWhenRuntimeAppendFails(t *testing.T) {
	out := &bytes.Buffer{}
	client := &runtimeControlFakeClient{submitErr: errors.New("daemon stalled"), appendErr: errors.New("append failed")}
	model := newProjectedTestUIModel(client, closedProjectedRuntimeEvents(), closedAskEvents())
	model.input = "run task"

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
	waitForTestCondition(t, 2*time.Second, "window size to apply before submit", func() bool {
		return model.windowSizeKnown
	})
	program.Send(tea.KeyMsg{Type: tea.KeyEnter})

	deadline := time.Now().Add(2 * time.Second)
	for {
		if len(model.transcriptEntries) == 1 && strings.Contains(normalizedOutput(out.String()), "daemon stalled") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for submit error fallback output=%q transcript=%+v native_projection=%+v native_rendered_projection=%+v native_snapshot=%q ongoing=%q window=%t replayed=%t flushed=%d", normalizedOutput(out.String()), model.transcriptEntries, model.nativeProjection, model.nativeRenderedProjection, model.nativeRenderedSnapshot, stripANSIAndTrimRight(model.view.OngoingSnapshot()), model.windowSizeKnown, model.nativeHistoryReplayed, model.nativeFlushedEntryCount)
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

	if normalized := normalizedOutput(out.String()); !strings.Contains(normalized, "daemon stalled") {
		t.Fatalf("expected submit error in native ongoing scrollback, got %q", normalized)
	}
}

func TestNativeDisconnectedSubmissionAppendsToScrollbackWhenRuntimeAppendFails(t *testing.T) {
	out := &bytes.Buffer{}
	client := &runtimeControlFakeClient{appendErr: errors.New("append failed")}
	model := newProjectedTestUIModel(client, closedProjectedRuntimeEvents(), closedAskEvents())
	model.input = "run task"

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
	waitForTestCondition(t, 2*time.Second, "window size to apply before disconnected submit", func() bool {
		return model.windowSizeKnown
	})
	model.setRuntimeDisconnected(true)
	program.Send(tea.KeyMsg{Type: tea.KeyEnter})

	deadline := time.Now().Add(2 * time.Second)
	for {
		if len(model.transcriptEntries) == 1 && strings.Contains(normalizedOutput(out.String()), runtimeDisconnectedStatusMessage) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for disconnected submit fallback output=%q transcript=%+v native_projection=%+v native_rendered_projection=%+v native_snapshot=%q ongoing=%q window=%t replayed=%t flushed=%d", normalizedOutput(out.String()), model.transcriptEntries, model.nativeProjection, model.nativeRenderedProjection, model.nativeRenderedSnapshot, stripANSIAndTrimRight(model.view.OngoingSnapshot()), model.windowSizeKnown, model.nativeHistoryReplayed, model.nativeFlushedEntryCount)
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

	if normalized := normalizedOutput(out.String()); !strings.Contains(normalized, runtimeDisconnectedStatusMessage) {
		t.Fatalf("expected disconnect error in native ongoing scrollback, got %q", normalized)
	}
}

func TestNativeDisconnectedSubmissionAfterRealRemoteDisconnectAppendsToScrollback(t *testing.T) {
	server := newRuntimeDisconnectTestRemote(t)
	defer server.Close()
	remote, err := sharedclient.DialRemoteURLForProject(context.Background(), "ws"+server.URL()[len("http"):], "project-1")
	if err != nil {
		t.Fatalf("DialRemoteURLForProject: %v", err)
	}
	defer func() { _ = remote.Close() }()

	runtimeClient := newUIRuntimeClientWithReads("session-1", remote, remote)
	out := &bytes.Buffer{}
	model := newProjectedTestUIModel(runtimeClient, closedProjectedRuntimeEvents(), closedAskEvents())
	model.input = "run task"

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
	waitForTestCondition(t, 2*time.Second, "window size to apply before real disconnect", func() bool {
		return model.windowSizeKnown
	})

	server.Close()
	var refreshErr error
	waitForTestCondition(t, 2*time.Second, "refresh main view error after remote shutdown", func() bool {
		_, refreshErr = runtimeClient.RefreshMainView()
		return refreshErr != nil
	})
	waitForTestCondition(t, 2*time.Second, "disconnect state after real remote shutdown", func() bool {
		return model.runtimeDisconnectStatusVisible()
	})

	program.Send(tea.KeyMsg{Type: tea.KeyEnter})

	deadline := time.Now().Add(2 * time.Second)
	for {
		if len(model.transcriptEntries) == 1 && strings.Contains(normalizedOutput(out.String()), runtimeDisconnectedStatusMessage) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for real disconnect fallback output=%q transcript=%+v native_projection=%+v native_rendered_projection=%+v native_snapshot=%q ongoing=%q window=%t replayed=%t flushed=%d", normalizedOutput(out.String()), model.transcriptEntries, model.nativeProjection, model.nativeRenderedProjection, model.nativeRenderedSnapshot, stripANSIAndTrimRight(model.view.OngoingSnapshot()), model.windowSizeKnown, model.nativeHistoryReplayed, model.nativeFlushedEntryCount)
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

	if normalized := normalizedOutput(out.String()); !strings.Contains(normalized, runtimeDisconnectedStatusMessage) {
		t.Fatalf("expected disconnect error in native ongoing scrollback, got %q", normalized)
	}
}

func TestNativeBackCommandSystemFeedbackAppendsToScrollback(t *testing.T) {
	out := &bytes.Buffer{}
	model := newProjectedStaticUIModel()
	model.input = "/back"

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
	waitForTestCondition(t, 2*time.Second, "window size to apply before back command", func() bool {
		return model.windowSizeKnown
	})
	program.Send(tea.KeyMsg{Type: tea.KeyEnter})

	deadline := time.Now().Add(2 * time.Second)
	for {
		if len(model.transcriptEntries) == 1 && strings.Contains(normalizedOutput(out.String()), "No parent session available") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for back command feedback output=%q transcript=%+v native_projection=%+v native_rendered_projection=%+v native_snapshot=%q ongoing=%q window=%t replayed=%t flushed=%d", normalizedOutput(out.String()), model.transcriptEntries, model.nativeProjection, model.nativeRenderedProjection, model.nativeRenderedSnapshot, stripANSIAndTrimRight(model.view.OngoingSnapshot()), model.windowSizeKnown, model.nativeHistoryReplayed, model.nativeFlushedEntryCount)
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

	if normalized := normalizedOutput(out.String()); !strings.Contains(normalized, "No parent session available") {
		t.Fatalf("expected back command feedback in native ongoing scrollback, got %q", normalized)
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

func TestNativeDeferredFinalWithQueuedInjectionSurvivesDetailModeRoundTrip(t *testing.T) {
	const roundTripTimeout = 5 * time.Second
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

	waitForTestCondition(t, roundTripTimeout, "live deferred final delta visible", func() bool {
		return strings.Contains(model.view.OngoingStreamingText(), "foreground done")
	})
	program.Send(tea.KeyMsg{Type: tea.KeyShiftTab})
	waitForTestCondition(t, roundTripTimeout, "detail mode active", func() bool {
		return model.view.Mode() == tui.ModeDetail
	})

	waitForSubmitResult(t, roundTripTimeout, submitDone)

	program.Send(tea.KeyMsg{Type: tea.KeyShiftTab})
	waitForTestCondition(t, roundTripTimeout, "ongoing mode active", func() bool {
		return model.view.Mode() == tui.ModeOngoing
	})
	waitForTestCondition(t, roundTripTimeout, "ongoing view keeps deferred final visible after detail exit", func() bool {
		ongoing := stripANSIAndTrimRight(model.view.OngoingSnapshot())
		return strings.Contains(ongoing, "foreground done")
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
}

func TestNativeDeferredFinalWithQueuedInjectionSurvivesDetailRoundTripBeforeCommit(t *testing.T) {
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
	program.Send(tea.KeyMsg{Type: tea.KeyShiftTab})
	waitForTestCondition(t, 2*time.Second, "detail mode active", func() bool {
		return model.view.Mode() == tui.ModeDetail
	})
	program.Send(tea.KeyMsg{Type: tea.KeyShiftTab})
	waitForTestCondition(t, 2*time.Second, "ongoing mode active before final commit", func() bool {
		return model.view.Mode() == tui.ModeOngoing
	})

	waitForSubmitResult(t, 2*time.Second, submitDone)
	waitForTestCondition(t, 2*time.Second, "ongoing view keeps deferred final visible after early detail exit", func() bool {
		ongoing := stripANSIAndTrimRight(model.view.OngoingSnapshot())
		return strings.Contains(ongoing, "foreground done")
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
	if !strings.Contains(normalized, "foreground done") {
		t.Fatalf("expected final answer to survive early detail round-trip, got %q", normalized)
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
