package app

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"builder/server/tools/askquestion"
	"builder/shared/clientui"
)

type countRinger struct {
	mu    sync.Mutex
	count int
	last  string
}

func (r *countRinger) Notify(message string) {
	r.mu.Lock()
	r.count++
	r.last = message
	r.mu.Unlock()
}

func (r *countRinger) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

func (r *countRinger) Last() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

func TestTerminalBellRingerWritesBellCharacter(t *testing.T) {
	var out bytes.Buffer
	notifier := newTerminalNotifier(notificationMethodBEL, &out, nil)
	notifier.Notify("ignored")

	if got := out.String(); got != terminalBell {
		t.Fatalf("bell output = %q, want %q", got, terminalBell)
	}
}

func TestOSC9TerminalNotifierWritesEscapeSequence(t *testing.T) {
	var out bytes.Buffer
	notifier := newTerminalNotifier(notificationMethodOSC9, &out, nil)
	notifier.Notify("done")

	want := osc9Prefix + "done" + terminalBell + terminalBell
	if got := out.String(); got != want {
		t.Fatalf("osc9 output = %q, want %q", got, want)
	}
}

func TestAutoNotifierUsesOSC9ForGhostty(t *testing.T) {
	var out bytes.Buffer
	notifier := newTerminalNotifier(notificationMethodAuto, &out, func(key string) (string, bool) {
		switch key {
		case "TERM_PROGRAM":
			return "ghostty", true
		default:
			return "", false
		}
	})
	notifier.Notify("ping")

	want := osc9Prefix + "ping" + terminalBell + terminalBell
	if got := out.String(); got != want {
		t.Fatalf("auto output = %q, want %q", got, want)
	}
}

func TestAutoNotifierFallsBackToBELForWindowsTerminal(t *testing.T) {
	var out bytes.Buffer
	notifier := newTerminalNotifier(notificationMethodAuto, &out, func(key string) (string, bool) {
		switch key {
		case "TERM_PROGRAM":
			return "ghostty", true
		case "WT_SESSION":
			return "1", true
		default:
			return "", false
		}
	})
	notifier.Notify("ping")

	if got := out.String(); got != terminalBell {
		t.Fatalf("auto output = %q, want %q", got, terminalBell)
	}
}

func TestBellHooksRingOnAskRequests(t *testing.T) {
	ringer := &countRinger{}
	hooks := newBellHooks(ringer, nil)

	hooks.OnAsk(askquestion.Request{Question: "question"})
	hooks.OnAsk(askquestion.Request{Question: "approval", Approval: true})

	if got := ringer.Count(); got != 2 {
		t.Fatalf("ring count = %d, want 2", got)
	}
	if got := ringer.Last(); got != "builder: Action required: approval" {
		t.Fatalf("last message = %q, want %q", got, "builder: Action required: approval")
	}
}

func TestBellHooksUseSessionNameAndQuestionTextForAskNotifications(t *testing.T) {
	ringer := &countRinger{}
	hooks := newBellHooks(ringer, func() string { return "incident triage" })

	hooks.OnAsk(askquestion.Request{Question: "Which rollback strategy should I use?"})

	if got := ringer.Last(); got != "incident triage: Question: Which rollback strategy should I use?" {
		t.Fatalf("last message = %q, want %q", got, "incident triage: Question: Which rollback strategy should I use?")
	}
}

