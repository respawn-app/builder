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

func (r *countRinger) Ring() {
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
	ringer := newTerminalBellRinger(&out)
	ringer.Ring()

	if got := out.String(); got != terminalBell {
		t.Fatalf("bell output = %q, want %q", got, terminalBell)
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
