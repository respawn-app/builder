package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"builder/prompts"
	"builder/server/llm"
	"builder/server/tools"
	"builder/shared/compaction"
)

type compactionMode string

const (
	compactionModeAuto    compactionMode = "auto"
	compactionModeHandoff compactionMode = "handoff"
	compactionModeManual  compactionMode = "manual"

	defaultContextWindowTokens        = 200_000
	compactOverflowRetries            = 2
	autoCompactNearLimitMargin        = 8_000
	compactionSoonReminderPercent     = 85
	manualCompactionCarryoverMaxChars = 4_000

	additionalCompactionInstructionsHeader = "# Additional user instructions or commentary for this task:"
	manualCompactionCarryoverHeader        = "# Last user message before compaction (work may have been done after it was sent):"
	handoffDisabledByUserMessage           = "User disabled the handoff manually for now. They do not want you to hand off at this time, so please keep working or retry this tool later"
	handoffTooEarlyMessage                 = "trigger_handoff is not enabled yet. Keep working until you receive the reminder that this tool is now enabled, then retry it."
)

var errRemoteCompactionMissingCheckpoint = errors.New("remote compaction output missing checkpoint item")

type compactionResult struct {
	engine            string
	items             []llm.ResponseItem
	usage             llm.Usage
	trimmedItemsCount int
	provider          string
	summary           string
}

type defaultContextCompactor struct {
	engine *Engine
	steps  exclusiveStepLifecycle
}

func (e *Engine) CompactContext(ctx context.Context, args string) error {
	e.ensureOrchestrationCollaborators()
	return e.compactionFlow.CompactContext(ctx, args)
}

func (e *Engine) CompactContextForPreSubmit(ctx context.Context) error {
	e.ensureOrchestrationCollaborators()
	return e.compactionFlow.CompactContextForPreSubmit(ctx)
}

func (e *Engine) TriggerHandoff(ctx context.Context, stepID string, activeCall llm.ToolCall, summarizerPrompt string, futureAgentMessage string) (string, bool, error) {
	e.ensureOrchestrationCollaborators()
	return e.compactionFlow.TriggerHandoff(ctx, stepID, activeCall, summarizerPrompt, futureAgentMessage)
}

func (c *defaultContextCompactor) CompactContext(ctx context.Context, args string) error {
	return c.compactContext(ctx, compactionModeManual, args, true)
}

func (c *defaultContextCompactor) CompactContextForPreSubmit(ctx context.Context) error {
	return c.compactContext(ctx, compactionModeManual, "", false)
}

func (c *defaultContextCompactor) TriggerHandoff(ctx context.Context, stepID string, activeCall llm.ToolCall, summarizerPrompt string, futureAgentMessage string) (string, bool, error) {
	e := c.engine
	_ = activeCall
	if strings.TrimSpace(stepID) == "" {
		return "", false, errors.New("trigger_handoff requires an active step")
	}
	if !e.AutoCompactionEnabled() {
		return "", false, errors.New(handoffDisabledByUserMessage)
	}
	if e.compactionMode() == "none" {
		return "", false, errors.New("User explicitly disabled compaction in configuration.")
	}
	if !e.handoffToolEnabled() {
		return "", false, errors.New(handoffTooEarlyMessage)
	}
	e.queueHandoffRequest(summarizerPrompt, futureAgentMessage)
	summary := "Handoff scheduled to run now."
	appended := strings.TrimSpace(futureAgentMessage) != ""
	return summary, appended, nil
}

func (c *defaultContextCompactor) compactContext(ctx context.Context, mode compactionMode, args string, includeManualCarryover bool) error {
	e := c.engine
	return c.steps.Run(ctx, exclusiveStepOptions{}, func(stepCtx context.Context, stepID string) error {
		if err := e.injectAgentsIfNeeded(stepID); err != nil {
			return err
		}
		_, err := e.compactNow(stepCtx, stepID, mode, args, includeManualCarryover)
		return err
	})
}

