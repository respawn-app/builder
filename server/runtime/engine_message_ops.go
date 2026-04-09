package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"builder/server/llm"
	"builder/server/tools"
	"builder/shared/transcript"
)

func (e *Engine) persistToolCompletion(stepID string, r tools.Result) error {
	if sessionID, ok := harvestedBackgroundCompletionSessionID(r); ok {
		e.ensureOrchestrationCollaborators()
		e.backgroundFlow.ConsumePendingBackgroundNotice(sessionID)
	}
	_, err := e.store.AppendEvent(stepID, "tool_completed", map[string]any{
		"call_id":  r.CallID,
		"name":     string(r.Name),
		"is_error": r.IsError,
		"output":   json.RawMessage(r.Output),
	})
	if err == nil {
		e.markCurrentRequestShapeDirtyForSignificantMutation()
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
	builder := newMetaContextBuilder(e.store.Meta().WorkspaceRoot, e.cfg.Model, e.ThinkingLevel(), e.cfg.DisabledSkills, time.Now())
	if e.cfg.HeadlessMode {
		if !shouldInjectHeadlessModePrompt(messages) {
			return nil
		}
		metaResult, err := builder.Build(metaContextBuildOptions{IncludeHeadless: true})
		if err != nil {
			return err
		}
		if len(metaResult.Headless) == 0 {
			return nil
		}
		return e.appendMessage(stepID, metaResult.Headless[0])
	}
	if !shouldInjectHeadlessModeExitPrompt(messages) {
		return nil
	}
	metaResult, err := builder.Build(metaContextBuildOptions{IncludeHeadlessExit: true})
	if err != nil {
		return err
	}
	if len(metaResult.HeadlessExit) == 0 {
		return nil
	}
	return e.appendMessage(stepID, metaResult.HeadlessExit[0])
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
	return e.appendPersistedLocalEntryRecord(stepID, storedLocalEntry{
		Visibility:  transcript.EntryVisibilityAuto,
		Role:        role,
		Text:        text,
		OngoingText: strings.TrimSpace(ongoingText),
	})
}

func (e *Engine) appendPersistedDiagnosticEntry(stepID, diagnosticKey, role, text string) error {
	diagnosticKey = strings.TrimSpace(diagnosticKey)
	if diagnosticKey == "" {
		return e.appendPersistedLocalEntry(stepID, role, text)
	}
	if !e.beginLocalDiagnostic(diagnosticKey) {
		return nil
	}
	entry := storedLocalEntry{
		Visibility:    transcript.EntryVisibilityAuto,
		Role:          role,
		Text:          text,
		DiagnosticKey: diagnosticKey,
	}
	entry.Role = strings.TrimSpace(entry.Role)
	entry.Text = strings.TrimSpace(entry.Text)
	entry.DiagnosticKey = strings.TrimSpace(entry.DiagnosticKey)
	if entry.Role == "" || entry.Text == "" {
		e.clearLocalDiagnostic(diagnosticKey)
		return nil
	}
	_, err := e.store.AppendEvent(stepID, "local_entry", entry)
	if err != nil {
		e.clearLocalDiagnostic(diagnosticKey)
		return err
	}
	e.chat.appendLocalEntryWithOngoingText(entry.Role, entry.Text, entry.OngoingText)
	e.emit(Event{Kind: EventLocalEntryAdded, StepID: stepID, LocalEntry: localEntryChatEntry(entry)})
	e.emit(Event{Kind: EventConversationUpdated, StepID: stepID})
	return nil
}

func (e *Engine) appendPersistedLocalEntryRecord(stepID string, entry storedLocalEntry) error {
	entry.Role = strings.TrimSpace(entry.Role)
	entry.Text = strings.TrimSpace(entry.Text)
	entry.OngoingText = strings.TrimSpace(entry.OngoingText)
	entry.DiagnosticKey = strings.TrimSpace(entry.DiagnosticKey)
	if entry.Role == "" || entry.Text == "" {
		return nil
	}
	_, err := e.store.AppendEvent(stepID, "local_entry", entry)
	if err == nil {
		e.chat.appendLocalEntryWithOngoingTextAndVisibility(entry.Role, entry.Text, entry.OngoingText, entry.Visibility)
		e.emit(Event{Kind: EventLocalEntryAdded, StepID: stepID, LocalEntry: localEntryChatEntry(entry)})
		e.emit(Event{Kind: EventConversationUpdated, StepID: stepID})
	}
	return err
}

func localEntryChatEntry(entry storedLocalEntry) *ChatEntry {
	return &ChatEntry{
		Visibility:  entry.Visibility,
		Role:        strings.TrimSpace(entry.Role),
		Text:        strings.TrimSpace(entry.Text),
		OngoingText: strings.TrimSpace(entry.OngoingText),
	}
}

func (e *Engine) beginLocalDiagnostic(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.localDiagnosticKeys[key]; exists {
		return false
	}
	e.localDiagnosticKeys[key] = struct{}{}
	return true
}

func (e *Engine) clearLocalDiagnostic(key string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.localDiagnosticKeys, key)
	delete(e.persistedDiagnostics, key)
}

