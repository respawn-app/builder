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
const osc9Prefix = "\x1b]9;"
const terminalNotificationPreviewLimit = 80

const (
	notificationMethodAuto = "auto"
	notificationMethodOSC9 = "osc9"
	notificationMethodBEL  = "bel"
)

type terminalNotifier interface {
	Notify(message string)
}

type belTerminalNotifier struct {
	mu  sync.Mutex
	out io.Writer
}

type osc9TerminalNotifier struct {
	mu  sync.Mutex
	out io.Writer
}

func newBELTerminalNotifier(out io.Writer) *belTerminalNotifier {
	if out == nil {
		out = io.Discard
	}
	return &belTerminalNotifier{out: out}
}

func newOSC9TerminalNotifier(out io.Writer) *osc9TerminalNotifier {
	if out == nil {
		out = io.Discard
	}
	return &osc9TerminalNotifier{out: out}
}

func defaultTerminalNotifier(method string) terminalNotifier {
	return newTerminalNotifier(method, os.Stdout, os.LookupEnv)
}

func newTerminalNotifier(method string, out io.Writer, lookup func(string) (string, bool)) terminalNotifier {
	normalized := strings.ToLower(strings.TrimSpace(method))
	if normalized == "" {
		normalized = notificationMethodAuto
	}
	switch normalized {
	case notificationMethodOSC9:
		return newOSC9TerminalNotifier(out)
	case notificationMethodBEL:
		return newBELTerminalNotifier(out)
	default:
		if supportsOSC9(lookup) {
			return newOSC9TerminalNotifier(out)
		}
		return newBELTerminalNotifier(out)
	}
}

func supportsOSC9(lookup func(string) (string, bool)) bool {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	if _, ok := lookup("WT_SESSION"); ok {
		return false
	}
	if termProgram, ok := lookup("TERM_PROGRAM"); ok {
		switch termProgram {
		case "WezTerm", "ghostty":
			return true
		}
	}
	if _, ok := lookup("ITERM_SESSION_ID"); ok {
		return true
	}
	if term, ok := lookup("TERM"); ok {
		switch term {
		case "xterm-kitty", "wezterm", "wezterm-mux":
			return true
		}
	}
	return false
}

func sanitizeTerminalNotificationMessage(message string) string {
	message = strings.ReplaceAll(message, "\x1b", "")
	message = strings.ReplaceAll(message, terminalBell, "")
	return message
}

func formatAssistantPreview(content string, maxChars int) string {
	normalized := strings.Join(strings.Fields(sanitizeTerminalNotificationMessage(content)), " ")
	trimmed := strings.TrimSpace(normalized)
	if trimmed == "" {
		return ""
	}
	if maxChars <= 0 {
		return trimmed
	}
	runes := []rune(trimmed)
	if len(runes) <= maxChars {
		return trimmed
	}
	if maxChars == 1 {
		return "…"
	}
	return string(runes[:maxChars-1]) + "…"
}

func (r *belTerminalNotifier) Notify(_ string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, _ = io.WriteString(r.out, terminalBell)
}

func (r *osc9TerminalNotifier) Notify(message string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// The first BEL terminates the OSC 9 sequence. Emit a second BEL so asks and
	// turn-complete notifications still produce an audible bell on OSC-capable terminals.
	_, _ = io.WriteString(r.out, osc9Prefix+sanitizeTerminalNotificationMessage(message)+terminalBell+terminalBell)
}

type bellHooks struct {
	mu          sync.Mutex
	notifier    terminalNotifier
	currentStep string
	toolCalls   int
}

func newBellHooks(notifier terminalNotifier) *bellHooks {
	if notifier == nil {
		notifier = newBELTerminalNotifier(io.Discard)
	}
	return &bellHooks{notifier: notifier}
}

func (h *bellHooks) OnAsk(req askquestion.Request) {
	message := "Builder: action required"
	if !req.Approval {
		message = "Builder: question from agent"
	}
	h.notifier.Notify(message)
}

func (h *bellHooks) OnRuntimeEvent(evt runtime.Event) {
	switch evt.Kind {
	case runtime.EventToolCallStarted:
		h.recordToolCall(evt.StepID)
	case runtime.EventAssistantMessage:
		h.ringIfToolHeavyTurnEnd(evt.StepID, evt.Message.Content)
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

func (h *bellHooks) ringIfToolHeavyTurnEnd(stepID, assistantContent string) {
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
		message := "Builder: turn complete"
		if preview := formatAssistantPreview(assistantContent, terminalNotificationPreviewLimit); preview != "" {
			message = "Builder: " + preview
		}
		h.notifier.Notify(message)
	}
}