func (e *Engine) autoCompactIfNeeded(ctx context.Context, stepID string, mode compactionMode) error {
	e.ensureOrchestrationCollaborators()
	return e.compactionFlow.AutoCompactIfNeeded(ctx, stepID, mode)
}

func (c *defaultContextCompactor) AutoCompactIfNeeded(ctx context.Context, stepID string, mode compactionMode) error {
	e := c.engine
	if mode == compactionModeAuto && !e.shouldAutoCompactWithContext(ctx) {
		return nil
	}
	_, err := e.compactNow(ctx, stepID, mode, "", false)
	if err != nil && mode == compactionModeAuto {
		return fmt.Errorf("auto compaction failed: %w", err)
	}
	if err == nil && mode == compactionModeAuto && e.shouldAutoCompactWithContext(ctx) {
		return errors.New("auto compaction did not reduce context below threshold")
	}
	return err
}

func (e *Engine) shouldAutoCompact() bool {
	return e.shouldAutoCompactWithContext(context.Background())
}

func (e *Engine) shouldAutoCompactWithContext(ctx context.Context) bool {
	if !e.AutoCompactionEnabled() {
		return false
	}
	if e.compactionMode() == "none" {
		return false
	}
	limit := e.autoCompactTokenLimit(ctx)
	if limit <= 0 {
		return false
	}
	return e.usageAtOrAboveLimit(ctx, limit)
}

func (e *Engine) autoCompactTokenLimit(ctx context.Context) int {
	if e.cfg.AutoCompactTokenLimit > 0 {
		return e.cfg.AutoCompactTokenLimit
	}
	limit := e.effectiveContextTokenLimit()
	if limit < 1 {
		return 1
	}
	return limit
}

func (e *Engine) preSubmitCompactionRunwayTokens() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cfg.PreSubmitCompactionLeadTokens > 0 {
		return e.cfg.PreSubmitCompactionLeadTokens
	}
	return compaction.DefaultPreSubmitRunwayTokens
}

func (e *Engine) preSubmitCompactionTokenLimit(ctx context.Context) int {
	limit := e.autoCompactTokenLimit(ctx)
	if limit <= 0 {
		return 0
	}
	return compaction.EffectivePreSubmitThresholdTokens(limit, e.preSubmitCompactionRunwayTokens())
}

func (e *Engine) ShouldCompactBeforeUserMessage(ctx context.Context, text string) (bool, error) {
	e.ensureOrchestrationCollaborators()
	return e.compactionFlow.ShouldCompactBeforeUserMessage(ctx, text)
}

func (c *defaultContextCompactor) ShouldCompactBeforeUserMessage(ctx context.Context, text string) (bool, error) {
	e := c.engine
	if strings.TrimSpace(text) == "" {
		return false, nil
	}
	if !e.AutoCompactionEnabled() || e.compactionMode() == "none" {
		return false, nil
	}
	limit := e.autoCompactTokenLimit(ctx)
	if limit <= 0 {
		return false, nil
	}
	reservedOutput := e.reservedOutputTokens()
	preSubmitLimit := e.preSubmitCompactionTokenLimit(ctx)
	estimatedCurrentTotal := e.currentTokenUsage() + reservedOutput
	if preSubmitLimit > 0 && estimatedCurrentTotal >= preSubmitLimit {
		if preciseInput, ok := e.currentInputTokensPrecisely(ctx); ok {
			return preciseInput+reservedOutput >= preSubmitLimit, nil
		}
		return true, nil
	}
	promptEstimate := estimateItemsTokens(llm.ItemsFromMessages([]llm.Message{{Role: llm.RoleUser, Content: text}}))
	if estimatedCurrentTotal+promptEstimate < limit {
		return false, nil
	}
	req, err := e.buildRequestWithExtraItems(ctx, []llm.ResponseItem{{Type: llm.ResponseItemTypeMessage, Role: llm.RoleUser, Content: text}}, true)
	if err != nil {
		return false, err
	}
	if preciseInput, ok := e.requestInputTokensPrecisely(ctx, req); ok {
		return preciseInput+reservedOutput >= limit, nil
	}
	return estimatedCurrentTotal+promptEstimate >= limit, nil
}

