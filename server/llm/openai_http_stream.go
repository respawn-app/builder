package llm

import (
	"fmt"
	"strings"

	"builder/shared/textutil"
	"github.com/openai/openai-go/v3/responses"
)

type responseStreamAccumulator struct {
	callbacks     StreamCallbacks
	windowTokens  int
	assistantText strings.Builder
	toolCalls     *toolCallAccumulator
	reasoning     *reasoningAccumulator
	completed     *responses.Response
}

func newResponseStreamAccumulator(callbacks StreamCallbacks, windowTokens int) *responseStreamAccumulator {
	return &responseStreamAccumulator{
		callbacks:    callbacks,
		windowTokens: windowTokens,
		toolCalls:    newToolCallAccumulator(),
		reasoning:    newReasoningAccumulator(),
	}
}

func (a *responseStreamAccumulator) Consume(evt responses.ResponseStreamEventUnion) {
	switch evt.Type {
	case "response.output_text.delta":
		if evt.Delta == "" {
			return
		}
		a.assistantText.WriteString(evt.Delta)
		if a.callbacks.OnAssistantDelta != nil {
			a.callbacks.OnAssistantDelta(evt.Delta)
		}
	case "response.output_item.added", "response.output_item.done":
		a.toolCalls.UpsertFromOutput(evt.Item)
		a.reasoning.UpsertReasoningItem(evt.Item)
	case "response.function_call_arguments.delta":
		a.toolCalls.AppendArguments(evt.ItemID, evt.Delta)
	case "response.function_call_arguments.done":
		a.toolCalls.SetArguments(evt.ItemID, evt.Arguments)
	case "response.reasoning_summary_text.delta":
		key := reasoningEventKey(evt.ItemID, evt.OutputIndex, evt.SummaryIndex)
		a.reasoning.Append(reasoningRoleSummary, key, evt.Delta)
		a.emitReasoningSummaryDelta(key)
	case "response.reasoning_summary_text.done":
		key := reasoningEventKey(evt.ItemID, evt.OutputIndex, evt.SummaryIndex)
		a.reasoning.Set(reasoningRoleSummary, key, evt.Text)
		a.emitReasoningSummaryDelta(key)
	case "response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
		if evt.Part.Type != "summary_text" {
			return
		}
		key := reasoningEventKey(evt.ItemID, evt.OutputIndex, evt.SummaryIndex)
		a.reasoning.Set(reasoningRoleSummary, key, evt.Part.Text)
		a.emitReasoningSummaryDelta(key)
	case "response.completed":
		completed := evt.AsResponseCompleted().Response
		a.completed = &completed
	}
}

func (a *responseStreamAccumulator) emitReasoningSummaryDelta(key string) {
	if a.callbacks.OnReasoningSummaryDelta == nil {
		return
	}
	a.callbacks.OnReasoningSummaryDelta(reasoningSummaryDeltaFromText(key, reasoningRoleSummary, a.reasoning.Current(reasoningRoleSummary, key)))
}

func (a *responseStreamAccumulator) Response() OpenAIResponse {
	usage := Usage{WindowTokens: a.windowTokens}
	finalText := a.assistantText.String()
	finalCalls := a.toolCalls.ToToolCalls()
	finalReasoning := a.reasoning.Entries()
	finalReasoningItems := a.reasoning.Items()
	finalOutputItems := buildOutputItemsFromStream(finalText, finalCalls, finalReasoning, finalReasoningItems)

	if a.completed == nil {
		return OpenAIResponse{
			AssistantText:  finalText,
			AssistantPhase: "",
			ToolCalls:      finalCalls,
			Reasoning:      normalizeReasoningEntries(finalReasoning),
			ReasoningItems: finalReasoningItems,
			OutputItems:    finalOutputItems,
			Usage:          usage,
		}
	}

	if a.completed.Usage.InputTokens > 0 || a.completed.Usage.OutputTokens > 0 {
		usage = usageFromSDK(a.completed.Usage, a.windowTokens)
	}
	parsedItems, parsedText, parsedPhase, parsedCalls, parsedReasoning, parsedReasoningItems := parseOutputItems(a.completed.Output)
	finalText = parsedText
	finalPhase := MessagePhase("")
	if parsedPhase != "" {
		finalPhase = parsedPhase
	}
	a.toolCalls.Merge(parsedCalls)
	finalCalls = a.toolCalls.ToToolCalls()
	finalReasoning = normalizeReasoningEntries(mergeReasoningEntries(parsedReasoning, finalReasoning))
	finalReasoningItems = mergeReasoningItems(parsedReasoningItems, finalReasoningItems)
	if len(parsedItems) > 0 {
		finalOutputItems = parsedItems
	}

	return OpenAIResponse{
		AssistantText:  finalText,
		AssistantPhase: finalPhase,
		ToolCalls:      finalCalls,
		Reasoning:      finalReasoning,
		ReasoningItems: finalReasoningItems,
		OutputItems:    finalOutputItems,
		Usage:          usage,
	}
}

