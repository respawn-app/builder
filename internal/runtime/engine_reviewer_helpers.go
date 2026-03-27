package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"builder/internal/llm"
	"builder/internal/transcript"
)

type reviewerSuggestionsResult struct {
	Suggestions           []string
	CacheHitPercent       int
	HasCacheHitPercentage bool
}

type reviewerRequestConfig struct {
	Model         string
	ThinkingLevel string
}

func (e *Engine) runReviewerSuggestions(ctx context.Context, reviewerClient llm.Client) (reviewerSuggestionsResult, error) {
	e.ensureOrchestrationCollaborators()
	return e.reviewerFlow.RunSuggestions(ctx, reviewerClient)
}

func parseReviewerSuggestionsObject(content string) []string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil
	}

	var payload struct {
		Suggestions []string `json:"suggestions"`
	}
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return nil
	}
	return payload.Suggestions
}

func buildReviewerRequestMessages(messages []llm.Message, workspaceRoot string, model string, thinkingLevel string, headless bool, disabledSkills map[string]bool) ([]llm.Message, error) {
	metaMessages, transcriptSource := splitMetaContextMessages(messages)
	builder := newMetaContextBuilder(workspaceRoot, model, thinkingLevel, disabledSkills, time.Now())
	metaResult, err := builder.Build(metaContextBuildOptions{
		ExistingMessages:          metaMessages,
		IncludeAgents:             true,
		IncludeSkills:             true,
		IncludeEnvironment:        true,
		IncludeHeadless:           headless,
		PermissiveAgentsReadError: true,
	})
	if err != nil {
		return nil, err
	}
	metaMessages = metaResult.OrderedMetaMessages()
	out := make([]llm.Message, 0, len(metaMessages)+2+len(transcriptSource))
	out = append(out, metaMessages...)
	out = append(out, llm.Message{Role: llm.RoleDeveloper, Content: reviewerMetaBoundaryMessage})
	out = append(out, buildReviewerTranscriptMessages(transcriptSource)...)
	return out, nil
}

func buildReviewerTranscriptMessages(messages []llm.Message) []llm.Message {
	toolOutputsByCallID := collectReviewerToolOutputs(messages)
	toolCallIDs := collectReviewerToolCallIDs(messages)
	out := make([]llm.Message, 0, len(messages)+1)
	for _, message := range messages {
		if message.Role == llm.RoleTool {
			callID := strings.TrimSpace(message.ToolCallID)
			if callID != "" && toolCallIDs[callID] {
				continue
			}
		}
		if message.Role == llm.RoleTool && strings.TrimSpace(message.ToolCallID) == "" {
			continue
		}
		if !shouldIncludeReviewerMessage(message) {
			continue
		}
		out = append(out, llm.Message{Role: llm.RoleUser, Content: formatReviewerTranscriptEntry(message, toolOutputsByCallID)})
	}
	if len(out) == 0 {
		out = append(out, llm.Message{Role: llm.RoleUser, Content: "No reviewable transcript entries were available for this turn."})
	}
	return out
}

func collectReviewerToolOutputs(messages []llm.Message) map[string]string {
	out := make(map[string]string)
	for _, message := range messages {
		if message.Role != llm.RoleTool {
			continue
		}
		callID := strings.TrimSpace(message.ToolCallID)
		if callID == "" {
			continue
		}
		if _, exists := out[callID]; exists {
			continue
		}
		out[callID] = compactReviewerToolOutput(message.Content)
	}
	return out
}

func collectReviewerToolCallIDs(messages []llm.Message) map[string]bool {
	out := make(map[string]bool)
	for _, message := range messages {
		if message.Role != llm.RoleAssistant || len(message.ToolCalls) == 0 {
			continue
		}
		for _, call := range message.ToolCalls {
			callID := strings.TrimSpace(call.ID)
			if callID == "" {
				continue
			}
			out[callID] = true
		}
	}
	return out
}

func shouldIncludeReviewerMessage(message llm.Message) bool {
	if message.Role == llm.RoleDeveloper {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			return false
		}
		if _, ok := classifyMetaContextMessage(message); ok {
			return false
		}
		if message.MessageType == llm.MessageTypeErrorFeedback || message.MessageType == llm.MessageTypeInterruption {
			return false
		}
	}
	if strings.TrimSpace(message.Content) == "" && len(message.ToolCalls) == 0 {
		return false
	}
	return true
}

