package clientui

import "context"

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
}

type RuntimeClient interface {
	Status() RuntimeStatus
	SetSessionName(name string) error
	SetThinkingLevel(level string) error
	SetFastModeEnabled(enabled bool) (bool, error)
	SetReviewerEnabled(enabled bool) (bool, string, error)
	SetAutoCompactionEnabled(enabled bool) (bool, bool)
	AppendLocalEntry(role, text string)
	ChatSnapshot() ChatSnapshot
	ShouldCompactBeforeUserMessage(ctx context.Context, text string) (bool, error)
	SubmitUserMessage(ctx context.Context, text string) (string, error)
	SubmitUserShellCommand(ctx context.Context, command string) error
	CompactContext(ctx context.Context, args string) error
	CompactContextForPreSubmit(ctx context.Context) error
	HasQueuedUserWork() bool
	SubmitQueuedUserMessages(ctx context.Context) (string, error)
	Interrupt() error
	QueueUserMessage(text string)
	DiscardQueuedUserMessagesMatching(text string) int
	RecordPromptHistory(text string) error
}
