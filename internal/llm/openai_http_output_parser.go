package llm

import (
	"encoding/json"
	"strings"

	"builder/internal/shared/textutil"
	"github.com/openai/openai-go/v3/responses"
)

type responseOutputItemParser interface {
	ItemType() string
	Parse(item responses.ResponseOutputItemUnion) parsedResponseOutputItem
}

type parsedResponseOutputItem struct {
	CanonicalItems    []ResponseItem
	AssistantSegments []assistantOutputSegment
	ToolCalls         []ToolCall
	Reasoning         []ReasoningEntry
	ReasoningItems    []ReasoningItem
}

type responseOutputItemParsers struct {
	byType map[string]responseOutputItemParser
}

func newResponseOutputItemParsers(parsers ...responseOutputItemParser) responseOutputItemParsers {
	byType := make(map[string]responseOutputItemParser, len(parsers))
	for _, parser := range parsers {
		byType[parser.ItemType()] = parser
	}
	return responseOutputItemParsers{byType: byType}
}

func defaultResponseOutputItemParsers() responseOutputItemParsers {
	return newResponseOutputItemParsers(
		messageOutputItemParser{},
		functionCallOutputItemParser{},
		reasoningOutputItemParser{},
		compactionOutputItemParser{},
	)
}

func parseOutputItems(items []responses.ResponseOutputItemUnion) ([]ResponseItem, string, MessagePhase, []ToolCall, []ReasoningEntry, []ReasoningItem) {
	parsers := defaultResponseOutputItemParsers()
	canonical := make([]ResponseItem, 0, len(items))
	assistantSegments := make([]assistantOutputSegment, 0, len(items))
	toolCalls := make([]ToolCall, 0, len(items))
	reasoning := make([]ReasoningEntry, 0, len(items))
	reasoningItems := make([]ReasoningItem, 0, len(items))

	for _, item := range items {
		parsed, ok := parsers.byType[item.Type]
		if !ok {
			raw := json.RawMessage(item.RawJSON())
			if len(raw) > 0 && json.Valid(raw) {
				canonical = append(canonical, ResponseItem{Type: ResponseItemTypeOther, Raw: raw})
			}
			continue
		}
		contribution := parsed.Parse(item)
		canonical = append(canonical, contribution.CanonicalItems...)
		assistantSegments = append(assistantSegments, contribution.AssistantSegments...)
		toolCalls = append(toolCalls, contribution.ToolCalls...)
		reasoning = append(reasoning, contribution.Reasoning...)
		reasoningItems = append(reasoningItems, contribution.ReasoningItems...)
	}

	assistantText, assistantPhase := resolveAssistantOutput(assistantSegments)
	return canonical, assistantText, assistantPhase, toolCalls, reasoning, reasoningItems
}

type messageOutputItemParser struct{}

func (messageOutputItemParser) ItemType() string { return "message" }

func (messageOutputItemParser) Parse(item responses.ResponseOutputItemUnion) parsedResponseOutputItem {
	role := Role(strings.TrimSpace(string(item.Role)))
	if role == "" {
		role = RoleAssistant
	}
	textParts := make([]string, 0, len(item.Content))
	for _, part := range item.Content {
		if part.Type == "output_text" || part.Type == "text" || part.Type == "input_text" {
			textParts = append(textParts, part.Text)
		}
	}
	text := strings.Join(textParts, "")
	phase := parseMessagePhaseFromRaw(json.RawMessage(item.RawJSON()))
	raw := json.RawMessage(item.RawJSON())
	parsed := parsedResponseOutputItem{
		CanonicalItems: []ResponseItem{{
			Type:    ResponseItemTypeMessage,
			Role:    role,
			Phase:   phase,
			ID:      item.ID,
			Content: text,
			Raw:     raw,
		}},
	}
	if role == RoleAssistant {
		parsed.AssistantSegments = append(parsed.AssistantSegments, assistantOutputSegment{Text: text, Phase: phase})
	}
	return parsed
}

type functionCallOutputItemParser struct{}

func (functionCallOutputItemParser) ItemType() string { return "function_call" }

func (functionCallOutputItemParser) Parse(item responses.ResponseOutputItemUnion) parsedResponseOutputItem {
	call := item.AsFunctionCall()
	callID := textutil.FirstNonEmpty(strings.TrimSpace(call.CallID), strings.TrimSpace(call.ID))
	name := strings.TrimSpace(call.Name)
	if callID == "" && name == "" {
		return parsedResponseOutputItem{}
	}
	arguments := normalizeToolInput(call.Arguments)
	raw := json.RawMessage(item.RawJSON())
	return parsedResponseOutputItem{
		CanonicalItems: []ResponseItem{{
			Type:      ResponseItemTypeFunctionCall,
			ID:        strings.TrimSpace(call.ID),
			CallID:    callID,
			Name:      call.Name,
			Arguments: arguments,
			Raw:       raw,
		}},
		ToolCalls: []ToolCall{{
			ID:    callID,
			Name:  call.Name,
			Input: arguments,
		}},
	}
}

type reasoningOutputItemParser struct{}

func (reasoningOutputItemParser) ItemType() string { return "reasoning" }

func (reasoningOutputItemParser) Parse(item responses.ResponseOutputItemUnion) parsedResponseOutputItem {
	reasoningItem := item.AsReasoning()
	summaries := make([]ReasoningEntry, 0, len(reasoningItem.Summary))
	reasoning := make([]ReasoningEntry, 0, len(reasoningItem.Summary))
	for _, summary := range reasoningItem.Summary {
		text := strings.TrimSpace(summary.Text)
		if text == "" {
			continue
		}
		entry := ReasoningEntry{Role: reasoningRoleSummary, Text: text}
		summaries = append(summaries, entry)
		reasoning = append(reasoning, entry)
	}
	raw := json.RawMessage(item.RawJSON())
	parsed := parsedResponseOutputItem{
		CanonicalItems: []ResponseItem{{
			Type:             ResponseItemTypeReasoning,
			ID:               strings.TrimSpace(reasoningItem.ID),
			ReasoningSummary: summaries,
			EncryptedContent: strings.TrimSpace(reasoningItem.EncryptedContent),
			Raw:              raw,
		}},
		Reasoning: reasoning,
	}
	if id := strings.TrimSpace(reasoningItem.ID); id != "" {
		if encrypted := strings.TrimSpace(reasoningItem.EncryptedContent); encrypted != "" {
			parsed.ReasoningItems = append(parsed.ReasoningItems, ReasoningItem{ID: id, EncryptedContent: encrypted})
		}
	}
	return parsed
}

type compactionOutputItemParser struct{}

func (compactionOutputItemParser) ItemType() string { return "compaction" }

func (compactionOutputItemParser) Parse(item responses.ResponseOutputItemUnion) parsedResponseOutputItem {
	compactionItem := item.AsCompaction()
	return parsedResponseOutputItem{
		CanonicalItems: []ResponseItem{{
			Type:             ResponseItemTypeCompaction,
			ID:               strings.TrimSpace(compactionItem.ID),
			EncryptedContent: strings.TrimSpace(compactionItem.EncryptedContent),
			Raw:              json.RawMessage(item.RawJSON()),
		}},
	}
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
