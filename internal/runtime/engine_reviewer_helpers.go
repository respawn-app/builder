package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"builder/internal/llm"
)

type reviewerSuggestionsResult struct {
	Suggestions           []string
	CacheHitPercent       int
	HasCacheHitPercentage bool
}

type reviewerRequestConfig struct {
	Model          string
	ThinkingLevel  string
	MaxSuggestions int
}

func (e *Engine) runReviewerSuggestions(ctx context.Context, reviewerClient llm.Client) (reviewerSuggestionsResult, error) {
	e.ensureOrchestrationCollaborators()
	return e.reviewerFlow.RunSuggestions(ctx, reviewerClient)
}

func parseReviewerSuggestionsObject(content string, maxSuggestions int) []string {
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
	out := payload.Suggestions

	if maxSuggestions <= 0 {
		maxSuggestions = 5
	}
	normalized := make([]string, 0, min(maxSuggestions, len(out)))
	seen := map[string]bool{}
	for _, suggestion := range out {
		text := strings.TrimSpace(suggestion)
		if text == "" {
			continue
		}
		if seen[text] {
			continue
		}
		seen[text] = true
		normalized = append(normalized, text)
		if len(normalized) >= maxSuggestions {
			break
		}
	}
	return normalized
}

func buildReviewerRequestMessages(messages []llm.Message, workspaceRoot string, model string, thinkingLevel string) []llm.Message {
	metaMessages, transcriptSource := splitReviewerMetaMessages(messages)
	metaMessages = appendMissingReviewerMetaContext(metaMessages, workspaceRoot, model, thinkingLevel)
	out := make([]llm.Message, 0, len(metaMessages)+2+len(transcriptSource))
	out = append(out, metaMessages...)
	out = append(out, llm.Message{Role: llm.RoleDeveloper, Content: reviewerMetaBoundaryMessage})
	out = append(out, buildReviewerTranscriptMessages(transcriptSource)...)
	return out
}

func splitReviewerMetaMessages(messages []llm.Message) ([]llm.Message, []llm.Message) {
	meta := make([]llm.Message, 0, 3)
	transcript := make([]llm.Message, 0, len(messages))
	seenEnvironment := false
	seenAgentContent := map[string]bool{}
	seenSkillsContent := map[string]bool{}
	for _, message := range messages {
		if message.Role == llm.RoleDeveloper && message.MessageType == llm.MessageTypeAgentsMD {
			normalized := strings.TrimSpace(message.Content)
			if normalized == "" || seenAgentContent[normalized] {
				continue
			}
			seenAgentContent[normalized] = true
			meta = append(meta, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeAgentsMD, Content: message.Content})
			continue
		}
		if message.Role == llm.RoleDeveloper && message.MessageType == llm.MessageTypeSkills {
			normalized := strings.TrimSpace(message.Content)
			if normalized == "" || seenSkillsContent[normalized] {
				continue
			}
			seenSkillsContent[normalized] = true
			meta = append(meta, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeSkills, Content: message.Content})
			continue
		}
		if message.Role == llm.RoleDeveloper && message.MessageType == llm.MessageTypeEnvironment {
			if seenEnvironment {
				continue
			}
			seenEnvironment = true
			meta = append(meta, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeEnvironment, Content: message.Content})
			continue
		}
		transcript = append(transcript, message)
	}
	return meta, transcript
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
	if isShortAssistantCommentaryPreamble(message) && len(message.ToolCalls) == 0 {
		return false
	}
	if message.Role == llm.RoleDeveloper {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			return false
		}
		if message.MessageType == llm.MessageTypeEnvironment || message.MessageType == llm.MessageTypeSkills {
			return false
		}
		if message.MessageType == llm.MessageTypeErrorFeedback || message.MessageType == llm.MessageTypeInterruption {
			return false
		}
		// Backward compatibility for persisted transcripts created before message_type.
		if strings.Contains(content, environmentInjectedHeader) {
			return false
		}
		if strings.Contains(content, skillsInjectedHeader+"\n") {
			return false
		}
		if content == commentaryWithoutToolCallsWarning || content == finalWithToolCallsIgnoredWarning || content == missingAssistantPhaseWarning || content == garbageAssistantContentWarning || content == interruptMessage {
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
			b.WriteString(formatReviewerToolCallPayload(call.Input, output))
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func formatReviewerToolCallPayload(input json.RawMessage, output string) string {
	payload := map[string]any{
		"input": reviewerJSONValueFromRaw(input),
	}
	if strings.TrimSpace(output) != "" {
		payload["output"] = reviewerJSONValueFromString(output)
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data)
}

func reviewerJSONValueFromRaw(raw json.RawMessage) any {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return map[string]any{}
	}
	if !json.Valid([]byte(trimmed)) {
		return trimmed
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return trimmed
	}
	return decoded
}

func reviewerJSONValueFromString(content string) any {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	if !json.Valid([]byte(trimmed)) {
		return trimmed
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return trimmed
	}
	return decoded
}

func reviewerTranscriptContent(message llm.Message) string {
	if isShortAssistantCommentaryPreamble(message) {
		return ""
	}
	return strings.TrimSpace(message.Content)
}

