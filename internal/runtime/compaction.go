package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"builder/internal/llm"
	"builder/prompts"
	"github.com/google/uuid"
)

type compactionMode string

const (
	compactionModeAuto   compactionMode = "auto"
	compactionModeManual compactionMode = "manual"

	defaultContextWindowTokens = 200_000
	compactOverflowRetries     = 2

	additionalCompactionInstructionsHeader = "# Additional user instructions or commentary for this task:"
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

func (e *Engine) CompactContext(ctx context.Context, args string) (err error) {
	e.mu.Lock()
	if e.busy {
		e.mu.Unlock()
		return errors.New("agent is busy")
	}
	e.busy = true
	stepCtx, cancel := context.WithCancel(ctx)
	e.cancelCurrent = cancel
	e.mu.Unlock()
	stepID := ""
	defer func() {
		e.mu.Lock()
		e.busy = false
		e.cancelCurrent = nil
		e.mu.Unlock()
		if clearErr := e.store.MarkInFlight(false); clearErr != nil {
			wrapped := fmt.Errorf("mark in-flight false: %w", clearErr)
			e.emit(Event{Kind: EventInFlightClearFailed, StepID: stepID, Error: wrapped.Error()})
			err = errors.Join(err, wrapped)
		}
	}()

	if err = e.store.MarkInFlight(true); err != nil {
		return err
	}

	stepID = uuid.NewString()
	if err = e.injectAgentsIfNeeded(stepID); err != nil {
		return err
	}
	_, err = e.compactNow(stepCtx, stepID, compactionModeManual, args)
	return err
}

func (e *Engine) autoCompactIfNeeded(ctx context.Context, stepID string, mode compactionMode) error {
	if mode == compactionModeAuto && !e.shouldAutoCompact() {
		return nil
	}
	_, err := e.compactNow(ctx, stepID, mode, "")
	if err != nil && mode == compactionModeAuto {
		return fmt.Errorf("auto compaction failed: %w", err)
	}
	if err == nil && mode == compactionModeAuto && e.shouldAutoCompact() {
		return errors.New("auto compaction did not reduce context below threshold")
	}
	return err
}

func (e *Engine) shouldAutoCompact() bool {
	current := e.currentTokenUsage()
	limit := e.autoCompactTokenLimit()
	return limit > 0 && current >= limit
}

func (e *Engine) autoCompactTokenLimit() int {
	if e.cfg.AutoCompactTokenLimit > 0 {
		return e.cfg.AutoCompactTokenLimit
	}
	window := e.contextWindowTokens()
	limit := int(float64(window) * 0.90)
	if limit < 1 {
		return 1
	}
	return limit
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
	if usage.InputTokens > 0 || usage.OutputTokens > 0 {
		return usage.InputTokens + usage.OutputTokens
	}
	return estimateItemsTokens(e.snapshotItems())
}

func (e *Engine) compactNow(ctx context.Context, stepID string, mode compactionMode, args string) (compactionResult, error) {
	input := e.snapshotItems()
	if len(input) == 0 {
		return compactionResult{}, nil
	}

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
	var result compactionResult
	if e.useNativeCompaction() && caps.SupportsResponsesCompact {
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
	compactionNumber := e.nextCompactionCount()
	if strings.TrimSpace(result.summary) != "" {
		if err := e.appendPersistedLocalEntry(stepID, "compaction_summary", strings.TrimSpace(result.summary)); err != nil {
			_ = e.emitCompactionStatus(stepID, EventCompactionFailed, mode, result.engine, providerID, result.trimmedItemsCount, 0, err.Error())
			return compactionResult{}, err
		}
	}
	windowTokens := result.usage.WindowTokens
	if windowTokens <= 0 {
		windowTokens = e.contextWindowTokens()
	}
	e.setLastUsage(llm.Usage{
		InputTokens:  estimateItemsTokens(result.items),
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
	canonicalContext := extractCanonicalContext(input)
	trimmedInput, trimmedCount := trimCompactionInput(input, e.effectiveContextTokenLimit())

	locked, err := e.ensureLocked()
	if err != nil {
		return compactionResult{}, err
	}
	baseRequest := llm.CompactionRequest{
		Model:        locked.Model,
		Instructions: instructions,
		SessionID:    e.store.Meta().SessionID,
		InputItems:   trimmedInput,
	}

	resp, _, extraTrimmed, err := e.compactWithContextTrimRetry(ctx, compactor, baseRequest)
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

		nextInput, trimmed := trimOldestEligibleItems(currentInput, 1+attempt)
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
		if llm.IsNonRetriableModelError(err) {
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
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	return statusErr.StatusCode == 400
}

func (e *Engine) compactLocal(ctx context.Context, input []llm.ResponseItem, providerID string, instructions string) (compactionResult, error) {
	summary, err := e.localCompactionSummary(ctx, input, instructions)
	if err != nil {
		return compactionResult{}, err
	}
	replacement := rebuildLocalCompactionHistory(input, summary, e.cfg.LocalCompactionCarryoverLimit)
	return compactionResult{
		engine:            "local",
		items:             replacement,
		usage:             llm.Usage{InputTokens: estimateItemsTokens(replacement), WindowTokens: e.contextWindowTokens()},
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
	messages := llm.MessagesFromItems(items)
	messages = sanitizeMessagesForLLM(messages)
	items = sanitizeItemsForLLM(items)

	req, err := llm.RequestFromLockedContractWithItems(locked, prompts.SystemPrompt, messages, items, e.requestTools())
	if err != nil {
		return "", err
	}
	req.SessionID = e.store.Meta().SessionID

	resp, err := e.generateWithRetry(ctx, req, nil, nil)
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
	if item.Type == llm.ResponseItemTypeMessage && item.Role == llm.RoleUser {
		return strings.HasPrefix(strings.TrimSpace(item.Content), prompts.CompactionSummaryPrefix)
	}
	return false
}

func (e *Engine) useNativeCompaction() bool {
	if e.cfg.UseNativeCompaction == nil {
		return true
	}
	return *e.cfg.UseNativeCompaction
}

func compactionInstructions(args string) string {
	instructions := prompts.CompactionPrompt
	if strings.TrimSpace(args) == "" {
		return instructions
	}
	instructions = strings.TrimRight(instructions, "\n")
	return instructions + "\n\n" + additionalCompactionInstructionsHeader + "\n " + strings.TrimSpace(args)
}

func (e *Engine) providerCapabilities(ctx context.Context) (llm.ProviderCapabilities, error) {
	caps := llm.ProviderCapabilities{
		ProviderID:                    "openai",
		SupportsResponsesAPI:          true,
		SupportsResponsesCompact:      false,
		SupportsReasoningEncrypted:    true,
		SupportsServerSideContextEdit: true,
		IsOpenAIFirstParty:            false,
	}
	if provider, ok := e.llm.(llm.ProviderCapabilitiesClient); ok {
		providerCaps, err := provider.ProviderCapabilities(ctx)
		if err != nil {
			return llm.ProviderCapabilities{}, err
		}
		caps = providerCaps
	}

	model := strings.TrimSpace(e.cfg.Model)
	if locked, err := e.ensureLocked(); err == nil {
		if v := strings.TrimSpace(locked.Model); v != "" {
			model = v
		}
	}
	if llm.InferProviderFromModel(model) == llm.ProviderOpenAI {
		caps.SupportsResponsesCompact = true
		caps.IsOpenAIFirstParty = true
		if strings.TrimSpace(caps.ProviderID) == "" {
			caps.ProviderID = "openai"
		}
	} else {
		caps.SupportsResponsesCompact = false
		caps.IsOpenAIFirstParty = false
	}
	return caps, nil
}

func (e *Engine) replaceHistory(stepID, engine string, mode compactionMode, items []llm.ResponseItem) error {
	payload := historyReplacementPayload{
		Engine: strings.TrimSpace(engine),
		Mode:   string(mode),
		Items:  llm.CloneResponseItems(items),
	}
	if payload.Engine == "reviewer_rollback" {
		e.chat.restoreMessagesFromItems(payload.Items)
	} else {
		e.chat.replaceHistory(payload.Items)
	}
	_, err := e.store.AppendEvent(stepID, "history_replaced", payload)
	if err == nil {
		e.emit(Event{Kind: EventConversationUpdated, StepID: stepID})
	}
	return err
}

func (e *Engine) emitCompactionStatus(stepID string, kind EventKind, mode compactionMode, engine, provider string, trimmed, count int, errText string) error {
	status := &CompactionStatus{
		Mode:              string(mode),
		Engine:            strings.TrimSpace(engine),
		Provider:          strings.TrimSpace(provider),
		TrimmedItemsCount: trimmed,
		Count:             count,
		Error:             strings.TrimSpace(errText),
	}
	e.emit(Event{
		Kind:       kind,
		StepID:     stepID,
		Compaction: status,
	})

	switch kind {
	case EventCompactionStarted:
		return nil
	case EventCompactionCompleted:
		return e.appendPersistedLocalEntry(stepID, "compaction_notice", fmt.Sprintf("context compacted for the %s time", ordinal(status.Count)))
	case EventCompactionFailed:
		message := fmt.Sprintf("Context compaction failed (%s): %s", status.Mode, status.Error)
		if strings.TrimSpace(status.Error) == "" {
			message = fmt.Sprintf("Context compaction failed (%s).", status.Mode)
		}
		return e.appendPersistedLocalEntry(stepID, "error", message)
	default:
		return nil
	}
}

func ordinal(v int) string {
	if v <= 0 {
		return "0th"
	}
	if v%100 >= 11 && v%100 <= 13 {
		return fmt.Sprintf("%dth", v)
	}
	switch v % 10 {
	case 1:
		return fmt.Sprintf("%dst", v)
	case 2:
		return fmt.Sprintf("%dnd", v)
	case 3:
		return fmt.Sprintf("%drd", v)
	default:
		return fmt.Sprintf("%dth", v)
	}
}

func trimCompactionInput(items []llm.ResponseItem, limit int) ([]llm.ResponseItem, int) {
	out := llm.CloneResponseItems(items)
	if limit <= 0 {
		return out, 0
	}
	trimmed := 0
	for estimateItemsTokens(out) > limit {
		trimmedIdx := -1
		for i, item := range out {
			if isCompactionTrimEligible(item) {
				trimmedIdx = i
				break
			}
		}
		if trimmedIdx < 0 {
			break
		}
		out = append(out[:trimmedIdx], out[trimmedIdx+1:]...)
		trimmed++
	}
	return out, trimmed
}

func trimOldestEligibleItems(items []llm.ResponseItem, count int) ([]llm.ResponseItem, int) {
	out := llm.CloneResponseItems(items)
	if count <= 0 {
		return out, 0
	}
	trimmed := 0
	for trimmed < count {
		trimmedIdx := -1
		for i, item := range out {
			if isCompactionTrimEligible(item) {
				trimmedIdx = i
				break
			}
		}
		if trimmedIdx < 0 {
			break
		}
		out = append(out[:trimmedIdx], out[trimmedIdx+1:]...)
		trimmed++
	}
	return out, trimmed
}

func isCompactionTrimEligible(item llm.ResponseItem) bool {
	if item.Type != llm.ResponseItemTypeMessage {
		return true
	}
	return item.Role != llm.RoleUser
}

func sanitizeRemoteCompactionOutput(output []llm.ResponseItem) ([]llm.ResponseItem, error) {
	filtered := make([]llm.ResponseItem, 0, len(output))
	hasCheckpoint := false
	typeCounts := make(map[string]int)
	for _, item := range output {
		typeCounts[outputItemTypeLabel(item)]++
		switch item.Type {
		case llm.ResponseItemTypeMessage:
			if item.Role == llm.RoleUser && strings.TrimSpace(item.Content) != "" {
				filtered = append(filtered, item)
			}
		case llm.ResponseItemTypeCompaction:
			if strings.TrimSpace(item.EncryptedContent) == "" {
				continue
			}
			filtered = append(filtered, item)
			hasCheckpoint = true
		case llm.ResponseItemTypeReasoning:
			if strings.TrimSpace(item.EncryptedContent) == "" {
				continue
			}
			filtered = append(filtered, item)
			hasCheckpoint = true
		case llm.ResponseItemTypeOther:
			if !itemHasEncryptedCheckpoint(item) {
				continue
			}
			filtered = append(filtered, item)
			hasCheckpoint = true
		}
	}
	if !hasCheckpoint {
		return nil, fmt.Errorf("%w (types=%s)", errRemoteCompactionMissingCheckpoint, formatOutputTypeCounts(typeCounts))
	}
	return filtered, nil
}

func outputItemTypeLabel(item llm.ResponseItem) string {
	if v := strings.TrimSpace(string(item.Type)); v != "" {
		return v
	}
	if len(item.Raw) > 0 {
		var decoded struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(item.Raw, &decoded); err == nil {
			if v := strings.TrimSpace(decoded.Type); v != "" {
				return v
			}
		}
	}
	return "unknown"
}

func itemHasEncryptedCheckpoint(item llm.ResponseItem) bool {
	if strings.TrimSpace(item.EncryptedContent) != "" {
		return true
	}
	if len(item.Raw) == 0 || !json.Valid(item.Raw) {
		return false
	}
	var decoded struct {
		EncryptedContent string `json:"encrypted_content"`
	}
	if err := json.Unmarshal(item.Raw, &decoded); err != nil {
		return false
	}
	return strings.TrimSpace(decoded.EncryptedContent) != ""
}

func formatOutputTypeCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", key, counts[key]))
	}
	return strings.Join(parts, ",")
}

func extractCanonicalContext(items []llm.ResponseItem) []llm.ResponseItem {
	contextItems := make([]llm.ResponseItem, 0, 8)
	for _, item := range items {
		if item.Type != llm.ResponseItemTypeMessage {
			continue
		}
		if item.Role == llm.RoleUser {
			break
		}
		if item.Role == llm.RoleDeveloper || item.Role == llm.RoleSystem {
			contextItems = append(contextItems, item)
		}
	}
	return llm.CloneResponseItems(contextItems)
}

func rebuildLocalCompactionHistory(items []llm.ResponseItem, summary string, carryoverLimit int) []llm.ResponseItem {
	contextItems := extractCanonicalContext(items)
	userMessages := make([]llm.ResponseItem, 0, len(items))
	for _, item := range items {
		if item.Type == llm.ResponseItemTypeMessage && item.Role == llm.RoleUser && strings.TrimSpace(item.Content) != "" {
			userMessages = append(userMessages, item)
		}
	}

	if carryoverLimit <= 0 {
		carryoverLimit = 20_000
	}
	selected := make([]llm.ResponseItem, 0, len(userMessages))
	budget := 0
	for i := len(userMessages) - 1; i >= 0; i-- {
		item := userMessages[i]
		estimated := estimateItemsTokens([]llm.ResponseItem{item})
		if len(selected) > 0 && budget+estimated > carryoverLimit {
			break
		}
		budget += estimated
		selected = append(selected, item)
	}
	for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
		selected[i], selected[j] = selected[j], selected[i]
	}

	summaryMessage := llm.ResponseItem{
		Type:    llm.ResponseItemTypeMessage,
		Role:    llm.RoleUser,
		Content: prompts.CompactionSummaryPrefix + "\n\n" + strings.TrimSpace(summary),
	}

	out := make([]llm.ResponseItem, 0, len(contextItems)+len(selected)+1)
	out = append(out, contextItems...)
	out = append(out, selected...)
	out = append(out, summaryMessage)
	return out
}

func estimateItemsTokens(items []llm.ResponseItem) int {
	totalChars := 0
	for _, item := range items {
		totalChars += len(item.Content)
		totalChars += len(item.ID)
		totalChars += len(item.Name)
		totalChars += len(item.CallID)
		totalChars += len(item.EncryptedContent)
		totalChars += len(item.Arguments)
		totalChars += len(item.Output)
		totalChars += len(item.Raw)
		for _, summary := range item.ReasoningSummary {
			totalChars += len(summary.Role) + len(summary.Text)
		}
	}
	if totalChars <= 0 {
		return 0
	}
	return totalChars / 4
}