func (e *Engine) restoreLocalDiagnostic(key string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.localDiagnosticKeys[key] = struct{}{}
	e.persistedDiagnostics[key] = struct{}{}
}

func (e *Engine) hasPersistedDiagnostic(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_, exists := e.persistedDiagnostics[key]
	return exists
}

func (e *Engine) resetLocalDiagnostics() {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.localDiagnosticKeys = make(map[string]struct{})
	e.persistedDiagnostics = make(map[string]struct{})
}

func (e *Engine) appendMessage(stepID string, msg llm.Message) error {
	msg = normalizeMessageForTranscript(msg, e.store.Meta().WorkspaceRoot)
	if e.beforePersistMessage != nil {
		if err := e.beforePersistMessage(msg); err != nil {
			return err
		}
	}
	if mutation := tokenUsageMutationForMessage(msg); mutation == tokenUsageMutationSignificant {
		e.markCurrentRequestShapeDirtyForSignificantMutation()
	} else {
		e.markCurrentRequestShapeDirty()
	}
	e.chat.appendMessage(msg)
	_, err := e.store.AppendEvent(stepID, "message", msg)
	if err == nil {
		e.emit(Event{Kind: EventConversationUpdated, StepID: stepID})
	}
	return err
}

func (e *Engine) appendMessageWithoutConversationUpdate(stepID string, msg llm.Message) error {
	msg = normalizeMessageForTranscript(msg, e.store.Meta().WorkspaceRoot)
	if e.beforePersistMessage != nil {
		if err := e.beforePersistMessage(msg); err != nil {
			return err
		}
	}
	if mutation := tokenUsageMutationForMessage(msg); mutation == tokenUsageMutationSignificant {
		e.markCurrentRequestShapeDirtyForSignificantMutation()
	} else {
		e.markCurrentRequestShapeDirty()
	}
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

func flushedUserMessageEvent(msg llm.Message, stepID string) *Event {
	if msg.Role != llm.RoleUser {
		return nil
	}
	if msg.MessageType == llm.MessageTypeCompactionSummary {
		return nil
	}
	if strings.TrimSpace(msg.Content) == "" {
		return nil
	}
	return &Event{Kind: EventUserMessageFlushed, StepID: stepID, UserMessage: msg.Content, UserMessageBatch: []string{msg.Content}}
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

func environmentContextMessage(workspaceRoot string, model string, now time.Time) (string, error) {
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

	modelLine, err := environmentModelContextLine(model)
	if err != nil {
		return "", err
	}

	return strings.Join([]string{
		environmentInjectedHeader,
		modelLine,
		fmt.Sprintf("OS: %s", osName),
		fmt.Sprintf("Current TZ: %s (UTC%s)", tzName, formatUTCOffset(tzOffset)),
		fmt.Sprintf("Date/time: %s", now.Format(time.RFC3339)),
		fmt.Sprintf("Shell: %s", shell),
		fmt.Sprintf("CWD: %s", cwd),
		fmt.Sprintf("CPU arch: %s", cpuArch),
	}, "\n"), nil
}

func environmentModelContextLine(model string) (string, error) {
	normalized := strings.TrimSpace(model)
	if normalized == "" {
		return "", errors.New("environment context requires a model")
	}
	return fmt.Sprintf("Your model: %s", normalized), nil
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