func isShortAssistantCommentaryPreamble(message llm.Message) bool {
	if message.Role != llm.RoleAssistant || message.Phase != llm.MessagePhaseCommentary {
		return false
	}
	content := strings.TrimSpace(message.Content)
	if content == "" {
		return false
	}
	if strings.Contains(content, "\n") {
		return false
	}
	return utf8.RuneCountInString(content) <= reviewerShortCommentaryMaxRunes
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

func prettyReviewerJSON(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "{}"
	}
	if !json.Valid([]byte(trimmed)) {
		return trimmed
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, []byte(trimmed), "", "  "); err != nil {
		return trimmed
	}
	return formatted.String()
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

func reviewerStatusText(status ReviewerStatus, suggestions []string) string {
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
	if len(suggestions) == 0 {
		if status.HasCacheHitPercentage {
			return statusText + "\n\n" + fmt.Sprintf("%d%% cache hit", status.CacheHitPercent)
		}
		return statusText
	}
	b := strings.Builder{}
	b.WriteString(statusText)
	b.WriteString("\n\n")
	b.WriteString("Supervisor suggested:\n")
	for idx, suggestion := range suggestions {
		b.WriteString(strconv.Itoa(idx + 1))
		b.WriteString(". ")
		b.WriteString(strings.TrimSpace(suggestion))
		if idx < len(suggestions)-1 {
			b.WriteString("\n")
		}
	}
	if status.HasCacheHitPercentage {
		b.WriteString("\n\n")
		b.WriteString(fmt.Sprintf("%d%% cache hit", status.CacheHitPercent))
	}
	return b.String()
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

func appendMissingReviewerMetaContext(messages []llm.Message, workspaceRoot string, model string, thinkingLevel string) []llm.Message {
	haveEnvironment := false
	haveAgents := false
	haveSkills := false
	for _, msg := range messages {
		if msg.Role != llm.RoleDeveloper {
			continue
		}
		if msg.MessageType == llm.MessageTypeAgentsMD {
			haveAgents = true
		}
		if msg.MessageType == llm.MessageTypeSkills {
			haveSkills = true
		}
		if msg.MessageType == llm.MessageTypeEnvironment {
			haveEnvironment = true
		}
	}
	if haveAgents && haveSkills && haveEnvironment {
		return messages
	}
	out := append([]llm.Message(nil), messages...)
	paths, err := agentsInjectionPaths(workspaceRoot)
	if err != nil {
		paths = nil
	}
	agentsToInsert := make([]llm.Message, 0, len(paths))
	if !haveAgents {
		for _, path := range paths {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				if errors.Is(readErr, os.ErrNotExist) {
					continue
				}
				continue
			}
			injected := fmt.Sprintf("%s\nsource: %s\n\n```%s\n%s\n```", agentsInjectedHeader, path, agentsInjectedFenceLabel, string(data))
			agentsToInsert = append(agentsToInsert, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeAgentsMD, Content: injected})
		}
		if len(agentsToInsert) > 0 {
			out = append(agentsToInsert, out...)
		}
	}

	if !haveSkills {
		skills, found, skillsErr := skillsContextMessage(workspaceRoot)
		if skillsErr == nil && found {
			skillsMessage := llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeSkills, Content: skills}
			insertAt := firstMetaBoundaryIndex(out)
			lastAgentsIndex := -1
			firstEnvironmentIndex := -1
			for idx, msg := range out {
				if msg.Role != llm.RoleDeveloper {
					continue
				}
				if msg.MessageType == llm.MessageTypeAgentsMD {
					lastAgentsIndex = idx
					continue
				}
				if msg.MessageType == llm.MessageTypeEnvironment && firstEnvironmentIndex < 0 {
					firstEnvironmentIndex = idx
				}
			}
			if lastAgentsIndex >= 0 {
				insertAt = lastAgentsIndex + 1
			} else if firstEnvironmentIndex >= 0 {
				insertAt = firstEnvironmentIndex
			}
			out = insertMessageAt(out, insertAt, skillsMessage)
		}
	}

	if !haveEnvironment {
		environmentMessage := llm.Message{
			Role:        llm.RoleDeveloper,
			MessageType: llm.MessageTypeEnvironment,
			Content:     environmentContextMessage(workspaceRoot, model, thinkingLevel, time.Now()),
		}
		insertAt := firstMetaBoundaryIndex(out)
		out = insertMessageAt(out, insertAt, environmentMessage)
	}

	if len(out) == len(messages) {
		return messages
	}
	return out
}

func firstMetaBoundaryIndex(messages []llm.Message) int {
	for idx, msg := range messages {
		if msg.Role != llm.RoleDeveloper {
			return idx
		}
		if msg.MessageType != llm.MessageTypeAgentsMD && msg.MessageType != llm.MessageTypeSkills && msg.MessageType != llm.MessageTypeEnvironment {
			return idx
		}
	}
	return len(messages)
}

func insertMessageAt(messages []llm.Message, index int, message llm.Message) []llm.Message {
	if index < 0 {
		index = 0
	}
	if index > len(messages) {
		index = len(messages)
	}
	messages = append(messages, llm.Message{})
	copy(messages[index+1:], messages[index:])
	messages[index] = message
	return messages
}