func formatReviewerTranscriptEntry(message llm.Message, toolOutputsByCallID map[string]string) string {
	b := strings.Builder{}
	b.WriteString(reviewerMessageLabel(message))
	content := reviewerTranscriptContent(message)
	if content != "" {
		b.WriteString("\n")
		if message.Role == llm.RoleTool {
			b.WriteString("Tool output:\n")
			b.WriteString(compactReviewerToolOutput(content))
		} else {
			b.WriteString(content)
		}
		b.WriteString("\n")
	}
	if len(message.ToolCalls) > 0 {
		b.WriteString("\nTool calls:\n")
		for i, call := range message.ToolCalls {
			b.WriteString(strconv.Itoa(i + 1))
			b.WriteString(". ")
			b.WriteString(strings.TrimSpace(call.Name))
			b.WriteString("\n")
			output := ""
			if callID := strings.TrimSpace(call.ID); callID != "" {
				output = strings.TrimSpace(toolOutputsByCallID[callID])
			}
			b.WriteString(formatReviewerToolCallPayload(call, output))
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func formatReviewerToolCallPayload(call llm.ToolCall, output string) string {
	sections := make([]string, 0, 2)
	if input := reviewerToolInputText(call); strings.TrimSpace(input) != "" {
		sections = append(sections, "Input:\n"+indentReviewerBlock(input))
	}
	if output = strings.TrimSpace(output); output != "" {
		sections = append(sections, "Output:\n"+indentReviewerBlock(output))
	}
	if len(sections) == 0 {
		return "{}"
	}
	return strings.Join(sections, "\n")
}

func reviewerToolInputText(call llm.ToolCall) string {
	if meta := decodeToolCallMeta(call); meta != nil {
		if text := reviewerToolPresentationText(meta); text != "" {
			return text
		}
	}
	return compactReviewerRawJSON(call.Input)
}

func reviewerToolPresentationText(meta *transcript.ToolCallMeta) string {
	if meta == nil {
		return ""
	}
	if meta.UsesAskQuestionRendering() {
		lines := make([]string, 0, len(meta.Suggestions)+2)
		if question := strings.TrimSpace(meta.Question); question != "" {
			lines = append(lines, "question: "+question)
		}
		for _, suggestion := range meta.Suggestions {
			trimmed := strings.TrimSpace(suggestion)
			if trimmed == "" {
				continue
			}
			lines = append(lines, "suggestion: "+trimmed)
		}
		if meta.RecommendedOptionIndex > 0 {
			lines = append(lines, fmt.Sprintf("recommended_option_index: %d", meta.RecommendedOptionIndex))
		}
		return strings.Join(lines, "\n")
	}
	lines := make([]string, 0, 4)
	if command := strings.TrimSpace(meta.Command); command != "" {
		lines = append(lines, command)
	} else if compact := strings.TrimSpace(meta.CompactText); compact != "" {
		lines = append(lines, compact)
	}
	if inlineMeta := strings.TrimSpace(meta.InlineMeta); inlineMeta != "" {
		lines = append(lines, "meta: "+inlineMeta)
	}
	if detail := strings.TrimSpace(meta.PatchDetail); detail != "" {
		lines = append(lines, detail)
	}
	if len(lines) == 0 {
		return strings.TrimSpace(meta.ToolName)
	}
	return strings.Join(lines, "\n")
}

func indentReviewerBlock(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i := range lines {
		lines[i] = "  " + lines[i]
	}
	return strings.Join(lines, "\n")
}

func reviewerTranscriptContent(message llm.Message) string {
	return strings.TrimSpace(message.Content)
}

func reviewerMessageLabel(message llm.Message) string {
	switch message.Role {
	case llm.RoleAssistant:
		return "Agent:"
	case llm.RoleUser:
		return "User:"
	case llm.RoleTool:
		return "Tool:"
	case llm.RoleDeveloper:
		return "Developer:"
	case llm.RoleSystem:
		return "System:"
	default:
		role := strings.TrimSpace(string(message.Role))
		if role == "" {
			role = "unknown"
		}
		return fmt.Sprintf("%s:", titleCaseASCII(role))
	}
}

func titleCaseASCII(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "Unknown"
	}
	runes := []rune(trimmed)
	if len(runes) == 1 {
		return strings.ToUpper(trimmed)
	}
	return strings.ToUpper(string(runes[0])) + string(runes[1:])
}

func compactReviewerToolOutput(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return "{}"
	}
	if !json.Valid([]byte(trimmed)) {
		return trimmed
	}
	var payload any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return trimmed
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return trimmed
	}
	return string(encoded)
}

