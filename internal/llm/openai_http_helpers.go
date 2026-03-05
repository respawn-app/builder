package llm

import (
	"encoding/json"
	"fmt"
	"strings"

	"builder/internal/shared/textutil"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
)

func buildResponsesInput(messages []Message, canonical []ResponseItem) []responses.ResponseInputItemUnionParam {
	if len(canonical) > 0 {
		return buildResponsesInputFromItems(canonical)
	}
	return buildResponsesInputFromMessages(messages)
}

func buildResponsesInputFromMessages(messages []Message) []responses.ResponseInputItemUnionParam {
	var items []responses.ResponseInputItemUnionParam
	for _, msg := range messages {
		switch msg.Role {
		case RoleTool:
			if strings.TrimSpace(msg.ToolCallID) == "" {
				continue
			}
			items = append(items, functionCallOutputInputItems(msg.ToolCallID, msg.Name, normalizeToolInput(msg.Content))...)
		case RoleAssistant:
			if strings.TrimSpace(msg.Content) != "" {
				items = append(items, messageInput(string(msg.Role), msg.Content))
			}
			for _, tc := range msg.ToolCalls {
				callID := strings.TrimSpace(tc.ID)
				if callID == "" {
					continue
				}
				items = append(items, responses.ResponseInputItemParamOfFunctionCall(normalizeToolArguments(string(tc.Input)), callID, tc.Name))
			}
			for _, ri := range msg.ReasoningItems {
				id := strings.TrimSpace(ri.ID)
				encrypted := strings.TrimSpace(ri.EncryptedContent)
				if id == "" || encrypted == "" {
					continue
				}
				items = append(items, responses.ResponseInputItemUnionParam{
					OfReasoning: &responses.ResponseReasoningItemParam{
						ID:               id,
						Summary:          []responses.ResponseReasoningItemSummaryParam{},
						EncryptedContent: param.NewOpt(encrypted),
					},
				})
			}
		default:
			if strings.TrimSpace(msg.Content) == "" {
				continue
			}
			items = append(items, messageInput(string(msg.Role), msg.Content))
		}
	}
	return items
}

func buildResponsesInputFromItems(canonical []ResponseItem) []responses.ResponseInputItemUnionParam {
	items := make([]responses.ResponseInputItemUnionParam, 0, len(canonical))
	for _, item := range canonical {
		switch item.Type {
		case ResponseItemTypeMessage:
			if strings.TrimSpace(item.Content) == "" {
				continue
			}
			items = append(items, messageInput(string(item.Role), item.Content))
		case ResponseItemTypeFunctionCall:
			callID := textutil.FirstNonEmpty(strings.TrimSpace(item.CallID), strings.TrimSpace(item.ID))
			if callID == "" {
				continue
			}
			items = append(items, responses.ResponseInputItemParamOfFunctionCall(normalizeToolArguments(string(item.Arguments)), callID, strings.TrimSpace(item.Name)))
		case ResponseItemTypeFunctionCallOutput:
			callID := strings.TrimSpace(item.CallID)
			if callID == "" {
				continue
			}
			items = append(items, functionCallOutputInputItems(callID, item.Name, item.Output)...)
		case ResponseItemTypeReasoning:
			id := strings.TrimSpace(item.ID)
			if id == "" {
				continue
			}
			reasoningParam := responses.ResponseReasoningItemParam{
				ID:      id,
				Summary: []responses.ResponseReasoningItemSummaryParam{},
			}
			for _, summary := range item.ReasoningSummary {
				text := strings.TrimSpace(summary.Text)
				if text == "" {
					continue
				}
				reasoningParam.Summary = append(reasoningParam.Summary, responses.ResponseReasoningItemSummaryParam{
					Text: text,
					Type: "summary_text",
				})
			}
			if encrypted := strings.TrimSpace(item.EncryptedContent); encrypted != "" {
				reasoningParam.EncryptedContent = param.NewOpt(encrypted)
			}
			items = append(items, responses.ResponseInputItemUnionParam{
				OfReasoning: &reasoningParam,
			})
		case ResponseItemTypeCompaction:
			encrypted := strings.TrimSpace(item.EncryptedContent)
			if encrypted == "" {
				continue
			}
			compactionParam := responses.ResponseCompactionItemParam{
				EncryptedContent: encrypted,
			}
			if id := strings.TrimSpace(item.ID); id != "" {
				compactionParam.ID = param.NewOpt(id)
			}
			items = append(items, responses.ResponseInputItemUnionParam{
				OfCompaction: &compactionParam,
			})
		default:
			if len(item.Raw) == 0 || !json.Valid(item.Raw) {
				continue
			}
			items = append(items, param.Override[responses.ResponseInputItemUnionParam](item.Raw))
		}
	}
	return items
}