func TestBellHooksRingOnToolHeavyTurnEnd(t *testing.T) {
	ringer := &countRinger{}
	hooks := newBellHooks(ringer, nil)

	hooks.OnProjectedRuntimeEvent(clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-1"})
	hooks.OnProjectedRuntimeEvent(clientui.Event{Kind: clientui.EventAssistantMessage, StepID: "step-1"})
	if got := ringer.Count(); got != 0 {
		t.Fatalf("ring count = %d after single tool call turn, want 0", got)
	}

	hooks.OnProjectedRuntimeEvent(clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-2"})
	hooks.OnProjectedRuntimeEvent(clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-2"})
	hooks.OnProjectedRuntimeEvent(clientui.Event{Kind: clientui.EventAssistantMessage, StepID: "step-2"})
	if got := ringer.Count(); got != 0 {
		t.Fatalf("ring count = %d before queue drain, want 0", got)
	}
	hooks.OnTurnQueueDrained()
	if got := ringer.Count(); got != 1 {
		t.Fatalf("ring count = %d after queue drain, want 1", got)
	}
	if got := ringer.Last(); got != "builder: turn complete" {
		t.Fatalf("last message = %q, want %q", got, "builder: turn complete")
	}

	hooks.OnTurnQueueDrained()
	if got := ringer.Count(); got != 1 {
		t.Fatalf("ring count = %d after duplicate queue drain, want 1", got)
	}
}

func TestBellHooksIncludeAssistantPreviewInTurnCompleteNotification(t *testing.T) {
	ringer := &countRinger{}
	hooks := newBellHooks(ringer, nil)

	hooks.OnProjectedRuntimeEvent(clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-1"})
	hooks.OnProjectedRuntimeEvent(clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-1"})
	hooks.OnProjectedRuntimeEvent(clientui.Event{Kind: clientui.EventAssistantMessage, StepID: "step-1", TranscriptEntries: []clientui.ChatEntry{{Role: "assistant", Text: "  First line\n\nSecond line with details  "}}})
	hooks.OnTurnQueueDrained()

	if got := ringer.Last(); got != "builder: First line Second line with details" {
		t.Fatalf("last message = %q, want %q", got, "builder: First line Second line with details")
	}
}

func TestBellHooksFallbackToTurnCompleteForWhitespacePreview(t *testing.T) {
	ringer := &countRinger{}
	hooks := newBellHooks(ringer, nil)

	hooks.OnProjectedRuntimeEvent(clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-1"})
	hooks.OnProjectedRuntimeEvent(clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-1"})
	hooks.OnProjectedRuntimeEvent(clientui.Event{Kind: clientui.EventAssistantMessage, StepID: "step-1", TranscriptEntries: []clientui.ChatEntry{{Role: "assistant", Text: "\n\t  "}}})
	hooks.OnTurnQueueDrained()

	if got := ringer.Last(); got != "builder: turn complete" {
		t.Fatalf("last message = %q, want %q", got, "builder: turn complete")
	}
}

func TestFormatAssistantPreview(t *testing.T) {
	if got := formatAssistantPreview("\n  hello\tworld  ", 80); got != "hello world" {
		t.Fatalf("preview = %q, want %q", got, "hello world")
	}

	if got := formatAssistantPreview("", 80); got != "" {
		t.Fatalf("preview = %q, want empty", got)
	}

	if got := formatAssistantPreview("abcdef", 4); got != "abc…" {
		t.Fatalf("preview = %q, want %q", got, "abc…")
	}

	long := strings.Repeat("a", terminalNotificationPreviewLimit+5)
	want := strings.Repeat("a", terminalNotificationPreviewLimit-1) + "…"
	if got := formatAssistantPreview(long, terminalNotificationPreviewLimit); got != want {
		t.Fatalf("preview = %q, want %q", got, want)
	}

	if got := formatAssistantPreview("ab\x1bcd\a ef", 80); got != "abcd ef" {
		t.Fatalf("preview = %q, want %q", got, "abcd ef")
	}
}

func TestBellHooksIgnoresMismatchedTurnEndStep(t *testing.T) {
	ringer := &countRinger{}
	hooks := newBellHooks(ringer, nil)

	hooks.OnProjectedRuntimeEvent(clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-1"})
	hooks.OnProjectedRuntimeEvent(clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-1"})
	hooks.OnProjectedRuntimeEvent(clientui.Event{Kind: clientui.EventAssistantMessage, StepID: "step-2"})
	hooks.OnTurnQueueDrained()

	if got := ringer.Count(); got != 0 {
		t.Fatalf("ring count = %d, want 0", got)
	}
}

func TestBellHooksRingOnceAfterQueuedTurnsDrain(t *testing.T) {
	ringer := &countRinger{}
	hooks := newBellHooks(ringer, nil)

	hooks.OnProjectedRuntimeEvent(clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-1"})
	hooks.OnProjectedRuntimeEvent(clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-1"})
	hooks.OnProjectedRuntimeEvent(clientui.Event{Kind: clientui.EventAssistantMessage, StepID: "step-1", TranscriptEntries: []clientui.ChatEntry{{Role: "assistant", Text: "first"}}})
	hooks.OnProjectedRuntimeEvent(clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-2"})
	hooks.OnProjectedRuntimeEvent(clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-2"})
	hooks.OnProjectedRuntimeEvent(clientui.Event{Kind: clientui.EventAssistantMessage, StepID: "step-2", TranscriptEntries: []clientui.ChatEntry{{Role: "assistant", Text: "second"}}})

	if got := ringer.Count(); got != 0 {
		t.Fatalf("ring count = %d before queue drain, want 0", got)
	}
	hooks.OnTurnQueueDrained()
	if got := ringer.Count(); got != 1 {
		t.Fatalf("ring count = %d after queue drain, want 1", got)
	}
	if got := ringer.Last(); got != "builder: second" {
		t.Fatalf("last message = %q, want %q", got, "builder: second")
	}
}

func TestBellHooksClearPendingTurnCompletionAfterAbort(t *testing.T) {
	ringer := &countRinger{}
	hooks := newBellHooks(ringer, nil)

	hooks.OnProjectedRuntimeEvent(clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-1"})
	hooks.OnProjectedRuntimeEvent(clientui.Event{Kind: clientui.EventToolCallStarted, StepID: "step-1"})
	hooks.OnProjectedRuntimeEvent(clientui.Event{Kind: clientui.EventAssistantMessage, StepID: "step-1", TranscriptEntries: []clientui.ChatEntry{{Role: "assistant", Text: "first"}}})
	hooks.OnTurnQueueAborted()
	hooks.OnTurnQueueDrained()

	if got := ringer.Count(); got != 0 {
		t.Fatalf("ring count = %d after aborted queue, want 0", got)
	}
}
