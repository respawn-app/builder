package app

import (
	"io"
	"os"
	"strings"
	"sync"

	"builder/server/tools/askquestion"
	"builder/shared/clientui"
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
	mu                    sync.Mutex
	notifier              terminalNotifier
	title                 func() string
	currentStep           string
	toolCalls             int
	pendingTurnCompletion bool
	lastCompletionMessage string
}

func newBellHooks(notifier terminalNotifier, title func() string) *bellHooks {
	if notifier == nil {
		notifier = newBELTerminalNotifier(io.Discard)
	}
	if title == nil {
		title = func() string { return defaultSessionTitle }
	}
	return &bellHooks{notifier: notifier, title: title}
}

func (h *bellHooks) OnAsk(req askquestion.Request) {
	question := formatAssistantPreview(req.Question, terminalNotificationPreviewLimit)
	if question == "" {
		if req.Approval {
			question = "action required"
		} else {
			question = "question from agent"
		}
	}
	label := "Question"
	if req.Approval {
		label = "Action required"
	}
	h.notifier.Notify(h.formatMessage(label + ": " + question))
}

func (h *bellHooks) OnProjectedRuntimeEvent(evt clientui.Event) {
	switch evt.Kind {
	case clientui.EventToolCallStarted:
		h.recordToolCall(evt.StepID)
	case clientui.EventAssistantMessage:
		h.recordTurnCompletion(evt.StepID, projectedAssistantMessageContent(evt.TranscriptEntries))
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

func (h *bellHooks) recordTurnCompletion(stepID, assistantContent string) {
	stepID = strings.TrimSpace(stepID)
	message := turnCompletionNotificationMessage(assistantContent)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastCompletionMessage = message
	if stepID == "" || h.currentStep != stepID {
		return
	}
	if h.toolCalls >= 2 {
		h.pendingTurnCompletion = true
	}
	h.currentStep = ""
	h.toolCalls = 0
}

func (h *bellHooks) OnTurnQueueDrained() {
	if h == nil {
		return
	}
	h.mu.Lock()
	if !h.pendingTurnCompletion {
		h.mu.Unlock()
		return
	}
	message := h.lastCompletionMessage
	h.pendingTurnCompletion = false
	h.lastCompletionMessage = ""
	h.mu.Unlock()
	h.notifier.Notify(h.formatMessage(message))
}

func (h *bellHooks) OnTurnQueueAborted() {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.currentStep = ""
	h.toolCalls = 0
	h.pendingTurnCompletion = false
	h.lastCompletionMessage = ""
	h.mu.Unlock()
}

func turnCompletionNotificationMessage(assistantContent string) string {
	if preview := formatAssistantPreview(assistantContent, terminalNotificationPreviewLimit); preview != "" {
		return preview
	}
	return "turn complete"
}

func projectedAssistantMessageContent(entries []clientui.ChatEntry) string {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Role != "assistant" {
			continue
		}
		return entries[i].Text
	}
	return ""
}

func (h *bellHooks) formatMessage(message string) string {
	title := defaultSessionTitle
	if h != nil && h.title != nil {
		title = sessionTitle(h.title())
	}
	return title + ": " + sanitizeTerminalNotificationMessage(message)
}
