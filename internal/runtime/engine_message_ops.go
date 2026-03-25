package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"builder/internal/llm"
	"builder/internal/tools"
	"builder/prompts"
)

func (e *Engine) persistToolCompletion(stepID string, r tools.Result) error {
	if sessionID, ok := harvestedBackgroundCompletionSessionID(r); ok {
		e.consumePendingBackgroundNotice(sessionID)
	}
	_, err := e.store.AppendEvent(stepID, "tool_completed", map[string]any{
		"call_id":  r.CallID,
		"name":     string(r.Name),
		"is_error": r.IsError,
		"output":   json.RawMessage(r.Output),
	})
	if err == nil {
		e.chat.recordToolCompletion(r)
		e.emit(Event{Kind: EventConversationUpdated, StepID: stepID})
	}
	return err
}

func (e *Engine) appendUserMessage(stepID, text string) error {
	msg := llm.Message{Role: llm.RoleUser, Content: text}
	return e.appendMessage(stepID, msg)
}

func (e *Engine) appendUserMessageWithoutConversationUpdate(stepID, text string) error {
	msg := llm.Message{Role: llm.RoleUser, Content: text}
	return e.appendMessageWithoutConversationUpdate(stepID, msg)
}

func (e *Engine) injectHeadlessModeTransitionPromptIfNeeded(stepID string) error {
	messages := e.snapshotMessages()
	if e.cfg.HeadlessMode {
		if !shouldInjectHeadlessModePrompt(messages) {
			return nil
		}
		return e.appendMessage(stepID, llm.Message{
			Role:        llm.RoleDeveloper,
			MessageType: llm.MessageTypeHeadlessMode,
			Content:     strings.TrimSpace(prompts.HeadlessModePrompt),
		})
	}
	if !shouldInjectHeadlessModeExitPrompt(messages) {
		return nil
	}
	return e.appendMessage(stepID, llm.Message{
		Role:        llm.RoleDeveloper,
		MessageType: llm.MessageTypeHeadlessModeExit,
		Content:     strings.TrimSpace(prompts.HeadlessModeExitPrompt),
	})
}

func shouldInjectHeadlessModePrompt(messages []llm.Message) bool {
	return !headlessModeActive(messages)
}

func shouldInjectHeadlessModeExitPrompt(messages []llm.Message) bool {
	return headlessModeActive(messages)
}

func headlessModeActive(messages []llm.Message) bool {
	active := false
	for _, msg := range messages {
		if msg.Role != llm.RoleDeveloper {
			continue
		}
		if msg.MessageType == llm.MessageTypeHeadlessMode {
			active = true
			continue
		}
		if msg.MessageType == llm.MessageTypeHeadlessModeExit {
			active = false
		}
	}
	return active
}

func (e *Engine) appendAssistantMessage(stepID string, msg llm.Message) error {
	return e.appendMessage(stepID, msg)
}