func compactReviewerRawJSON(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "{}"
	}
	if !json.Valid([]byte(trimmed)) {
		return trimmed
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return trimmed
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return trimmed
	}
	return string(encoded)
}

func formatReviewerDeveloperInstruction(suggestions []string) string {
	b := strings.Builder{}
	b.WriteString("Supervisor agent gave you suggestions:\n")
	for idx, suggestion := range suggestions {
		b.WriteString(strconv.Itoa(idx + 1))
		b.WriteString(". ")
		b.WriteString(suggestion)
		b.WriteString("\n")
	}
	b.WriteString("\nIf no suggestions are applicable and you don't want to say anything to the user (not the supervisor!), respond with exactly ")
	b.WriteString(reviewerNoopToken)
	b.WriteString(" and no additional text. Otherwise, address the suggestions now. The supervisor can't hear you, your response will be to the user.")
	return b.String()
}

func reviewerStatusText(status ReviewerStatus, _ []string) string {
	statusText := ""
	switch strings.TrimSpace(status.Outcome) {
	case "failed":
		if strings.TrimSpace(status.Error) == "" {
			statusText = "Supervisor ran: failed to generate suggestions."
			break
		}
		statusText = fmt.Sprintf("Supervisor ran: failed to generate suggestions: %s", status.Error)
	case "no_suggestions":
		statusText = "Supervisor ran: no suggestions."
	case "followup_failed":
		if strings.TrimSpace(status.Error) == "" {
			statusText = fmt.Sprintf("Supervisor ran: %s, but follow-up failed.", reviewerSuggestionCountLabel(status.SuggestionsCount))
			break
		}
		statusText = fmt.Sprintf("Supervisor ran: %s, but follow-up failed: %s", reviewerSuggestionCountLabel(status.SuggestionsCount), status.Error)
	case "noop":
		statusText = fmt.Sprintf("Supervisor ran: %s, no changes applied.", reviewerSuggestionCountLabel(status.SuggestionsCount))
	case "applied":
		statusText = fmt.Sprintf("Supervisor ran: %s, applied.", reviewerSuggestionCountLabel(status.SuggestionsCount))
	default:
		statusText = "Supervisor ran."
	}
	if status.HasCacheHitPercentage {
		return statusText + "\n\n" + fmt.Sprintf("%d%% cache hit", status.CacheHitPercent)
	}
	return statusText
}

func reviewerSuggestionsText(suggestions []string) string {
	if len(suggestions) == 0 {
		return ""
	}
	b := strings.Builder{}
	b.WriteString("Supervisor suggested:\n")
	for idx, suggestion := range suggestions {
		b.WriteString(strconv.Itoa(idx + 1))
		b.WriteString(". ")
		b.WriteString(strings.TrimSpace(suggestion))
		if idx < len(suggestions)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func reviewerSuggestionCountLabel(count int) string {
	if count <= 1 {
		return "1 suggestion"
	}
	return fmt.Sprintf("%d suggestions", count)
}

func reviewerSessionID(sessionID string) string {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return ""
	}
	return trimmed + "-review"
}

func appendMissingReviewerMetaContext(messages []llm.Message, workspaceRoot string, model string, thinkingLevel string, headless bool, disabledSkills map[string]bool) ([]llm.Message, error) {
	metaMessages, transcript := splitMetaContextMessages(messages)
	builder := newMetaContextBuilder(workspaceRoot, model, thinkingLevel, disabledSkills, time.Now())
	metaResult, err := builder.Build(metaContextBuildOptions{
		ExistingMessages:          metaMessages,
		IncludeAgents:             true,
		IncludeSkills:             true,
		IncludeEnvironment:        true,
		IncludeHeadless:           headless,
		PermissiveAgentsReadError: true,
	})
	if err != nil {
		return nil, err
	}
	out := append(metaResult.OrderedMetaMessages(), transcript...)
	return out, nil
}