func mergeReasoningEntries(primary, secondary []ReasoningEntry) []ReasoningEntry {
	out := make([]ReasoningEntry, 0, len(primary)+len(secondary))
	seen := make(map[string]struct{}, len(primary)+len(secondary))
	appendEntries := func(entries []ReasoningEntry) {
		for _, entry := range entries {
			role := strings.TrimSpace(entry.Role)
			text := strings.TrimSpace(entry.Text)
			if role == "" || text == "" {
				continue
			}
			key := role + "\x00" + text
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, ReasoningEntry{Role: role, Text: text})
		}
	}
	appendEntries(primary)
	appendEntries(secondary)
	return out
}

func mergeReasoningItems(primary, secondary []ReasoningItem) []ReasoningItem {
	out := make([]ReasoningItem, 0, len(primary)+len(secondary))
	seen := make(map[string]struct{}, len(primary)+len(secondary))
	appendItems := func(items []ReasoningItem) {
		for _, item := range items {
			id := strings.TrimSpace(item.ID)
			encrypted := strings.TrimSpace(item.EncryptedContent)
			if id == "" || encrypted == "" {
				continue
			}
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, ReasoningItem{ID: id, EncryptedContent: encrypted})
		}
	}
	appendItems(primary)
	appendItems(secondary)
	return out
}

func reasoningEventKey(itemID string, outputIndex, partIndex int64) string {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return fmt.Sprintf("output:%d:part:%d", outputIndex, partIndex)
	}
	return fmt.Sprintf("%s:part:%d", itemID, partIndex)
}

type reasoningAccumulator struct {
	order         []string
	items         map[string]*ReasoningEntry
	reasoningIDs  []string
	reasoningByID map[string]ReasoningItem
}

func newReasoningAccumulator() *reasoningAccumulator {
	return &reasoningAccumulator{
		order:         make([]string, 0, 8),
		items:         make(map[string]*ReasoningEntry, 8),
		reasoningIDs:  make([]string, 0, 4),
		reasoningByID: make(map[string]ReasoningItem, 4),
	}
}

func (a *reasoningAccumulator) ensure(role, key string) *ReasoningEntry {
	role = strings.TrimSpace(role)
	key = strings.TrimSpace(key)
	if role == "" || key == "" {
		return nil
	}
	composite := role + "\x00" + key
	if item, ok := a.items[composite]; ok {
		return item
	}
	entry := &ReasoningEntry{Role: role}
	a.items[composite] = entry
	a.order = append(a.order, composite)
	return entry
}

func (a *reasoningAccumulator) Append(role, key, delta string) {
	if delta == "" {
		return
	}
	entry := a.ensure(role, key)
	if entry == nil {
		return
	}
	entry.Text += delta
}

func (a *reasoningAccumulator) Set(role, key, text string) {
	if text == "" {
		return
	}
	entry := a.ensure(role, key)
	if entry == nil {
		return
	}
	entry.Text = text
}

func (a *reasoningAccumulator) Current(role, key string) string {
	entry := a.ensure(role, key)
	if entry == nil {
		return ""
	}
	return entry.Text
}

func (a *reasoningAccumulator) Entries() []ReasoningEntry {
	if a == nil {
		return nil
	}
	out := make([]ReasoningEntry, 0, len(a.order))
	for _, key := range a.order {
		entry, ok := a.items[key]
		if !ok {
			continue
		}
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			continue
		}
		out = append(out, ReasoningEntry{Role: entry.Role, Text: text})
	}
	return out
}

func (a *reasoningAccumulator) UpsertReasoningItem(item responses.ResponseOutputItemUnion) {
	if item.Type != "reasoning" {
		return
	}
	reasoningItem := item.AsReasoning()
	id := strings.TrimSpace(reasoningItem.ID)
	if id == "" {
		return
	}
	for idx, summary := range reasoningItem.Summary {
		key := fmt.Sprintf("%s:summary:%d", id, idx)
		a.Set(reasoningRoleSummary, key, summary.Text)
	}
	encrypted := strings.TrimSpace(reasoningItem.EncryptedContent)
	if encrypted == "" {
		return
	}
	if _, exists := a.reasoningByID[id]; !exists {
		a.reasoningIDs = append(a.reasoningIDs, id)
	}
	a.reasoningByID[id] = ReasoningItem{ID: id, EncryptedContent: encrypted}
}

