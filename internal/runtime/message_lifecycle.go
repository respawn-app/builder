package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"builder/internal/llm"
)

type defaultMessageLifecycle struct {
	engine *Engine
}

func (m *defaultMessageLifecycle) RestoreMessages() error {
	e := m.engine
	events, err := e.store.ReadEvents()
	if err != nil {
		return err
	}
	for _, evt := range events {
		switch evt.Kind {
		case "message":
			var msg llm.Message
			if err := json.Unmarshal(evt.Payload, &msg); err != nil {
				return fmt.Errorf("decode message event: %w", err)
			}
			e.chat.appendMessage(msg)
		case "tool_completed":
			if err := e.chat.restoreToolCompletionPayload(evt.Payload); err != nil {
				return err
			}
		case "local_entry":
			var entry storedLocalEntry
			if err := json.Unmarshal(evt.Payload, &entry); err != nil {
				return fmt.Errorf("decode local_entry event: %w", err)
			}
			e.chat.appendLocalEntry(entry.Role, entry.Text)
		case "history_replaced":
			var payload historyReplacementPayload
			if err := json.Unmarshal(evt.Payload, &payload); err != nil {
				return fmt.Errorf("decode history_replaced event: %w", err)
			}
			if strings.TrimSpace(payload.Engine) == "reviewer_rollback" {
				e.chat.restoreMessagesFromItems(payload.Items)
			} else {
				e.chat.replaceHistory(payload.Items)
				e.compactionCount++
			}
		}
	}
	e.chat.clearActivity()
	return nil
}

func (m *defaultMessageLifecycle) FlushPendingUserInjections(stepID string) (int, error) {
	e := m.engine
	e.mu.Lock()
	pending := append([]string(nil), e.pendingInjected...)
	e.pendingInjected = nil
	e.mu.Unlock()
	flushed := 0

	for _, pendingMessage := range pending {
		if err := e.appendUserMessage(stepID, pendingMessage); err != nil {
			return flushed, err
		}
		flushed++
		e.emit(Event{Kind: EventUserMessageFlushed, StepID: stepID, UserMessage: pendingMessage})
	}
	return flushed, nil
}

func (m *defaultMessageLifecycle) InjectAgentsIfNeeded(stepID string) error {
	e := m.engine
	meta := e.store.Meta()
	if meta.AgentsInjected {
		return nil
	}
	paths, err := agentsInjectionPaths(meta.WorkspaceRoot)
	if err != nil {
		return err
	}

	for _, path := range paths {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			if errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("read AGENTS.md: %w", readErr)
		}
		injected := fmt.Sprintf("%s\nsource: %s\n\n```%s\n%s\n```", agentsInjectedHeader, path, agentsInjectedFenceLabel, string(data))
		if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeAgentsMD, Content: injected}); err != nil {
			return err
		}
	}
	skills, found, err := skillsContextMessage(meta.WorkspaceRoot)
	if err != nil {
		return err
	}
	if found {
		if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeSkills, Content: skills}); err != nil {
			return err
		}
	}
	environment := environmentContextMessage(meta.WorkspaceRoot, e.cfg.Model, e.ThinkingLevel(), time.Now())
	if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeEnvironment, Content: environment}); err != nil {
		return err
	}

	return e.store.MarkAgentsInjected()
}
