package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"builder/internal/llm"
	"builder/internal/tools"
)

func (e *Engine) snapshotMessages() []llm.Message {
	return e.chat.snapshotMessages()
}

func (e *Engine) snapshotItems() []llm.ResponseItem {
	return e.chat.snapshotItems()
}

func (e *Engine) ChatSnapshot() ChatSnapshot {
	return e.chat.snapshot()
}

func (e *Engine) ContextUsage() ContextUsage {
	window := e.contextWindowTokens()
	used := e.currentTokenUsage()
	cacheHitPercent, hasCacheHitPercentage := e.cacheHitSnapshot()
	if used < 0 {
		used = 0
	}
	if window < 0 {
		window = 0
	}
	return ContextUsage{
		UsedTokens:            used,
		WindowTokens:          window,
		CacheHitPercent:       cacheHitPercent,
		HasCacheHitPercentage: hasCacheHitPercentage,
	}
}

func (e *Engine) AppendLocalEntry(role, text string) {
	e.AppendLocalEntryWithOngoingText(role, text, "")
}

func (e *Engine) AppendLocalEntryWithOngoingText(role, text, ongoingText string) {
	e.chat.appendLocalEntryWithOngoingText(role, text, ongoingText)
	e.emit(Event{Kind: EventConversationUpdated, StepID: ""})
}

func (e *Engine) RecordPromptHistory(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	_, err := e.store.AppendEvent("", "prompt_history", map[string]any{"text": text})
	return err
}

func (e *Engine) SetOngoingError(text string) {
	e.chat.setOngoingError(text)
	e.emit(Event{Kind: EventConversationUpdated, StepID: ""})
}

func (e *Engine) ClearOngoingError() {
	e.chat.clearOngoingError()
	e.emit(Event{Kind: EventConversationUpdated, StepID: ""})
}

func (e *Engine) SetSessionName(name string) error {
	return e.store.SetName(name)
}

func (e *Engine) SetThinkingLevel(level string) error {
	normalized, ok := NormalizeThinkingLevel(level)
	if !ok {
		return fmt.Errorf("invalid thinking level %q (expected low|medium|high|xhigh)", strings.TrimSpace(level))
	}
	e.mu.Lock()
	e.cfg.ThinkingLevel = normalized
	e.mu.Unlock()
	return nil
}

func (e *Engine) SetFastModeEnabled(enabled bool) (bool, error) {
	if enabled && !e.FastModeAvailable() {
		return false, errors.New("fast mode is only available for OpenAI-based Responses providers")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cfg.FastModeState != nil {
		return e.cfg.FastModeState.SetEnabled(enabled), nil
	}
	if e.cfg.FastModeEnabled == enabled {
		return false, nil
	}
	e.cfg.FastModeEnabled = enabled
	return true, nil
}

func (e *Engine) SetAutoCompactionEnabled(enabled bool) (bool, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	current := true
	if e.cfg.AutoCompactionEnabled != nil {
		current = *e.cfg.AutoCompactionEnabled
	}
	if current == enabled {
		return false, current
	}
	if e.cfg.AutoCompactionEnabled == nil {
		e.cfg.AutoCompactionEnabled = new(bool)
	}
	*e.cfg.AutoCompactionEnabled = enabled
	return true, enabled
}

func (e *Engine) SetReviewerEnabled(enabled bool) (bool, string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	current, ok := NormalizeReviewerFrequency(e.cfg.Reviewer.Frequency)
	if !ok {
		current = "off"
	}
	if current != "off" {
		e.reviewerResumeFrequency = current
	}

	if enabled {
		if current != "off" {
			return false, current, nil
		}
		if err := e.initReviewerClientLocked(); err != nil {
			return false, current, err
		}
		target := e.reviewerResumeFrequency
		if target == "" || target == "off" {
			target = "edits"
		}
		e.cfg.Reviewer.Frequency = target
		return true, target, nil
	}

	if current == "off" {
		return false, current, nil
	}
	e.cfg.Reviewer.Frequency = "off"
	return true, "off", nil
}

func (e *Engine) ThinkingLevel() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return strings.TrimSpace(e.cfg.ThinkingLevel)
}

func (e *Engine) FastModeEnabled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cfg.FastModeState != nil {
		return e.cfg.FastModeState.Enabled()
	}
	return e.cfg.FastModeEnabled
}

