package clientui

import "builder/shared/transcript"

type EventKind string

const (
	EventConversationUpdated EventKind = "conversation_updated"
	EventAssistantDelta      EventKind = "assistant_delta"
	EventAssistantDeltaReset EventKind = "assistant_delta_reset"
	EventReasoningDelta      EventKind = "reasoning_delta"
	EventReasoningDeltaReset EventKind = "reasoning_delta_reset"
	EventAssistantMessage    EventKind = "assistant_message"
	EventModelResponse       EventKind = "model_response_received"
	EventUserMessageFlushed  EventKind = "user_message_flushed"
	EventToolCallStarted     EventKind = "tool_call_started"
	EventToolCallCompleted   EventKind = "tool_call_completed"
	EventReviewerStarted     EventKind = "reviewer_started"
	EventReviewerCompleted   EventKind = "reviewer_completed"
	EventInFlightClearFailed EventKind = "in_flight_clear_failed"
	EventCompactionStarted   EventKind = "context_compaction_started"
	EventCompactionCompleted EventKind = "context_compaction_completed"
	EventCompactionFailed    EventKind = "context_compaction_failed"
	EventRunStateChanged     EventKind = "run_state_changed"
	EventBackgroundUpdated   EventKind = "background_updated"
)

type Event struct {
	Kind             EventKind
	StepID           string
	Error            string
	AssistantDelta   string
	ReasoningDelta   *ReasoningDelta
	UserMessage      string
	UserMessageBatch []string
	RunState         *RunState
	Background       *BackgroundShellEvent
}

type ReasoningDelta struct {
	Key  string
	Role string
	Text string
}

type RunState struct {
	Busy bool
}

type BackgroundShellEvent struct {
	Type              string
	ID                string
	State             string
	Command           string
	Workdir           string
	LogPath           string
	NoticeText        string
	CompactText       string
	Preview           string
	Removed           int
	ExitCode          *int
	UserRequestedKill bool
	NoticeSuppressed  bool
}

type ChatEntry struct {
	Role        string
	Text        string
	OngoingText string
	Phase       string
	ToolCallID  string
	ToolCall    *transcript.ToolCallMeta
}

type ChatSnapshot struct {
	Entries      []ChatEntry
	Ongoing      string
	OngoingError string
}
