package clientui

import (
	"context"
	"time"
)

type ConversationFreshness uint8

const (
	ConversationFreshnessFresh ConversationFreshness = iota
	ConversationFreshnessEstablished
)

func (f ConversationFreshness) IsFresh() bool {
	return f == ConversationFreshnessFresh
}

type RuntimeContextUsage struct {
	UsedTokens            int
	WindowTokens          int
	CacheHitPercent       int
	HasCacheHitPercentage bool
}

type RuntimeStatus struct {
	ReviewerFrequency                 string
	ReviewerEnabled                   bool
	AutoCompactionEnabled             bool
	FastModeAvailable                 bool
	FastModeEnabled                   bool
	ConversationFreshness             ConversationFreshness
	ParentSessionID                   string
	LastCommittedAssistantFinalAnswer string
	ThinkingLevel                     string
	CompactionMode                    string
	ContextUsage                      RuntimeContextUsage
	CompactionCount                   int
	Update                            UpdateStatus
}

type UpdateStatus struct {
	Checked        bool
	Available      bool
	CurrentVersion string
	LatestVersion  string
}

type RunStatus string

const (
	RunStatusRunning     RunStatus = "running"
	RunStatusCompleted   RunStatus = "completed"
	RunStatusInterrupted RunStatus = "interrupted"
	RunStatusFailed      RunStatus = "failed"
)

type RunView struct {
	RunID      string
	SessionID  string
	StepID     string
	Status     RunStatus
	StartedAt  time.Time
	FinishedAt time.Time
}

type RuntimeMainView struct {
	Status    RuntimeStatus
	Session   RuntimeSessionView
	ActiveRun *RunView
}

type TranscriptMetadata struct {
	Revision            int64
	CommittedEntryCount int
}

type SessionExecutionTarget struct {
	WorkspaceID           string
	WorkspaceName         string
	WorkspaceRoot         string
	WorkspaceAvailability string
	WorktreeID            string
	WorktreeName          string
	WorktreeRoot          string
	WorktreeAvailability  string
	CwdRelpath            string
	EffectiveWorkdir      string
}

type RuntimeSessionView struct {
	SessionID             string
	SessionName           string
	ConversationFreshness ConversationFreshness
	ExecutionTarget       SessionExecutionTarget
	Transcript            TranscriptMetadata
	Chat                  ChatSnapshot
}

type RuntimeClient interface {
	MainView() RuntimeMainView
	RefreshMainView() (RuntimeMainView, error)
	Transcript() TranscriptPage
	RefreshTranscript() (TranscriptPage, error)
	RefreshTranscriptPage(req TranscriptPageRequest) (TranscriptPage, error)
	LoadTranscriptPage(req TranscriptPageRequest) (TranscriptPage, error)
	Status() RuntimeStatus
	SessionView() RuntimeSessionView
	SetSessionName(name string) error
	SetThinkingLevel(level string) error
	SetFastModeEnabled(enabled bool) (bool, error)
	SetReviewerEnabled(enabled bool) (bool, string, error)
	SetAutoCompactionEnabled(enabled bool) (bool, bool, error)
	AppendLocalEntry(role, text string) error
	ShouldCompactBeforeUserMessage(ctx context.Context, text string) (bool, error)
	SubmitUserMessage(ctx context.Context, text string) (string, error)
	SubmitUserShellCommand(ctx context.Context, command string) error
	CompactContext(ctx context.Context, args string) error
	CompactContextForPreSubmit(ctx context.Context) error
	HasQueuedUserWork() (bool, error)
	SubmitQueuedUserMessages(ctx context.Context) (string, error)
	Interrupt() error
	QueueUserMessage(text string)
	DiscardQueuedUserMessagesMatching(text string) int
	RecordPromptHistory(text string) error
}
