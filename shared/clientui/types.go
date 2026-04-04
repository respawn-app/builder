package clientui

import patchformat "builder/server/tools/patch/format"
import "time"

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
	Busy       bool
	RunID      string
	Status     RunStatus
	StartedAt  time.Time
	FinishedAt time.Time
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
	ToolCall    *ToolCallMeta
}

type ChatSnapshot struct {
	Entries      []ChatEntry
	Ongoing      string
	OngoingError string
}

type TranscriptWindow string

const (
	TranscriptWindowDefault     TranscriptWindow = ""
	TranscriptWindowOngoingTail TranscriptWindow = "ongoing_tail"
)

type TranscriptPageRequest struct {
	Offset   int
	Limit    int
	Page     int
	PageSize int
	Window   TranscriptWindow
}

type TranscriptPage struct {
	SessionID             string
	SessionName           string
	ConversationFreshness ConversationFreshness
	Revision              int64
	TotalEntries          int
	Offset                int
	NextOffset            int
	HasMore               bool
	Entries               []ChatEntry
	Ongoing               string
	OngoingError          string
}

type ToolPresentationKind string
type ToolCallRenderBehavior string
type ToolRenderKind string

const (
	ToolPresentationDefault     ToolPresentationKind = "default"
	ToolPresentationShell       ToolPresentationKind = "shell"
	ToolPresentationAskQuestion ToolPresentationKind = "ask_question"

	ToolCallRenderBehaviorDefault     ToolCallRenderBehavior = "default"
	ToolCallRenderBehaviorShell       ToolCallRenderBehavior = "shell"
	ToolCallRenderBehaviorAskQuestion ToolCallRenderBehavior = "ask_question"

	ToolRenderKindShell  ToolRenderKind = "shell"
	ToolRenderKindDiff   ToolRenderKind = "diff"
	ToolRenderKindSource ToolRenderKind = "source"
)

type ToolRenderHint struct {
	Kind       ToolRenderKind
	Path       string
	ResultOnly bool
}

type ToolCallMeta struct {
	ToolName               string
	Presentation           ToolPresentationKind
	RenderBehavior         ToolCallRenderBehavior
	IsShell                bool
	UserInitiated          bool
	Command                string
	CompactText            string
	InlineMeta             string
	TimeoutLabel           string
	PatchSummary           string
	PatchDetail            string
	PatchRender            *patchformat.RenderedPatch
	RenderHint             *ToolRenderHint
	Question               string
	Suggestions            []string
	RecommendedOptionIndex int
	OmitSuccessfulResult   bool
}
