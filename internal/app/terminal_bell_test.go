package app

import (
	"bytes"
	"sync"
	"testing"

	"builder/internal/runtime"
	"builder/internal/tools/askquestion"
)

type countRinger struct {
	mu    sync.Mutex
	count int
}

func (r *countRinger) Notify(_ string) {
	r.mu.Lock()
	r.count++
	r.mu.Unlock()
}

func (r *countRinger) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
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

	want := osc9Prefix + "done" + terminalBell
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

	want := osc9Prefix + "ping" + terminalBell
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
	hooks := newBellHooks(ringer)

	hooks.OnAsk(askquestion.Request{Question: "question"})
	hooks.OnAsk(askquestion.Request{Question: "approval", Approval: true})

	if got := ringer.Count(); got != 2 {
		t.Fatalf("ring count = %d, want 2", got)
	}
}

func TestBellHooksRingOnToolHeavyTurnEnd(t *testing.T) {
	ringer := &countRinger{}
	hooks := newBellHooks(ringer)

	hooks.OnRuntimeEvent(runtime.Event{Kind: runtime.EventToolCallStarted, StepID: "step-1"})
	hooks.OnRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantMessage, StepID: "step-1"})
	if got := ringer.Count(); got != 0 {
		t.Fatalf("ring count = %d after single tool call turn, want 0", got)
	}

	hooks.OnRuntimeEvent(runtime.Event{Kind: runtime.EventToolCallStarted, StepID: "step-2"})
	hooks.OnRuntimeEvent(runtime.Event{Kind: runtime.EventToolCallStarted, StepID: "step-2"})
	hooks.OnRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantMessage, StepID: "step-2"})
	if got := ringer.Count(); got != 1 {
		t.Fatalf("ring count = %d after two tool call turn, want 1", got)
	}

	hooks.OnRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantMessage, StepID: "step-2"})
	if got := ringer.Count(); got != 1 {
		t.Fatalf("ring count = %d after duplicate assistant event, want 1", got)
	}
}

func TestBellHooksIgnoresMismatchedTurnEndStep(t *testing.T) {
	ringer := &countRinger{}
	hooks := newBellHooks(ringer)

	hooks.OnRuntimeEvent(runtime.Event{Kind: runtime.EventToolCallStarted, StepID: "step-1"})
	hooks.OnRuntimeEvent(runtime.Event{Kind: runtime.EventToolCallStarted, StepID: "step-1"})
	hooks.OnRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantMessage, StepID: "step-2"})

	if got := ringer.Count(); got != 0 {
		t.Fatalf("ring count = %d, want 0", got)
	}
}