func (e *Engine) resolveContextWindowTokens(ctx context.Context) int {
	if configured := e.configuredContextWindowTokens(); configured > 0 {
		return configured
	}

	model := e.currentModel()
	if resolver, ok := e.llm.(llm.ModelContextWindowClient); ok {
		resolved, err := resolver.ResolveModelContextWindow(ctx, model)
		if err == nil && resolved > 0 {
			e.setContextWindowTokens(resolved)
			return resolved
		}
	}
	return e.contextWindowTokens()
}

func (e *Engine) configuredContextWindowTokens() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cfg.ContextWindowTokens > 0 {
		return e.cfg.ContextWindowTokens
	}
	return 0
}

func (e *Engine) setContextWindowTokens(tokens int) {
	if tokens <= 0 {
		return
	}
	e.mu.Lock()
	e.cfg.ContextWindowTokens = tokens
	e.mu.Unlock()
}

func (e *Engine) currentModel() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.locked != nil {
		if model := strings.TrimSpace(e.locked.Model); model != "" {
			return model
		}
	}
	return strings.TrimSpace(e.cfg.Model)
}

func (e *Engine) reservedOutputTokens() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.locked != nil && e.locked.MaxOutputToken > 0 {
		return e.locked.MaxOutputToken
	}
	if e.cfg.MaxTokens > 0 {
		return e.cfg.MaxTokens
	}
	return 0
}

func autoCompactPrecisionMarginForLimit(limit int) int {
	if limit <= 0 {
		return autoCompactNearLimitMargin
	}
	percentMargin := limit / 50
	if percentMargin > autoCompactNearLimitMargin {
		return percentMargin
	}
	return autoCompactNearLimitMargin
}

func (e *Engine) usageAtOrAboveLimit(ctx context.Context, limit int) bool {
	if limit <= 0 {
		return false
	}
	reservedOutput := e.reservedOutputTokens()
	estimatedInput := e.currentTokenUsage()
	estimatedTotal := estimatedInput + reservedOutput
	margin := autoCompactPrecisionMarginForLimit(limit)
	if estimatedTotal < limit && estimatedTotal+margin < limit {
		return false
	}
	preciseInput, ok := e.currentInputTokensPrecisely(ctx)
	if !ok {
		return estimatedTotal >= limit
	}
	return preciseInput+reservedOutput >= limit
}

func (e *Engine) compactionSoonReminderLimit(ctx context.Context) int {
	limit := e.autoCompactTokenLimit(ctx)
	if limit <= 0 {
		return 0
	}
	reminderLimit := (limit * compactionSoonReminderPercent) / 100
	if reminderLimit < 1 {
		return 1
	}
	return reminderLimit
}

func (e *Engine) maybeAppendCompactionSoonReminder(ctx context.Context, stepID string) error {
	if !e.AutoCompactionEnabled() || e.compactionMode() == "none" {
		return nil
	}
	limit := e.compactionSoonReminderLimit(ctx)
	if limit <= 0 {
		return nil
	}
	if !e.usageAtOrAboveLimit(ctx, limit) {
		return nil
	}
	content := prompts.RenderCompactionSoonReminderPrompt(e.triggerHandoffConfigured())
	if content == "" {
		return nil
	}
	if e.shouldAutoCompactWithContext(ctx) {
		return nil
	}
	e.mu.Lock()
	if e.compactionSoonReminderIssued {
		e.mu.Unlock()
		return nil
	}
	e.mu.Unlock()
	if err := e.appendMessage(stepID, llm.Message{
		Role:        llm.RoleDeveloper,
		MessageType: llm.MessageTypeCompactionSoonReminder,
		Content:     content,
	}); err != nil {
		return err
	}
	return e.persistCompactionSoonReminderIssued(true)
}

