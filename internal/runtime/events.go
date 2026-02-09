package runtime

import (
	"builder/internal/llm"
	"builder/internal/tools"
)

type EventKind string

const (
	EventConversationUpdated EventKind = "conversation_updated"
	EventAssistantDelta      EventKind = "assistant_delta"
	EventAssistantDeltaReset EventKind = "assistant_delta_reset"
	EventAssistantMessage    EventKind = "assistant_message"
	EventUserMessageFlushed  EventKind = "user_message_flushed"
	EventToolCallStarted     EventKind = "tool_call_started"
	EventToolCallCompleted   EventKind = "tool_call_completed"
	EventInFlightClearFailed EventKind = "in_flight_clear_failed"
	EventCompactionStarted   EventKind = "context_compaction_started"
	EventCompactionCompleted EventKind = "context_compaction_completed"
	EventCompactionFailed    EventKind = "context_compaction_failed"
)

type Event struct {
	Kind           EventKind
	StepID         string
	Error          string
	AssistantDelta string
	UserMessage    string
	Message        llm.Message
	ToolCall       *llm.ToolCall
	ToolResult     *tools.Result
	Compaction     *CompactionStatus
}

type CompactionStatus struct {
	Mode              string
	Engine            string
	Provider          string
	TrimmedItemsCount int
	Count             int
	Error             string
}
