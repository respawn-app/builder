package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"builder/internal/llm"
	"builder/internal/tools"
)

type defaultBackgroundNoticeScheduler struct {
	engine *Engine
	steps  exclusiveStepLifecycle

	mu        sync.Mutex
	pending   []llm.Message
	scheduled bool
}

func (e *Engine) HandleBackgroundShellEvent(evt BackgroundShellEvent) {
	e.HandleBackgroundShellUpdate(evt, true)
}

func (e *Engine) HandleBackgroundShellUpdate(evt BackgroundShellEvent, queueNotice bool) {
	e.ensureOrchestrationCollaborators()
	e.backgroundFlow.HandleBackgroundShellUpdate(evt, queueNotice)
}

func (b *defaultBackgroundNoticeScheduler) HandleBackgroundShellUpdate(evt BackgroundShellEvent, queueNotice bool) {
	b.engine.emit(Event{Kind: EventBackgroundUpdated, Background: &evt})
	if !queueNotice {
		return
	}
	if evt.Type != "completed" && evt.Type != "killed" {
		return
	}
	b.QueueDeveloperNotice(llm.Message{
		Role:           llm.RoleDeveloper,
		MessageType:    llm.MessageTypeBackgroundNotice,
		Name:           strings.TrimSpace(evt.ID),
		Content:        formatBackgroundShellNotice(evt),
		CompactContent: formatBackgroundShellCompact(evt),
	})
}

func formatBackgroundShellNotice(evt BackgroundShellEvent) string {
	if strings.TrimSpace(evt.NoticeText) != "" {
		return strings.TrimSpace(evt.NoticeText)
	}
	parts := []string{fmt.Sprintf("Background shell %s %s.", evt.ID, evt.State)}
	if code := evt.ExitCode; code != nil {
		parts = append(parts, fmt.Sprintf("Exit code: %d", *code))
	}
	preview := strings.TrimSpace(evt.Preview)
	if preview != "" {
		parts = append(parts, "Output:")
		parts = append(parts, preview)
	} else {
		parts = append(parts, "No output")
	}
	return strings.Join(parts, "\n")
}

func formatBackgroundShellCompact(evt BackgroundShellEvent) string {
	if strings.TrimSpace(evt.CompactText) != "" {
		return strings.TrimSpace(evt.CompactText)
	}
	text := fmt.Sprintf("Background shell %s %s", evt.ID, evt.State)
	if code := evt.ExitCode; code != nil {
		text = fmt.Sprintf("%s (exit %d)", text, *code)
	}
	return text
}

func (b *defaultBackgroundNoticeScheduler) QueueDeveloperNotice(msg llm.Message) {
	if strings.TrimSpace(msg.Content) == "" {
		return
	}
	shouldSchedule := false
	b.mu.Lock()
	b.pending = append(b.pending, msg)
	if !b.scheduled && (b.steps == nil || !b.steps.IsBusy()) {
		b.scheduled = true
		shouldSchedule = true
	}
	b.mu.Unlock()
	if shouldSchedule {
		go b.processQueuedNotices(context.Background())
	}
}

func (b *defaultBackgroundNoticeScheduler) DrainPendingNotices() []llm.Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pending) == 0 {
		b.scheduled = false
		return nil
	}
	pending := append([]llm.Message(nil), b.pending...)
	b.pending = nil
	b.scheduled = false
	return pending
}

func (b *defaultBackgroundNoticeScheduler) ConsumePendingBackgroundNotice(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	removed := false
	filtered := b.pending[:0]
	for _, msg := range b.pending {
		if msg.Role == llm.RoleDeveloper && msg.MessageType == llm.MessageTypeBackgroundNotice && strings.TrimSpace(msg.Name) == sessionID {
			removed = true
			continue
		}
		filtered = append(filtered, msg)
	}
	b.pending = filtered
	if len(b.pending) == 0 {
		b.scheduled = false
	}
	return removed
}

func (b *defaultBackgroundNoticeScheduler) ScheduleIfIdle() {
	if b.steps != nil && b.steps.IsBusy() {
		return
	}
	shouldSchedule := false
	b.mu.Lock()
	if len(b.pending) > 0 && !b.scheduled {
		b.scheduled = true
		shouldSchedule = true
	}
	b.mu.Unlock()
	if shouldSchedule {
		go b.processQueuedNotices(context.Background())
	}
}

type harvestedBackgroundCompletion struct {
	SessionID  int  `json:"background_session_id"`
	Running    bool `json:"background_running"`
	Background bool `json:"backgrounded"`
}

func harvestedBackgroundCompletionSessionID(res tools.Result) (string, bool) {
	if res.IsError || res.Name != tools.ToolWriteStdin {
		return "", false
	}
	var out harvestedBackgroundCompletion
	if err := json.Unmarshal(res.Output, &out); err != nil {
		return "", false
	}
	if out.SessionID <= 0 || out.Running || !out.Background {
		return "", false
	}
	return fmt.Sprintf("%d", out.SessionID), true
}

func (b *defaultBackgroundNoticeScheduler) processQueuedNotices(ctx context.Context) {
	if _, err := b.runQueuedNotices(ctx); err != nil {
		b.engine.AppendLocalEntry("error", fmt.Sprintf("background continuation failed: %v", err))
	}
}

func (b *defaultBackgroundNoticeScheduler) runQueuedNotices(ctx context.Context) (assistant llm.Message, err error) {
	if len(b.pendingSnapshot()) == 0 {
		b.clearScheduled()
		return llm.Message{}, nil
	}
	err = b.steps.Run(ctx, exclusiveStepOptions{EmitRunState: true}, func(stepCtx context.Context, stepID string) error {
		pending := b.DrainPendingNotices()
		if len(pending) == 0 {
			return nil
		}
		if err := b.engine.injectAgentsIfNeeded(stepID); err != nil {
			return err
		}
		for _, msg := range pending {
			if err := b.engine.appendMessage(stepID, msg); err != nil {
				return err
			}
		}
		msg, runErr := b.engine.runStepLoop(stepCtx, stepID)
		assistant = msg
		return runErr
	})
	if errors.Is(err, errExclusiveStepBusy) {
		b.clearScheduled()
		return llm.Message{}, nil
	}
	return assistant, err
}

func (b *defaultBackgroundNoticeScheduler) pendingSnapshot() []llm.Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]llm.Message(nil), b.pending...)
}

func (b *defaultBackgroundNoticeScheduler) clearScheduled() {
	b.mu.Lock()
	b.scheduled = false
	b.mu.Unlock()
}