func (e *Engine) currentInputTokensPrecisely(ctx context.Context) (int, bool) {
	req, err := e.buildRequest(ctx, "", true)
	if err != nil {
		return 0, false
	}
	return e.requestInputTokensPrecisely(ctx, req)
}

func (e *Engine) requestInputTokensPrecisely(ctx context.Context, req llm.Request) (int, bool) {
	counter, ok := e.llm.(llm.RequestInputTokenCountClient)
	if !ok {
		return 0, false
	}
	cacheKey := requestTokenCountCacheKey(req)
	if cacheKey != "" {
		if cached, ok := e.lookupCompactionTokenCountCache(cacheKey); ok {
			return cached, true
		}
	}
	count, err := counter.CountRequestInputTokens(ctx, req)
	if err != nil || count <= 0 {
		return 0, false
	}
	if cacheKey != "" {
		e.storeCompactionTokenCountCache(cacheKey, count)
	}
	return count, true
}

func requestTokenCountCacheKey(req llm.Request) string {
	payload, err := json.Marshal(req)
	if err != nil {
		return ""
	}
	return string(payload)
}

func (e *Engine) lookupCompactionTokenCountCache(cacheKey string) (int, bool) {
	if strings.TrimSpace(cacheKey) == "" {
		return 0, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.compactionTokenCountCacheKey != cacheKey {
		return 0, false
	}
	if e.compactionTokenCountCacheValue <= 0 {
		return 0, false
	}
	return e.compactionTokenCountCacheValue, true
}

func (e *Engine) storeCompactionTokenCountCache(cacheKey string, count int) {
	if strings.TrimSpace(cacheKey) == "" || count <= 0 {
		return
	}
	e.mu.Lock()
	e.compactionTokenCountCacheKey = cacheKey
	e.compactionTokenCountCacheValue = count
	e.mu.Unlock()
}

func (e *Engine) contextWindowTokens() int {
	if e.cfg.ContextWindowTokens > 0 {
		return e.cfg.ContextWindowTokens
	}
	usage := e.lastUsageSnapshot()
	if usage.WindowTokens > 0 {
		return usage.WindowTokens
	}
	return defaultContextWindowTokens
}

func (e *Engine) effectiveContextTokenLimit() int {
	percent := e.cfg.EffectiveContextWindowPercent
	if percent <= 0 || percent > 100 {
		percent = 95
	}
	return (e.contextWindowTokens() * percent) / 100
}

func (e *Engine) currentTokenUsage() int {
	usage := e.lastUsageSnapshot()
	usageTotal := 0
	if usage.InputTokens > 0 || usage.OutputTokens > 0 {
		usageTotal = usage.InputTokens + usage.OutputTokens
	}
	estimated := e.chat.estimatedProviderTokens()
	if estimated > usageTotal {
		return estimated
	}
	return usageTotal
}

func (e *Engine) compactNow(ctx context.Context, stepID string, mode compactionMode, args string, includeManualCarryover bool) (compactionResult, error) {
	if e.compactionMode() == "none" {
		if mode == compactionModeAuto {
			return compactionResult{}, nil
		}
		return compactionResult{}, errors.New("context compaction is disabled (compaction_mode=none)")
	}

	input := e.snapshotItems()
	if len(input) == 0 {
		return compactionResult{}, nil
	}

	_ = e.resolveContextWindowTokens(ctx)

	caps, err := e.providerCapabilities(ctx)
	if err != nil {
		return compactionResult{}, err
	}
	providerID := strings.TrimSpace(caps.ProviderID)
	if providerID == "" {
		providerID = "unknown"
	}

	if err := e.emitCompactionStatus(stepID, EventCompactionStarted, mode, "selector", providerID, 0, 0, ""); err != nil {
		return compactionResult{}, err
	}

	instructions := compactionInstructions(args)
	manualCarryover := ""
	if mode == compactionModeManual && includeManualCarryover {
		manualCarryover = e.lastVisibleUserMessage()
	}
	var result compactionResult
	if e.compactionMode() == "native" && caps.SupportsResponsesCompact {
		result, err = e.compactRemote(ctx, input, providerID, instructions)
		if err != nil && errors.Is(err, errRemoteCompactionMissingCheckpoint) {
			result, err = e.compactLocal(ctx, input, providerID, instructions)
		}
	} else {
		result, err = e.compactLocal(ctx, input, providerID, instructions)
	}
	if err != nil {
		_ = e.emitCompactionStatus(stepID, EventCompactionFailed, mode, result.engine, providerID, result.trimmedItemsCount, 0, err.Error())
		return compactionResult{}, err
	}

	if len(result.items) == 0 {
		err := errors.New("compaction returned empty replacement history")
		_ = e.emitCompactionStatus(stepID, EventCompactionFailed, mode, result.engine, providerID, result.trimmedItemsCount, 0, err.Error())
		return compactionResult{}, err
	}

	if err := e.replaceHistory(stepID, result.engine, mode, result.items); err != nil {
		_ = e.emitCompactionStatus(stepID, EventCompactionFailed, mode, result.engine, providerID, result.trimmedItemsCount, 0, err.Error())
		return compactionResult{}, err
	}
	if strings.TrimSpace(result.summary) != "" {
		summary := strings.TrimSpace(result.summary)
		if err := e.appendPersistedLocalEntry(stepID, "compaction_summary", summary); err != nil {
			_ = e.emitCompactionStatus(stepID, EventCompactionFailed, mode, result.engine, providerID, result.trimmedItemsCount, 0, err.Error())
			return compactionResult{}, err
		}
	}
	if mode == compactionModeManual {
		if carryover := manualCompactionCarryoverMessage(manualCarryover); strings.TrimSpace(carryover.Content) != "" {
			if err := e.appendMessage(stepID, carryover); err != nil {
				_ = e.emitCompactionStatus(stepID, EventCompactionFailed, mode, result.engine, providerID, result.trimmedItemsCount, 0, err.Error())
				return compactionResult{}, err
			}
		}
	}
	compactionNumber := e.nextCompactionCount()
	windowTokens := result.usage.WindowTokens
	if windowTokens <= 0 {
		windowTokens = e.contextWindowTokens()
	}
	inputTokens := estimateItemsTokens(result.items)
	if preciseInput, ok := e.currentInputTokensPrecisely(ctx); ok {
		inputTokens = preciseInput
	}
	e.setLastUsage(llm.Usage{
		InputTokens:  inputTokens,
		OutputTokens: 0,
		WindowTokens: windowTokens,
	})

	if err := e.emitCompactionStatus(stepID, EventCompactionCompleted, mode, result.engine, providerID, result.trimmedItemsCount, compactionNumber, ""); err != nil {
		return compactionResult{}, err
	}
	return result, nil
}

func (e *Engine) compactRemote(ctx context.Context, input []llm.ResponseItem, providerID string, instructions string) (compactionResult, error) {
	compactor, ok := e.llm.(llm.CompactionClient)
	if !ok {
		return compactionResult{}, errors.New("llm client does not support remote compaction")
	}
	locked, err := e.ensureLocked()
	if err != nil {
		return compactionResult{}, err
	}
	contextLimit := e.effectiveContextTokenLimit()
	canonicalContext := extractCanonicalContext(input)
	trimmedInput, trimmedCount := e.trimCompactionInputToLimit(ctx, locked.Model, instructions, input, contextLimit)
	baseRequest := llm.CompactionRequest{
		Model:        locked.Model,
		Instructions: instructions,
		SessionID:    e.store.Meta().SessionID,
		InputItems:   trimmedInput,
	}

	resp, _, extraTrimmed, err := e.compactWithContextTrimRetry(ctx, compactor, baseRequest, contextLimit)
	if err != nil {
		return compactionResult{}, err
	}
	trimmedCount += extraTrimmed

	sanitized, err := sanitizeRemoteCompactionOutput(resp.OutputItems)
	if err != nil {
		return compactionResult{}, err
	}
	replacement := make([]llm.ResponseItem, 0, len(canonicalContext)+len(sanitized))
	replacement = append(replacement, canonicalContext...)
	replacement = append(replacement, sanitized...)
	return compactionResult{
		engine:            "remote",
		items:             replacement,
		usage:             resp.Usage,
		trimmedItemsCount: trimmedCount + resp.TrimmedItemsCount,
		provider:          providerID,
	}, nil
}

func (e *Engine) compactWithContextTrimRetry(
	ctx context.Context,
	client llm.CompactionClient,
	request llm.CompactionRequest,
	limit int,
) (llm.CompactionResponse, []llm.ResponseItem, int, error) {
	currentInput := llm.CloneResponseItems(request.InputItems)
	additionalTrimmed := 0

	for attempt := 0; attempt <= compactOverflowRetries; attempt++ {
		req := request
		req.InputItems = llm.CloneResponseItems(currentInput)

		resp, err := e.compactWithRetry(ctx, client, req)
		if err == nil {
			return resp, currentInput, additionalTrimmed, nil
		}
		if !isCompactionContextOverflow(err) || attempt == compactOverflowRetries {
			return llm.CompactionResponse{}, nil, additionalTrimmed, err
		}

		nextInput, trimmed := e.trimCompactionInputToLimit(ctx, request.Model, request.Instructions, currentInput, limit)
		if trimmed == 0 {
			nextInput, trimmed = trimOldestEligibleItems(currentInput, 1+attempt)
		}
		if trimmed == 0 {
			return llm.CompactionResponse{}, nil, additionalTrimmed, err
		}
		currentInput = nextInput
		additionalTrimmed += trimmed
	}

	return llm.CompactionResponse{}, nil, additionalTrimmed, errors.New("compaction context trim retry exhausted")
}

func (e *Engine) compactWithRetry(ctx context.Context, client llm.CompactionClient, request llm.CompactionRequest) (llm.CompactionResponse, error) {
	delays := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}
	var lastErr error
	for i := 0; i <= len(delays); i++ {
		resp, err := client.Compact(ctx, request)
		if err == nil {
			return resp, nil
		}
		if llm.IsNonRetriableModelError(err) || llm.IsContextLengthOverflowError(err) {
			return llm.CompactionResponse{}, err
		}
		lastErr = err
		if i == len(delays) {
			break
		}
		select {
		case <-ctx.Done():
			return llm.CompactionResponse{}, ctx.Err()
		case <-time.After(delays[i]):
		}
	}
	return llm.CompactionResponse{}, fmt.Errorf("compaction request failed after retries: %w", lastErr)
}

