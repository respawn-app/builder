package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"builder/server/runtime"
)

const runLogFileName = "steps.log"

type runLogger struct {
	mu                   sync.Mutex
	fp                   writeStringCloser
	onDiagnostic         func(runLoggerDiagnostic)
	reportedWriteFailure bool
}

type writeStringCloser interface {
	WriteString(string) (int, error)
	Close() error
}

type runLoggerDiagnostic struct {
	Kind    string
	Message string
	Err     error
}

func newRunLogger(sessionDir string, onDiagnostic func(runLoggerDiagnostic)) (*runLogger, error) {
	fp, err := os.OpenFile(filepath.Join(sessionDir, runLogFileName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &runLogger{onDiagnostic: onDiagnostic}, nil
		}
		return nil, fmt.Errorf("open run log: %w", err)
	}
	return &runLogger{fp: fp, onDiagnostic: onDiagnostic}, nil
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
	if _, err := l.fp.WriteString(stamp + " " + line + "\n"); err != nil && !l.reportedWriteFailure {
		l.reportedWriteFailure = true
		if l.onDiagnostic != nil {
			l.onDiagnostic(runLoggerDiagnostic{
				Kind:    "write_failed",
				Message: fmt.Sprintf("run log write failed; observability degraded: %v", err),
				Err:     err,
			})
		}
	}
}

func reportRunLoggerDiagnostic(w io.Writer, diag runLoggerDiagnostic) {
	if w == nil {
		return
	}
	_, _ = fmt.Fprintln(w, formatRunLoggerDiagnostic(diag))
}

func formatRunLoggerDiagnostic(diag runLoggerDiagnostic) string {
	message := strings.TrimSpace(diag.Message)
	if message == "" {
		message = "run logger diagnostic"
	}
	parts := []string{"run_logger.diagnostic"}
	if kind := strings.TrimSpace(diag.Kind); kind != "" {
		parts = append(parts, fmt.Sprintf("kind=%s", kind))
	}
	parts = append(parts, fmt.Sprintf("message=%q", message))
	if diag.Err != nil {
		parts = append(parts, fmt.Sprintf("err=%q", diag.Err.Error()))
	}
	return strings.Join(parts, " ")
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
	case runtime.EventRunStateChanged:
		if evt.RunState != nil {
			return fmt.Sprintf("runtime.event kind=%s step_id=%s busy=%t", evt.Kind, evt.StepID, evt.RunState.Busy)
		}
	case runtime.EventBackgroundUpdated:
		if evt.Background != nil {
			line := fmt.Sprintf("runtime.event kind=%s id=%s type=%s state=%s", evt.Kind, evt.Background.ID, evt.Background.Type, evt.Background.State)
			if evt.Background.ExitCode != nil {
				line += fmt.Sprintf(" exit_code=%d", *evt.Background.ExitCode)
			}
			return line
		}
	}
	return fmt.Sprintf("runtime.event kind=%s step_id=%s", evt.Kind, evt.StepID)
}
