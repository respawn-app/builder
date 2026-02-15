package app

import (
	"io"
	"os"
	"strings"
	"sync"

	"builder/internal/runtime"
	"builder/internal/tools/askquestion"
)

const terminalBell = "\a"

type bellRinger interface {
	Ring()
}

type terminalBellRinger struct {
	mu  sync.Mutex
	out io.Writer
}

func newTerminalBellRinger(out io.Writer) *terminalBellRinger {
	if out == nil {
		out = io.Discard
	}
	return &terminalBellRinger{out: out}
}

func defaultTerminalBellRinger() *terminalBellRinger {
	return newTerminalBellRinger(os.Stdout)
}

func (r *terminalBellRinger) Ring() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, _ = io.WriteString(r.out, terminalBell)
}

type bellHooks struct {
	mu          sync.Mutex
	ringer      bellRinger
	currentStep string
	toolCalls   int
}

func newBellHooks(ringer bellRinger) *bellHooks {
	if ringer == nil {
		ringer = newTerminalBellRinger(io.Discard)
	}
	return &bellHooks{ringer: ringer}
}

func (h *bellHooks) OnAsk(_ askquestion.Request) {
	h.ringer.Ring()
}

func (h *bellHooks) OnRuntimeEvent(evt runtime.Event) {
	switch evt.Kind {
	case runtime.EventToolCallStarted:
		h.recordToolCall(evt.StepID)
	case runtime.EventAssistantMessage:
		h.ringIfToolHeavyTurnEnd(evt.StepID)
	}
}

func (h *bellHooks) recordToolCall(stepID string) {
	stepID = strings.TrimSpace(stepID)
	if stepID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.currentStep != stepID {
		h.currentStep = stepID
		h.toolCalls = 0
	}
	h.toolCalls++
}

func (h *bellHooks) ringIfToolHeavyTurnEnd(stepID string) {
	stepID = strings.TrimSpace(stepID)
	if stepID == "" {
		return
	}
	shouldRing := false
	h.mu.Lock()
	if h.currentStep == stepID {
		shouldRing = h.toolCalls >= 2
		h.currentStep = ""
		h.toolCalls = 0
	}
	h.mu.Unlock()
	if shouldRing {
		h.ringer.Ring()
	}
}