func isCompactionContextOverflow(err error) bool {
	return llm.IsContextLengthOverflowError(err)
}

func (e *Engine) compactLocal(ctx context.Context, input []llm.ResponseItem, providerID string, instructions string) (compactionResult, error) {
	locked, err := e.ensureLocked()
	if err != nil {
		return compactionResult{}, err
	}
	summary, err := e.localCompactionSummary(ctx, input, instructions)
	if err != nil {
		return compactionResult{}, err
	}
	replacement := e.rebuildLocalCompactionHistory(ctx, locked.Model, input, summary, e.cfg.LocalCompactionCarryoverLimit)
	usageInputTokens := estimateItemsTokens(replacement)
	if preciseInput, ok := e.inputTokensForItems(ctx, locked.Model, "", replacement); ok {
		usageInputTokens = preciseInput
	}
	return compactionResult{
		engine:            "local",
		items:             replacement,
		usage:             llm.Usage{InputTokens: usageInputTokens, WindowTokens: e.contextWindowTokens()},
		trimmedItemsCount: 0,
		provider:          providerID,
		summary:           strings.TrimSpace(summary),
	}, nil
}

func (e *Engine) localCompactionSummary(ctx context.Context, input []llm.ResponseItem, instructions string) (string, error) {
	locked, err := e.ensureLocked()
	if err != nil {
		return "", err
	}
	window := localCompactionWindow(input)
	items := append(window, llm.ResponseItem{
		Type:    llm.ResponseItemTypeMessage,
		Role:    llm.RoleDeveloper,
		Content: instructions,
	})
	items = sanitizeItemsForLLM(items)

	req, err := llm.RequestFromLockedContract(locked, prompts.BaseSystemPrompt(), items, e.requestTools())
	if err != nil {
		return "", err
	}
	req.ReasoningEffort = e.ThinkingLevel()
	req.FastMode = e.FastModeEnabled()
	req.SessionID = e.store.Meta().SessionID

	resp, err := e.generateWithRetry(ctx, "", req, nil, nil, nil)
	if err != nil {
		return "", err
	}
	if len(resp.ToolCalls) > 0 {
		return "", errors.New("local compaction summary attempted tool calls")
	}
	summary := strings.TrimSpace(resp.Assistant.Content)
	if summary == "" {
		return "", errors.New("local compaction summary was empty")
	}
	return summary, nil
}

