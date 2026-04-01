package app

import (
	"context"
	"errors"
	"testing"

	"builder/shared/clientui"
)

type runtimeControlFakeClient struct {
	status                clientui.RuntimeStatus
	sessionView           clientui.RuntimeSessionView
	mainView              clientui.RuntimeMainView
	setSessionNameArg     string
	setThinkingLevelArg   string
	setFastModeArg        bool
	setReviewerArg        bool
	setAutoCompactArg     bool
	appendedRole          string
	appendedText          string
	shouldCompactText     string
	shouldCompactResult   bool
	submitText            string
	submitResult          string
	submitShellCommand    string
	compactArgs           string
	hasQueuedUserWork     bool
	submitQueuedResult    string
	interruptCalls        int
	queuedText            string
	discardQueuedText     string
	discardQueuedCount    int
	recordedPromptHistory string
	err                   error
}

func (f *runtimeControlFakeClient) MainView() clientui.RuntimeMainView {
	if f.mainView.Session.SessionID != "" || f.mainView.Status.ThinkingLevel != "" || f.mainView.ActiveRun != nil {
		return f.mainView
	}
	return clientui.RuntimeMainView{Status: f.status, Session: f.sessionView}
}
func (f *runtimeControlFakeClient) Status() clientui.RuntimeStatus { return f.status }
func (f *runtimeControlFakeClient) SessionView() clientui.RuntimeSessionView {
	return f.sessionView
}
func (f *runtimeControlFakeClient) SetSessionName(name string) error {
	f.setSessionNameArg = name
	return f.err
}
func (f *runtimeControlFakeClient) SetThinkingLevel(level string) error {
	f.setThinkingLevelArg = level
	return f.err
}
func (f *runtimeControlFakeClient) SetFastModeEnabled(enabled bool) (bool, error) {
	f.setFastModeArg = enabled
	return true, f.err
}
func (f *runtimeControlFakeClient) SetReviewerEnabled(enabled bool) (bool, string, error) {
	f.setReviewerArg = enabled
	return true, "edits", f.err
}
func (f *runtimeControlFakeClient) SetAutoCompactionEnabled(enabled bool) (bool, bool) {
	f.setAutoCompactArg = enabled
	return true, enabled
}
func (f *runtimeControlFakeClient) AppendLocalEntry(role, text string) {
	f.appendedRole = role
	f.appendedText = text
}
func (f *runtimeControlFakeClient) ShouldCompactBeforeUserMessage(_ context.Context, text string) (bool, error) {
	f.shouldCompactText = text
	return f.shouldCompactResult, f.err
}
func (f *runtimeControlFakeClient) SubmitUserMessage(_ context.Context, text string) (string, error) {
	f.submitText = text
	return f.submitResult, f.err
}
func (f *runtimeControlFakeClient) SubmitUserShellCommand(_ context.Context, command string) error {
	f.submitShellCommand = command
	return f.err
}
func (f *runtimeControlFakeClient) CompactContext(_ context.Context, args string) error {
	f.compactArgs = args
	return f.err
}
func (f *runtimeControlFakeClient) CompactContextForPreSubmit(context.Context) error {
	f.compactArgs = "__pre_submit__"
	return f.err
}
func (f *runtimeControlFakeClient) HasQueuedUserWork() bool { return f.hasQueuedUserWork }
func (f *runtimeControlFakeClient) SubmitQueuedUserMessages(context.Context) (string, error) {
	return f.submitQueuedResult, f.err
}
func (f *runtimeControlFakeClient) Interrupt() error {
	f.interruptCalls++
	return f.err
}
func (f *runtimeControlFakeClient) QueueUserMessage(text string) { f.queuedText = text }
func (f *runtimeControlFakeClient) DiscardQueuedUserMessagesMatching(text string) int {
	f.discardQueuedText = text
	return f.discardQueuedCount
}
func (f *runtimeControlFakeClient) RecordPromptHistory(text string) error {
	f.recordedPromptHistory = text
	return f.err
}