func (a *reasoningAccumulator) Items() []ReasoningItem {
	if a == nil {
		return nil
	}
	out := make([]ReasoningItem, 0, len(a.reasoningIDs))
	for _, id := range a.reasoningIDs {
		item, ok := a.reasoningByID[id]
		if !ok {
			continue
		}
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.EncryptedContent) == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

type toolCallAccumulator struct {
	byKey     map[string]*toolCallState
	itemToKey map[string]string
	order     []string
}

type toolCallState struct {
	CallID string
	Name   string
	Args   strings.Builder
}

func newToolCallAccumulator() *toolCallAccumulator {
	return &toolCallAccumulator{
		byKey:     map[string]*toolCallState{},
		itemToKey: map[string]string{},
		order:     []string{},
	}
}

func (a *toolCallAccumulator) ensure(key string) *toolCallState {
	if key == "" {
		return nil
	}
	if state, ok := a.byKey[key]; ok {
		return state
	}
	state := &toolCallState{CallID: key}
	a.byKey[key] = state
	a.order = append(a.order, key)
	return state
}

func (a *toolCallAccumulator) UpsertFromOutput(item responses.ResponseOutputItemUnion) {
	if item.Type != "function_call" {
		return
	}
	call := item.AsFunctionCall()
	key := textutil.FirstNonEmpty(strings.TrimSpace(call.CallID), strings.TrimSpace(call.ID))
	if key == "" {
		return
	}
	state := a.ensure(key)
	if state == nil {
		return
	}
	if v := strings.TrimSpace(call.CallID); v != "" {
		state.CallID = v
	}
	if v := strings.TrimSpace(call.Name); v != "" {
		state.Name = v
	}
	if call.ID != "" {
		a.itemToKey[call.ID] = key
	}
	if args := strings.TrimSpace(call.Arguments); args != "" {
		state.Args.Reset()
		state.Args.WriteString(args)
	}
}

func (a *toolCallAccumulator) AppendArguments(itemID, delta string) {
	key := textutil.FirstNonEmpty(strings.TrimSpace(a.itemToKey[itemID]), strings.TrimSpace(itemID))
	state := a.ensure(key)
	if state == nil || strings.TrimSpace(delta) == "" {
		return
	}
	state.Args.WriteString(delta)
}

func (a *toolCallAccumulator) SetArguments(itemID, arguments string) {
	key := textutil.FirstNonEmpty(strings.TrimSpace(a.itemToKey[itemID]), strings.TrimSpace(itemID))
	state := a.ensure(key)
	if state == nil {
		return
	}
	state.Args.Reset()
	state.Args.WriteString(arguments)
}

func (a *toolCallAccumulator) Merge(calls []ToolCall) {
	for _, call := range calls {
		key := textutil.FirstNonEmpty(strings.TrimSpace(call.ID), strings.TrimSpace(call.Name))
		state := a.ensure(key)
		if state == nil {
			continue
		}
		if v := strings.TrimSpace(call.ID); v != "" {
			state.CallID = v
		}
		if v := strings.TrimSpace(call.Name); v != "" {
			state.Name = v
		}
		if len(call.Input) > 0 {
			state.Args.Reset()
			state.Args.WriteString(normalizeToolArguments(string(call.Input)))
		}
	}
}

func (a *toolCallAccumulator) ToToolCalls() []ToolCall {
	out := make([]ToolCall, 0, len(a.order))
	for _, key := range a.order {
		state, ok := a.byKey[key]
		if !ok {
			continue
		}
		callID := textutil.FirstNonEmpty(strings.TrimSpace(state.CallID), key)
		if callID == "" && strings.TrimSpace(state.Name) == "" {
			continue
		}
		out = append(out, ToolCall{
			ID:    callID,
			Name:  state.Name,
			Input: normalizeToolInput(state.Args.String()),
		})
	}
	return out
}

func buildOutputItemsFromStream(text string, toolCalls []ToolCall, reasoning []ReasoningEntry, reasoningItems []ReasoningItem) []ResponseItem {
	items := make([]ResponseItem, 0, 1+len(toolCalls)+len(reasoningItems))
	if strings.TrimSpace(text) != "" {
		items = append(items, ResponseItem{Type: ResponseItemTypeMessage, Role: RoleAssistant, Content: text})
	}
	for _, call := range toolCalls {
		callID := textutil.FirstNonEmpty(strings.TrimSpace(call.ID), strings.TrimSpace(call.Name))
		if callID == "" {
			continue
		}
		items = append(items, ResponseItem{
			Type:      ResponseItemTypeFunctionCall,
			ID:        callID,
			CallID:    callID,
			Name:      call.Name,
			Arguments: normalizeToolInput(string(call.Input)),
		})
	}
	summaries := make([]ReasoningEntry, 0, len(reasoning))
	for _, entry := range reasoning {
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			continue
		}
		summaries = append(summaries, ReasoningEntry{Role: entry.Role, Text: text})
	}
	for _, item := range reasoningItems {
		id := strings.TrimSpace(item.ID)
		encrypted := strings.TrimSpace(item.EncryptedContent)
		if id == "" || encrypted == "" {
			continue
		}
		items = append(items, ResponseItem{
			Type:             ResponseItemTypeReasoning,
			ID:               id,
			EncryptedContent: encrypted,
			ReasoningSummary: append([]ReasoningEntry(nil), summaries...),
		})
	}
	return items
}