func localCompactionWindow(input []llm.ResponseItem) []llm.ResponseItem {
	if len(input) == 0 {
		return nil
	}
	start := 0
	for i := len(input) - 1; i >= 0; i-- {
		if isCompactionBoundaryItem(input[i]) {
			start = i
			break
		}
	}
	window := llm.CloneResponseItems(input[start:])
	if start == 0 {
		return window
	}
	canonical := extractCanonicalContext(input)
	out := make([]llm.ResponseItem, 0, len(canonical)+len(window))
	out = append(out, canonical...)
	out = append(out, window...)
	return out
}

func isCompactionBoundaryItem(item llm.ResponseItem) bool {
	if item.Type == llm.ResponseItemTypeCompaction {
		return true
	}
	if item.Type == llm.ResponseItemTypeMessage {
		return item.MessageType == llm.MessageTypeCompactionSummary
	}
	return false
}

func (e *Engine) lastVisibleUserMessage() string {
	messages := e.snapshotMessages()
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != llm.RoleUser {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" || msg.MessageType == llm.MessageTypeCompactionSummary {
			continue
		}
		return msg.Content
	}
	return ""
}

func manualCompactionCarryoverMessage(text string) llm.Message {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return llm.Message{}
	}
	content := trimCompactionCarryoverText(trimmed, manualCompactionCarryoverMaxChars)
	return llm.Message{
		Role:        llm.RoleDeveloper,
		MessageType: llm.MessageTypeManualCompactionCarryover,
		Content:     manualCompactionCarryoverHeader + "\n\n" + content,
	}
}