func TestRuntimeControlHelpersDelegateToRuntimeClient(t *testing.T) {
	client := &runtimeControlFakeClient{
		shouldCompactResult: true,
		submitResult:        "assistant",
		hasQueuedUserWork:   true,
		submitQueuedResult:  "queued assistant",
		discardQueuedCount:  2,
	}
	m := newProjectedStaticUIModel()
	m.engine = client

	if err := m.setRuntimeSessionName("incident triage"); err != nil {
		t.Fatalf("set runtime session name: %v", err)
	}
	if err := m.setRuntimeThinkingLevel("high"); err != nil {
		t.Fatalf("set runtime thinking level: %v", err)
	}
	if changed, err := m.setRuntimeFastModeEnabled(true); !changed || err != nil {
		t.Fatalf("set runtime fast mode = (%t, %v), want (true, nil)", changed, err)
	}
	if changed, mode, err := m.setRuntimeReviewerEnabled(true); !changed || mode != "edits" || err != nil {
		t.Fatalf("set runtime reviewer = (%t, %q, %v)", changed, mode, err)
	}
	if changed, enabled := m.setRuntimeAutoCompactionEnabled(false); !changed || enabled {
		t.Fatalf("set runtime autocompaction = (%t, %t), want (true, false)", changed, enabled)
	}
	m.appendRuntimeLocalEntry("system", "hello")
	shouldCompact, err := m.runtimeShouldCompactBeforeUserMessage(context.Background(), "prompt")
	if err != nil || !shouldCompact {
		t.Fatalf("runtime should compact = (%t, %v), want (true, nil)", shouldCompact, err)
	}
	message, err := m.submitRuntimeUserMessage(context.Background(), "prompt")
	if err != nil || message != "assistant" {
		t.Fatalf("submit runtime user message = (%q, %v), want (assistant, nil)", message, err)
	}
	if err := m.submitRuntimeUserShellCommand(context.Background(), "echo hi"); err != nil {
		t.Fatalf("submit runtime shell command: %v", err)
	}
	if err := m.compactRuntimeContext(context.Background(), "--force"); err != nil {
		t.Fatalf("compact runtime context: %v", err)
	}
	if err := m.compactRuntimeContextForPreSubmit(context.Background()); err != nil {
		t.Fatalf("compact runtime context for presubmit: %v", err)
	}
	if !m.hasQueuedRuntimeUserWork() {
		t.Fatal("expected queued runtime user work")
	}
	queuedMessage, err := m.submitQueuedRuntimeUserMessages(context.Background())
	if err != nil || queuedMessage != "queued assistant" {
		t.Fatalf("submit queued runtime user messages = (%q, %v)", queuedMessage, err)
	}
	if err := m.interruptRuntime(); err != nil {
		t.Fatalf("interrupt runtime: %v", err)
	}
	m.queueRuntimeUserMessage("queued text")
	if discarded := m.discardQueuedRuntimeUserMessagesMatching("queued text"); discarded != 2 {
		t.Fatalf("discard queued runtime user messages = %d, want 2", discarded)
	}
	if err := m.recordRuntimePromptHistory("prompt history"); err != nil {
		t.Fatalf("record runtime prompt history: %v", err)
	}

	if client.setSessionNameArg != "incident triage" || client.setThinkingLevelArg != "high" {
		t.Fatalf("unexpected set args: session=%q thinking=%q", client.setSessionNameArg, client.setThinkingLevelArg)
	}
	if !client.setFastModeArg || !client.setReviewerArg || client.setAutoCompactArg {
		t.Fatalf("unexpected toggle args: fast=%t reviewer=%t autocompact=%t", client.setFastModeArg, client.setReviewerArg, client.setAutoCompactArg)
	}
	if client.appendedRole != "system" || client.appendedText != "hello" {
		t.Fatalf("unexpected appended local entry: role=%q text=%q", client.appendedRole, client.appendedText)
	}
	if client.shouldCompactText != "prompt" || client.submitText != "prompt" || client.submitShellCommand != "echo hi" {
		t.Fatalf("unexpected submission args: compact=%q submit=%q shell=%q", client.shouldCompactText, client.submitText, client.submitShellCommand)
	}
	if client.compactArgs != "__pre_submit__" {
		t.Fatalf("unexpected compact arg marker: %q", client.compactArgs)
	}
	if client.interruptCalls != 1 || client.queuedText != "queued text" || client.discardQueuedText != "queued text" || client.recordedPromptHistory != "prompt history" {
		t.Fatalf("unexpected runtime helper side effects: interrupts=%d queued=%q discard=%q history=%q", client.interruptCalls, client.queuedText, client.discardQueuedText, client.recordedPromptHistory)
	}
}