func (e *Engine) appendReasoningEntries(stepID string, entries []llm.ReasoningEntry) error {
	for _, entry := range entries {
		if err := e.appendPersistedLocalEntry(stepID, entry.Role, entry.Text); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) appendPersistedLocalEntry(stepID, role, text string) error {
	return e.appendPersistedLocalEntryWithOngoingText(stepID, role, text, "")
}

func (e *Engine) appendPersistedLocalEntryWithOngoingText(stepID, role, text, ongoingText string) error {
	role = strings.TrimSpace(role)
	if role == "" || strings.TrimSpace(text) == "" {
		return nil
	}
	e.chat.appendLocalEntryWithOngoingText(role, text, ongoingText)
	_, err := e.store.AppendEvent(stepID, "local_entry", storedLocalEntry{
		Role:        role,
		Text:        text,
		OngoingText: strings.TrimSpace(ongoingText),
	})
	if err == nil {
		e.emit(Event{Kind: EventConversationUpdated, StepID: stepID})
	}
	return err
}

func (e *Engine) appendMessage(stepID string, msg llm.Message) error {
	e.chat.appendMessage(msg)
	_, err := e.store.AppendEvent(stepID, "message", msg)
	if err == nil {
		e.emit(Event{Kind: EventConversationUpdated, StepID: stepID})
	}
	return err
}

func (e *Engine) appendMessageWithoutConversationUpdate(stepID string, msg llm.Message) error {
	e.chat.appendMessage(msg)
	_, err := e.store.AppendEvent(stepID, "message", msg)
	return err
}

func (e *Engine) clearStreamingAssistantState(stepID string) {
	e.chat.clearOngoing()
	e.chat.clearOngoingError()
	e.emit(Event{Kind: EventConversationUpdated, StepID: stepID})
	e.emit(Event{Kind: EventAssistantDeltaReset, StepID: stepID})
	e.emit(Event{Kind: EventReasoningDeltaReset, StepID: stepID})
}

func (e *Engine) flushPendingUserInjections(stepID string) (int, error) {
	e.ensureOrchestrationCollaborators()
	return e.messageFlow.FlushPendingUserInjections(stepID)
}

func (e *Engine) injectAgentsIfNeeded(stepID string) error {
	e.ensureOrchestrationCollaborators()
	return e.messageFlow.InjectAgentsIfNeeded(stepID)
}

func agentsInjectionPaths(workspaceRoot string) ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home dir: %w", err)
	}

	paths := make([]string, 0, 2)
	seen := map[string]bool{}
	addPath := func(path string) {
		cleaned := filepath.Clean(path)
		if cleaned == "" || seen[cleaned] {
			return
		}
		seen[cleaned] = true
		paths = append(paths, cleaned)
	}

	addPath(filepath.Join(home, agentsGlobalDirName, agentsFileName))
	addPath(filepath.Join(workspaceRoot, agentsFileName))
	return paths, nil
}

func environmentContextMessage(workspaceRoot string, model string, thinkingLevel string, now time.Time) string {
	cwd, err := os.Getwd()
	if err != nil || strings.TrimSpace(cwd) == "" {
		cwd = strings.TrimSpace(workspaceRoot)
	}
	if strings.TrimSpace(cwd) == "" {
		cwd = "unknown"
	}

	shell := shellEnvironmentName()
	if strings.TrimSpace(shell) == "" {
		shell = "unknown"
	}

	osName := strings.TrimSpace(goruntime.GOOS)
	if osName == "" {
		osName = "unknown"
	}

	cpuArch := strings.TrimSpace(goruntime.GOARCH)
	if strings.TrimSpace(cpuArch) == "" {
		cpuArch = "unknown"
	}

	tzName, tzOffset := now.Zone()
	tzName = strings.TrimSpace(tzName)
	if tzName == "" {
		tzName = strings.TrimSpace(now.Location().String())
	}
	if tzName == "" {
		tzName = "unknown"
	}

	return strings.Join([]string{
		environmentInjectedHeader,
		llm.ModelDisplayLabel(model, thinkingLevel),
		fmt.Sprintf("OS: %s", osName),
		fmt.Sprintf("Current TZ: %s (UTC%s)", tzName, formatUTCOffset(tzOffset)),
		fmt.Sprintf("Date/time: %s", now.Format(time.RFC3339)),
		fmt.Sprintf("Shell: %s", shell),
		fmt.Sprintf("CWD: %s", cwd),
		fmt.Sprintf("CPU arch: %s", cpuArch),
	}, "\n")
}

func shellEnvironmentName() string {
	for _, key := range []string{"SHELL", "COMSPEC"} {
		value := strings.TrimSpace(os.Getenv(key))
		if value == "" {
			continue
		}
		base := filepath.Base(value)
		if base == "" || base == "." || base == string(filepath.Separator) {
			return value
		}
		return base
	}
	return ""
}

func formatUTCOffset(offsetSeconds int) string {
	sign := "+"
	if offsetSeconds < 0 {
		sign = "-"
		offsetSeconds = -offsetSeconds
	}
	hours := offsetSeconds / 3600
	minutes := (offsetSeconds % 3600) / 60
	return fmt.Sprintf("%s%02d:%02d", sign, hours, minutes)
}

func (e *Engine) restoreMessages() error {
	e.ensureOrchestrationCollaborators()
	return e.messageFlow.RestoreMessages()
}