func handoffFutureAgentMessage(text string) llm.Message {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return llm.Message{}
	}
	return llm.Message{
		Role:        llm.RoleDeveloper,
		MessageType: llm.MessageTypeHandoffFutureMessage,
		Content:     trimmed,
	}
}

func (e *Engine) queueHandoffRequest(summarizerPrompt string, futureAgentMessage string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pendingHandoffRequest = &handoffRequest{
		summarizerPrompt:   strings.TrimSpace(summarizerPrompt),
		futureAgentMessage: strings.TrimSpace(futureAgentMessage),
	}
}

func (e *Engine) clearPendingHandoffRequest() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pendingHandoffRequest = nil
}

func (e *Engine) queuePendingHandoffFutureMessage(message string) {
	trimmed := strings.TrimSpace(message)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pendingHandoffFutureMessage = trimmed
}

func (e *Engine) clearPendingHandoffFutureMessage() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pendingHandoffFutureMessage = ""
}

func (e *Engine) pendingHandoffRequestSnapshot() *handoffRequest {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pendingHandoffRequest == nil {
		return nil
	}
	req := *e.pendingHandoffRequest
	return &req
}

func (e *Engine) pendingHandoffFutureMessageSnapshot() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return strings.TrimSpace(e.pendingHandoffFutureMessage)
}