func messageInput(role, text string) responses.ResponseInputItemUnionParam {
	role = strings.TrimSpace(role)
	if role == string(RoleAssistant) {
		content := []responses.ResponseOutputMessageContentUnionParam{
			{
				OfOutputText: &responses.ResponseOutputTextParam{
					Annotations: []responses.ResponseOutputTextAnnotationUnionParam{},
					Text:        text,
				},
			},
		}
		return responses.ResponseInputItemParamOfOutputMessage(content, "", responses.ResponseOutputMessageStatusCompleted)
	}

	inputRole := string(RoleUser)
	switch role {
	case string(RoleSystem), string(RoleDeveloper), string(RoleUser):
		inputRole = role
	}
	content := responses.ResponseInputMessageContentListParam{
		responses.ResponseInputContentParamOfInputText(text),
	}
	return responses.ResponseInputItemParamOfInputMessage(content, inputRole)
}

func parseOutputItems(items []responses.ResponseOutputItemUnion) ([]ResponseItem, string, MessagePhase, []ToolCall, []ReasoningEntry, []ReasoningItem) {
	canonical := make([]ResponseItem, 0, len(items))
	assistantSegments := make([]assistantOutputSegment, 0, len(items))
	toolCalls := make([]ToolCall, 0, len(items))
	reasoning := make([]ReasoningEntry, 0, len(items))
	reasoningItems := make([]ReasoningItem, 0, len(items))
	for _, item := range items {
		raw := json.RawMessage(item.RawJSON())
		switch item.Type {
		case "message":
			role := Role(strings.TrimSpace(string(item.Role)))
			if role == "" {
				role = RoleAssistant
			}
			textPartsForItem := make([]string, 0, len(item.Content))
			for _, part := range item.Content {
				if part.Type == "output_text" || part.Type == "text" || part.Type == "input_text" {
					textPartsForItem = append(textPartsForItem, part.Text)
				}
			}
			text := strings.Join(textPartsForItem, "")
			phase := parseMessagePhaseFromRaw(raw)
			canonical = append(canonical, ResponseItem{
				Type:    ResponseItemTypeMessage,
				Role:    role,
				Phase:   phase,
				ID:      item.ID,
				Content: text,
				Raw:     raw,
			})
			if role == RoleAssistant {
				assistantSegments = append(assistantSegments, assistantOutputSegment{
					Text:  text,
					Phase: phase,
				})
			}
		case "function_call":
			callID := textutil.FirstNonEmpty(strings.TrimSpace(item.CallID), strings.TrimSpace(item.ID))
			if callID == "" && strings.TrimSpace(item.Name) == "" {
				continue
			}
			args := normalizeToolInput(item.Arguments)
			canonical = append(canonical, ResponseItem{
				Type:      ResponseItemTypeFunctionCall,
				ID:        strings.TrimSpace(item.ID),
				CallID:    callID,
				Name:      item.Name,
				Arguments: args,
				Raw:       raw,
			})
			toolCalls = append(toolCalls, ToolCall{
				ID:    callID,
				Name:  item.Name,
				Input: args,
			})
		case "reasoning":
			reasoningItem := item.AsReasoning()
			reasoningSummary := make([]ReasoningEntry, 0, len(reasoningItem.Summary))
			for _, summary := range reasoningItem.Summary {
				if strings.TrimSpace(summary.Text) != "" {
					reasoningSummary = append(reasoningSummary, ReasoningEntry{
						Role: reasoningRoleSummary,
						Text: summary.Text,
					})
				}
				reasoning = appendReasoningEntry(reasoning, reasoningRoleSummary, summary.Text)
			}
			canonicalReasoning := ResponseItem{
				Type:             ResponseItemTypeReasoning,
				ID:               strings.TrimSpace(reasoningItem.ID),
				ReasoningSummary: reasoningSummary,
				EncryptedContent: strings.TrimSpace(reasoningItem.EncryptedContent),
				Raw:              raw,
			}
			canonical = append(canonical, canonicalReasoning)
			if id := strings.TrimSpace(reasoningItem.ID); id != "" {
				if encrypted := strings.TrimSpace(reasoningItem.EncryptedContent); encrypted != "" {
					reasoningItems = append(reasoningItems, ReasoningItem{
						ID:               id,
						EncryptedContent: encrypted,
					})
				}
			}
		case "compaction":
			compactionItem := item.AsCompaction()
			canonical = append(canonical, ResponseItem{
				Type:             ResponseItemTypeCompaction,
				ID:               strings.TrimSpace(compactionItem.ID),
				EncryptedContent: strings.TrimSpace(compactionItem.EncryptedContent),
				Raw:              raw,
			})
		default:
			if len(raw) > 0 && json.Valid(raw) {
				canonical = append(canonical, ResponseItem{
					Type: ResponseItemTypeOther,
					Raw:  raw,
				})
			}
		}
	}
	assistantText, assistantPhase := resolveAssistantOutput(assistantSegments)
	return canonical, assistantText, assistantPhase, toolCalls, reasoning, reasoningItems
}

