package app

import (
	"context"

	"builder/server/runtime"
	"builder/server/session"
	"builder/shared/clientui"
)

type engineUIRuntimeClient struct {
	engine *runtime.Engine
}

func newUIRuntimeClient(engine *runtime.Engine) clientui.RuntimeClient {
	if engine == nil {
		return nil
	}
	return engineUIRuntimeClient{engine: engine}
}

func (c engineUIRuntimeClient) ReviewerFrequency() string   { return c.engine.ReviewerFrequency() }
func (c engineUIRuntimeClient) ReviewerEnabled() bool       { return c.engine.ReviewerEnabled() }
func (c engineUIRuntimeClient) AutoCompactionEnabled() bool { return c.engine.AutoCompactionEnabled() }
func (c engineUIRuntimeClient) FastModeAvailable() bool     { return c.engine.FastModeAvailable() }
func (c engineUIRuntimeClient) FastModeEnabled() bool       { return c.engine.FastModeEnabled() }
func (c engineUIRuntimeClient) ConversationFreshness() clientui.ConversationFreshness {
	return mapConversationFreshness(c.engine.ConversationFreshness())
}
func (c engineUIRuntimeClient) ParentSessionID() string { return c.engine.ParentSessionID() }
func (c engineUIRuntimeClient) LastCommittedAssistantFinalAnswer() string {
	return c.engine.LastCommittedAssistantFinalAnswer()
}
func (c engineUIRuntimeClient) SetSessionName(name string) error {
	return c.engine.SetSessionName(name)
}
func (c engineUIRuntimeClient) ThinkingLevel() string { return c.engine.ThinkingLevel() }
func (c engineUIRuntimeClient) SetThinkingLevel(level string) error {
	return c.engine.SetThinkingLevel(level)
}
func (c engineUIRuntimeClient) SetFastModeEnabled(enabled bool) (bool, error) {
	return c.engine.SetFastModeEnabled(enabled)
}
func (c engineUIRuntimeClient) SetReviewerEnabled(enabled bool) (bool, string, error) {
	return c.engine.SetReviewerEnabled(enabled)
}
func (c engineUIRuntimeClient) CompactionMode() string { return c.engine.CompactionMode() }
func (c engineUIRuntimeClient) SetAutoCompactionEnabled(enabled bool) (bool, bool) {
	return c.engine.SetAutoCompactionEnabled(enabled)
}
func (c engineUIRuntimeClient) AppendLocalEntry(role, text string) {
	c.engine.AppendLocalEntry(role, text)
}
func (c engineUIRuntimeClient) ChatSnapshot() clientui.ChatSnapshot {
	return projectChatSnapshot(c.engine.ChatSnapshot())
}
func (c engineUIRuntimeClient) ShouldCompactBeforeUserMessage(ctx context.Context, text string) (bool, error) {
	return c.engine.ShouldCompactBeforeUserMessage(ctx, text)
}
func (c engineUIRuntimeClient) SubmitUserMessage(ctx context.Context, text string) (string, error) {
	msg, err := c.engine.SubmitUserMessage(ctx, text)
	return msg.Content, err
}
func (c engineUIRuntimeClient) SubmitUserShellCommand(ctx context.Context, command string) error {
	_, err := c.engine.SubmitUserShellCommand(ctx, command)
	return err
}
func (c engineUIRuntimeClient) CompactContext(ctx context.Context, args string) error {
	return c.engine.CompactContext(ctx, args)
}
func (c engineUIRuntimeClient) CompactContextForPreSubmit(ctx context.Context) error {
	return c.engine.CompactContextForPreSubmit(ctx)
}
func (c engineUIRuntimeClient) HasQueuedUserWork() bool { return c.engine.HasQueuedUserWork() }
func (c engineUIRuntimeClient) SubmitQueuedUserMessages(ctx context.Context) (string, error) {
	msg, err := c.engine.SubmitQueuedUserMessages(ctx)
	return msg.Content, err
}
func (c engineUIRuntimeClient) Interrupt() error             { return c.engine.Interrupt() }
func (c engineUIRuntimeClient) QueueUserMessage(text string) { c.engine.QueueUserMessage(text) }
func (c engineUIRuntimeClient) DiscardQueuedUserMessagesMatching(text string) int {
	return c.engine.DiscardQueuedUserMessagesMatching(text)
}
func (c engineUIRuntimeClient) RecordPromptHistory(text string) error {
	return c.engine.RecordPromptHistory(text)
}
func (c engineUIRuntimeClient) ContextUsage() clientui.RuntimeContextUsage {
	usage := c.engine.ContextUsage()
	return clientui.RuntimeContextUsage{
		UsedTokens:            usage.UsedTokens,
		WindowTokens:          usage.WindowTokens,
		CacheHitPercent:       usage.CacheHitPercent,
		HasCacheHitPercentage: usage.HasCacheHitPercentage,
	}
}
func (c engineUIRuntimeClient) CompactionCount() int { return c.engine.CompactionCount() }

func mapConversationFreshness(freshness session.ConversationFreshness) clientui.ConversationFreshness {
	if freshness.IsFresh() {
		return clientui.ConversationFreshnessFresh
	}
	return clientui.ConversationFreshnessEstablished
}