func (e *Engine) FastModeAvailable() bool {
	caps, err := e.providerCapabilities(context.Background())
	if err != nil {
		return false
	}
	return llm.SupportsFastModeProvider(caps)
}

func (e *Engine) ReviewerFrequency() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	normalized, ok := NormalizeReviewerFrequency(e.cfg.Reviewer.Frequency)
	if !ok {
		return "off"
	}
	return normalized
}

func (e *Engine) ReviewerEnabled() bool {
	return e.ReviewerFrequency() != "off"
}

func (e *Engine) AutoCompactionEnabled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cfg.AutoCompactionEnabled == nil {
		return true
	}
	return *e.cfg.AutoCompactionEnabled
}

func (e *Engine) CompactionMode() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	normalized, ok := NormalizeCompactionMode(e.cfg.CompactionMode)
	if !ok {
		return "native"
	}
	return normalized
}

func (e *Engine) initReviewerClient() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.initReviewerClientLocked()
}

func (e *Engine) initReviewerClientLocked() error {
	if e.reviewer != nil {
		return nil
	}
	if e.cfg.Reviewer.ClientFactory == nil {
		return errors.New("reviewer client is not configured")
	}
	client, err := e.cfg.Reviewer.ClientFactory()
	if err != nil {
		return fmt.Errorf("configure reviewer client: %w", err)
	}
	e.reviewer = client
	return nil
}

func (e *Engine) reviewerClientSnapshot() llm.Client {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reviewer
}

func (e *Engine) reviewerTurnConfigSnapshot() (string, llm.Client) {
	e.mu.Lock()
	defer e.mu.Unlock()
	normalized, ok := NormalizeReviewerFrequency(e.cfg.Reviewer.Frequency)
	if !ok {
		normalized = "off"
	}
	return normalized, e.reviewer
}

func (e *Engine) reviewerRequestConfigSnapshot() reviewerRequestConfig {
	e.mu.Lock()
	defer e.mu.Unlock()
	return reviewerRequestConfig{
		Model:         strings.TrimSpace(e.cfg.Reviewer.Model),
		ThinkingLevel: strings.TrimSpace(e.cfg.Reviewer.ThinkingLevel),
	}
}

func (e *Engine) SessionName() string {
	return strings.TrimSpace(e.store.Meta().Name)
}

func (e *Engine) SessionID() string {
	return strings.TrimSpace(e.store.Meta().SessionID)
}

func (e *Engine) ParentSessionID() string {
	return strings.TrimSpace(e.store.Meta().ParentSessionID)
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

type storedLocalEntry struct {
	Role        string `json:"role"`
	Text        string `json:"text"`
	OngoingText string `json:"ongoing_text,omitempty"`
}

type historyReplacementPayload struct {
	Engine string             `json:"engine"`
	Mode   string             `json:"mode"`
	Items  []llm.ResponseItem `json:"items"`
}

func toToolNames(ids []tools.ID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		out = append(out, string(id))
	}
	return out
}

func (e *Engine) lastUsageSnapshot() llm.Usage {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastUsage
}

func (e *Engine) setLastUsage(usage llm.Usage) {
	e.mu.Lock()
	e.lastUsage = usage
	if usage.HasCachedInputTokens && usage.InputTokens > 0 {
		cachedTokens := usage.CachedInputTokens
		if cachedTokens < 0 {
			cachedTokens = 0
		}
		if cachedTokens > usage.InputTokens {
			cachedTokens = usage.InputTokens
		}
		e.totalInputTokens += usage.InputTokens
		e.totalCachedInputTokens += cachedTokens
	}
	e.mu.Unlock()
}

func (e *Engine) cacheHitSnapshot() (int, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.totalInputTokens <= 0 {
		return 0, false
	}
	cachedTokens := e.totalCachedInputTokens
	if cachedTokens < 0 {
		cachedTokens = 0
	}
	if cachedTokens > e.totalInputTokens {
		cachedTokens = e.totalInputTokens
	}
	pct := (cachedTokens * 100) / e.totalInputTokens
	if pct < 0 {
		return 0, false
	}
	if pct > 100 {
		return 100, true
	}
	return pct, true
}

func (e *Engine) emit(evt Event) {
	if e.cfg.OnEvent != nil {
		e.cfg.OnEvent(evt)
	}
}

func (e *Engine) nextCompactionCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.compactionCount++
	return e.compactionCount
}
