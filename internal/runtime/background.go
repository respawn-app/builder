package runtime

import (
	"context"
	"fmt"
	"strings"

	"builder/internal/llm"
	"github.com/google/uuid"
)

func (e *Engine) HandleBackgroundShellEvent(evt BackgroundShellEvent) {
	e.HandleBackgroundShellUpdate(evt, true)
}

func (e *Engine) HandleBackgroundShellUpdate(evt BackgroundShellEvent, queueNotice bool) {
	e.emit(Event{Kind: EventBackgroundUpdated, Background: &evt})
	if !queueNotice {
		return
	}
	if evt.Type != "completed" && evt.Type != "killed" {
		return
	}
	e.queueDeveloperNotice(llm.Message{
		Role:           llm.RoleDeveloper,
		MessageType:    llm.MessageTypeBackgroundNotice,
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
		parts = append(parts, "no output")
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

func (e *Engine) queueDeveloperNotice(msg llm.Message) {
	if strings.TrimSpace(msg.Content) == "" {
		return
	}
	shouldSchedule := false
	e.mu.Lock()
	e.pendingNotices = append(e.pendingNotices, msg)
	if !e.busy && !e.noticeScheduled {
		e.noticeScheduled = true
		shouldSchedule = true
	}
	e.mu.Unlock()
	if shouldSchedule {
		go e.processQueuedNotices(context.Background())
	}
}

func (e *Engine) processQueuedNotices(ctx context.Context) {
	defer func() {
		shouldSchedule := false
		e.mu.Lock()
		if !e.busy {
			e.noticeScheduled = false
			if len(e.pendingNotices) > 0 {
				e.noticeScheduled = true
				shouldSchedule = true
			}
		}
		e.mu.Unlock()
		if shouldSchedule {
			go e.processQueuedNotices(context.Background())
		}
	}()
	if _, err := e.runQueuedNotices(ctx); err != nil {
		e.AppendLocalEntry("error", fmt.Sprintf("background continuation failed: %v", err))
	}
}

func (e *Engine) runQueuedNotices(ctx context.Context) (llm.Message, error) {
	e.mu.Lock()
	if e.busy || len(e.pendingNotices) == 0 {
		e.noticeScheduled = false
		e.mu.Unlock()
		return llm.Message{}, nil
	}
	e.busy = true
	stepCtx, cancel := context.WithCancel(ctx)
	e.cancelCurrent = cancel
	pending := append([]llm.Message(nil), e.pendingNotices...)
	e.pendingNotices = nil
	e.mu.Unlock()
	e.emit(Event{Kind: EventRunStateChanged, RunState: &RunState{Busy: true}})
	stepID := ""
	var err error
	defer func() {
		e.mu.Lock()
		e.busy = false
		e.cancelCurrent = nil
		e.mu.Unlock()
		e.emit(Event{Kind: EventRunStateChanged, StepID: stepID, RunState: &RunState{Busy: false}})
		if clearErr := e.store.MarkInFlight(false); clearErr != nil {
			wrapped := fmt.Errorf("mark in-flight false: %w", clearErr)
			e.emit(Event{Kind: EventInFlightClearFailed, StepID: stepID, Error: wrapped.Error()})
			err = wrapped
		}
	}()
	if err = e.store.MarkInFlight(true); err != nil {
		return llm.Message{}, err
	}
	stepID = uuid.NewString()
	if err = e.injectAgentsIfNeeded(stepID); err != nil {
		return llm.Message{}, err
	}
	for _, msg := range pending {
		if err = e.appendMessage(stepID, msg); err != nil {
			return llm.Message{}, err
		}
	}
	assistant, runErr := e.runStepLoop(stepCtx, stepID)
	if runErr != nil {
		return llm.Message{}, runErr
	}
	return assistant, err
}