func TestRuntimeControlHelpersFallbackWithoutRuntimeClient(t *testing.T) {
	m := newProjectedStaticUIModel()

	if err := m.setRuntimeSessionName("name"); err != nil {
		t.Fatalf("set runtime session name without client: %v", err)
	}
	if err := m.setRuntimeThinkingLevel("high"); err != nil {
		t.Fatalf("set runtime thinking level without client: %v", err)
	}
	if changed, err := m.setRuntimeFastModeEnabled(true); changed || err != nil {
		t.Fatalf("set runtime fast mode without client = (%t, %v), want (false, nil)", changed, err)
	}
	if changed, mode, err := m.setRuntimeReviewerEnabled(true); changed || mode != "" || err != nil {
		t.Fatalf("set runtime reviewer without client = (%t, %q, %v)", changed, mode, err)
	}
	if changed, enabled := m.setRuntimeAutoCompactionEnabled(true); changed || enabled {
		t.Fatalf("set runtime autocompaction without client = (%t, %t), want (false, false)", changed, enabled)
	}
	if shouldCompact, err := m.runtimeShouldCompactBeforeUserMessage(context.Background(), "prompt"); shouldCompact || err != nil {
		t.Fatalf("runtime should compact without client = (%t, %v), want (false, nil)", shouldCompact, err)
	}
	if message, err := m.submitRuntimeUserMessage(context.Background(), "prompt"); message != "" || err != nil {
		t.Fatalf("submit runtime user message without client = (%q, %v), want (empty, nil)", message, err)
	}
	if err := m.submitRuntimeUserShellCommand(context.Background(), "echo hi"); err != nil {
		t.Fatalf("submit runtime shell command without client: %v", err)
	}
	if err := m.compactRuntimeContext(context.Background(), "--force"); err != nil {
		t.Fatalf("compact runtime context without client: %v", err)
	}
	if err := m.compactRuntimeContextForPreSubmit(context.Background()); err != nil {
		t.Fatalf("compact runtime context for presubmit without client: %v", err)
	}
	if m.hasQueuedRuntimeUserWork() {
		t.Fatal("did not expect queued runtime user work without client")
	}
	if message, err := m.submitQueuedRuntimeUserMessages(context.Background()); message != "" || err != nil {
		t.Fatalf("submit queued runtime user messages without client = (%q, %v), want (empty, nil)", message, err)
	}
	if err := m.interruptRuntime(); err != nil {
		t.Fatalf("interrupt runtime without client: %v", err)
	}
	m.queueRuntimeUserMessage("queued text")
	if discarded := m.discardQueuedRuntimeUserMessagesMatching("queued text"); discarded != 0 {
		t.Fatalf("discard queued runtime user messages without client = %d, want 0", discarded)
	}
	if err := m.recordRuntimePromptHistory("prompt history"); err != nil {
		t.Fatalf("record runtime prompt history without client: %v", err)
	}
}

func TestRuntimeControlHelpersPropagateRuntimeErrors(t *testing.T) {
	boom := errors.New("boom")
	m := newProjectedStaticUIModel()
	m.engine = &runtimeControlFakeClient{err: boom}

	if err := m.setRuntimeSessionName("name"); !errors.Is(err, boom) {
		t.Fatalf("set runtime session name error = %v, want boom", err)
	}
	if _, err := m.setRuntimeFastModeEnabled(true); !errors.Is(err, boom) {
		t.Fatalf("set runtime fast mode error = %v, want boom", err)
	}
	if _, _, err := m.setRuntimeReviewerEnabled(true); !errors.Is(err, boom) {
		t.Fatalf("set runtime reviewer error = %v, want boom", err)
	}
	if _, err := m.runtimeShouldCompactBeforeUserMessage(context.Background(), "prompt"); !errors.Is(err, boom) {
		t.Fatalf("runtime should compact error = %v, want boom", err)
	}
	if _, err := m.submitRuntimeUserMessage(context.Background(), "prompt"); !errors.Is(err, boom) {
		t.Fatalf("submit runtime user message error = %v, want boom", err)
	}
	if err := m.submitRuntimeUserShellCommand(context.Background(), "echo hi"); !errors.Is(err, boom) {
		t.Fatalf("submit runtime shell command error = %v, want boom", err)
	}
	if err := m.compactRuntimeContext(context.Background(), "--force"); !errors.Is(err, boom) {
		t.Fatalf("compact runtime context error = %v, want boom", err)
	}
	if _, err := m.submitQueuedRuntimeUserMessages(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("submit queued runtime user messages error = %v, want boom", err)
	}
	if err := m.interruptRuntime(); !errors.Is(err, boom) {
		t.Fatalf("interrupt runtime error = %v, want boom", err)
	}
	if err := m.recordRuntimePromptHistory("prompt history"); !errors.Is(err, boom) {
		t.Fatalf("record runtime prompt history error = %v, want boom", err)
	}
}
