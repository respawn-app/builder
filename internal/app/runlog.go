package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"builder/internal/runtime"
)

const runLogFileName = "steps.log"

type runLogger struct {
	mu sync.Mutex
	fp *os.File
}

func newRunLogger(sessionDir string) (*runLogger, error) {
	fp, err := os.OpenFile(filepath.Join(sessionDir, runLogFileName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &runLogger{}, nil
		}
		return nil, fmt.Errorf("open run log: %w", err)
	}
	return &runLogger{fp: fp}, nil
}

func (l *runLogger) Close() error {
	if l == nil || l.fp == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fp.Close()
}

func (l *runLogger) Logf(format string, args ...any) {
	if l == nil || l.fp == nil {
		return
	}
	line := fmt.Sprintf(format, args...)
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return
	}

	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.fp.WriteString(stamp + " " + line + "\n")
}

func formatRuntimeEvent(evt runtime.Event) string {
	switch evt.Kind {
	case runtime.EventAssistantDelta:
		return fmt.Sprintf("runtime.event kind=%s step_id=%s delta_chars=%d", evt.Kind, evt.StepID, len(evt.AssistantDelta))
	case runtime.EventAssistantDeltaReset:
		return fmt.Sprintf("runtime.event kind=%s step_id=%s", evt.Kind, evt.StepID)
	case runtime.EventAssistantMessage:
		return fmt.Sprintf("runtime.event kind=%s step_id=%s message_chars=%d", evt.Kind, evt.StepID, len(evt.Message.Content))
	case runtime.EventModelResponse:
		if evt.ModelResponse != nil {
			return fmt.Sprintf(
				"runtime.event kind=%s step_id=%s phase=%s assistant_chars=%d tool_calls=%d output_items=%d output_types=%q",
				evt.Kind,
				evt.StepID,
				evt.ModelResponse.AssistantPhase,
				evt.ModelResponse.AssistantChars,
				evt.ModelResponse.ToolCallsCount,
				evt.ModelResponse.OutputItemsCount,
				strings.Join(evt.ModelResponse.OutputItemTypes, ","),
			)
		}
	case runtime.EventUserMessageFlushed:
		return fmt.Sprintf("runtime.event kind=%s step_id=%s user_chars=%d", evt.Kind, evt.StepID, len(evt.UserMessage))
	case runtime.EventToolCallStarted:
		if evt.ToolCall != nil {
			return fmt.Sprintf("runtime.event kind=%s step_id=%s call_id=%s name=%s", evt.Kind, evt.StepID, evt.ToolCall.ID, evt.ToolCall.Name)
		}
	case runtime.EventToolCallCompleted:
		if evt.ToolResult != nil {
			return fmt.Sprintf("runtime.event kind=%s step_id=%s call_id=%s name=%s is_error=%t", evt.Kind, evt.StepID, evt.ToolResult.CallID, evt.ToolResult.Name, evt.ToolResult.IsError)
		}
	case runtime.EventReviewerCompleted:
		if evt.Reviewer != nil {
			line := fmt.Sprintf(
				"runtime.event kind=%s step_id=%s outcome=%s suggestions=%d",
				evt.Kind,
				evt.StepID,
				evt.Reviewer.Outcome,
				evt.Reviewer.SuggestionsCount,
			)
			if strings.TrimSpace(evt.Reviewer.Error) != "" {
				line += fmt.Sprintf(" err=%q", evt.Reviewer.Error)
			}
			return line
		}
	case runtime.EventInFlightClearFailed:
		if strings.TrimSpace(evt.Error) != "" {
			return fmt.Sprintf("runtime.event kind=%s step_id=%s err=%q", evt.Kind, evt.StepID, evt.Error)
		}
	case runtime.EventCompactionStarted, runtime.EventCompactionCompleted, runtime.EventCompactionFailed:
		if evt.Compaction != nil {
			line := fmt.Sprintf(
				"runtime.event kind=%s step_id=%s mode=%s engine=%s provider=%s trimmed=%d count=%d",
				evt.Kind,
				evt.StepID,
				evt.Compaction.Mode,
				evt.Compaction.Engine,
				evt.Compaction.Provider,
				evt.Compaction.TrimmedItemsCount,
				evt.Compaction.Count,
			)
			if strings.TrimSpace(evt.Compaction.Error) != "" {
				line += fmt.Sprintf(" err=%q", evt.Compaction.Error)
			}
			return line
		}
	}
	return fmt.Sprintf("runtime.event kind=%s step_id=%s", evt.Kind, evt.StepID)
}