func (e *Engine) applyPendingHandoffIfNeeded(ctx context.Context, stepID string) error {
	if futureMessage := e.pendingHandoffFutureMessageSnapshot(); futureMessage != "" {
		if err := e.appendMessage(stepID, handoffFutureAgentMessage(futureMessage)); err != nil {
			return err
		}
		e.clearPendingHandoffFutureMessage()
		return nil
	}
	req := e.pendingHandoffRequestSnapshot()
	if req == nil {
		return nil
	}
	if _, err := e.compactNow(ctx, stepID, compactionModeHandoff, req.summarizerPrompt, false); err != nil {
		return err
	}
	if futureMessage := strings.TrimSpace(req.futureAgentMessage); futureMessage != "" {
		e.queuePendingHandoffFutureMessage(futureMessage)
		if msg := handoffFutureAgentMessage(futureMessage); strings.TrimSpace(msg.Content) != "" {
			if err := e.appendMessage(stepID, msg); err != nil {
				return err
			}
		}
		e.clearPendingHandoffFutureMessage()
	}
	e.clearPendingHandoffRequest()
	return nil
}

func trimCompactionCarryoverText(text string, maxChars int) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || maxChars <= 0 {
		return trimmed
	}
	runes := []rune(trimmed)
	if len(runes) <= maxChars {
		return trimmed
	}
	if maxChars < 4 {
		return string(runes[:maxChars])
	}
	return string(runes[:maxChars-4]) + "\n..."
}

func (e *Engine) handoffToolEnabled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.compactionSoonReminderIssued
}

func (e *Engine) setCompactionSoonReminderIssued(issued bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.compactionSoonReminderIssued = issued
}

func (e *Engine) persistCompactionSoonReminderIssued(issued bool) error {
	e.setCompactionSoonReminderIssued(issued)
	return e.store.SetCompactionSoonReminderIssued(issued)
}

func (e *Engine) syncCompactionSoonReminderIssuedFromMessages(messages []llm.Message) {
	issued := false
	for _, message := range messages {
		if message.Role == llm.RoleDeveloper && message.MessageType == llm.MessageTypeCompactionSoonReminder && strings.TrimSpace(message.Content) != "" {
			issued = true
		}
	}
	e.setCompactionSoonReminderIssued(issued)
}

func (e *Engine) syncCompactionSoonReminderIssuedFromItems(items []llm.ResponseItem) {
	e.syncCompactionSoonReminderIssuedFromMessages(llm.MessagesFromItems(items))
}

func (e *Engine) triggerHandoffConfigured() bool {
	for _, id := range e.cfg.EnabledTools {
		if id == tools.ToolTriggerHandoff {
			return true
		}
	}
	return false
}

func (e *Engine) compactionMode() string {
	normalized, ok := NormalizeCompactionMode(e.cfg.CompactionMode)
	if !ok {
		return "native"
	}
	return normalized
}

func compactionInstructions(args string) string {
	instructions := prompts.CompactionPrompt
	if strings.TrimSpace(args) == "" {
		return instructions
	}
	instructions = strings.TrimRight(instructions, "\n")
	return instructions + "\n\n" + additionalCompactionInstructionsHeader + "\n " + strings.TrimSpace(args)
}