type assistantOutputSegment struct {
	Text  string
	Phase MessagePhase
}

func resolveAssistantOutput(segments []assistantOutputSegment) (string, MessagePhase) {
	if len(segments) == 0 {
		return "", ""
	}
	last := len(segments) - 1
	if segments[last].Phase == "" {
		return segments[last].Text, ""
	}
	phase := segments[last].Phase
	start := last
	for start > 0 {
		if segments[start-1].Phase != phase {
			break
		}
		start--
	}
	textParts := make([]string, 0, last-start+1)
	for i := start; i <= last; i++ {
		textParts = append(textParts, segments[i].Text)
	}
	return strings.Join(textParts, ""), phase
}

func parseMessagePhaseFromRaw(raw json.RawMessage) MessagePhase {
	if len(raw) == 0 || !json.Valid(raw) {
		return ""
	}
	var payload struct {
		Phase string `json:"phase"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return normalizeMessagePhase(payload.Phase)
}

func appendReasoningEntry(entries []ReasoningEntry, role, text string) []ReasoningEntry {
	text = strings.TrimSpace(text)
	if text == "" {
		return entries
	}
	return append(entries, ReasoningEntry{
		Role: role,
		Text: text,
	})
}

func mergeReasoningEntries(primary, secondary []ReasoningEntry) []ReasoningEntry {
	out := make([]ReasoningEntry, 0, len(primary)+len(secondary))
	seen := make(map[string]struct{}, len(primary)+len(secondary))
	add := func(entries []ReasoningEntry) {
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
	add(primary)
	add(secondary)
	return out
}

func mergeReasoningItems(primary, secondary []ReasoningItem) []ReasoningItem {
	out := make([]ReasoningItem, 0, len(primary)+len(secondary))
	seen := make(map[string]struct{}, len(primary)+len(secondary))
	add := func(items []ReasoningItem) {
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
			out = append(out, ReasoningItem{
				ID:               id,
				EncryptedContent: encrypted,
			})
		}
	}
	add(primary)
	add(secondary)
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
	delta = strings.TrimSpace(delta)
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
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	entry := a.ensure(role, key)
	if entry == nil {
		return
	}
	entry.Text = text
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
		out = append(out, ReasoningEntry{
			Role: entry.Role,
			Text: text,
		})
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
	a.reasoningByID[id] = ReasoningItem{
		ID:               id,
		EncryptedContent: encrypted,
	}
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
	if s, ok := a.byKey[key]; ok {
		return s
	}
	s := &toolCallState{CallID: key}
	a.byKey[key] = s
	a.order = append(a.order, key)
	return s
}

func (a *toolCallAccumulator) UpsertFromOutput(item responses.ResponseOutputItemUnion) {
	if item.Type != "function_call" {
		return
	}
	key := textutil.FirstNonEmpty(strings.TrimSpace(item.CallID), strings.TrimSpace(item.ID))
	if key == "" {
		return
	}
	state := a.ensure(key)
	if state == nil {
		return
	}
	if v := strings.TrimSpace(item.CallID); v != "" {
		state.CallID = v
	}
	if v := strings.TrimSpace(item.Name); v != "" {
		state.Name = v
	}
	if item.ID != "" {
		a.itemToKey[item.ID] = key
	}
	if args := strings.TrimSpace(item.Arguments); args != "" {
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
