package runtime

import (
	"builder/internal/llm"
	"builder/internal/tools"
)

type EventKind string

const (
	EventAssistantDelta      EventKind = "assistant_delta"
	EventAssistantDeltaReset EventKind = "assistant_delta_reset"
	EventAssistantMessage    EventKind = "assistant_message"
	EventToolCallStarted     EventKind = "tool_call_started"
	EventToolCallCompleted   EventKind = "tool_call_completed"
)

type Event struct {
	Kind           EventKind
	StepID         string
	AssistantDelta string
	Message        llm.Message
	ToolCall       *llm.ToolCall
	ToolResult     *tools.Result
}
