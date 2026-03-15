package runtime

import (
	"context"
	"strings"

	"builder/internal/llm"
)

var malformedAssistantArtifacts = []string{
	"#+#+#+#+",
	"#+#+#+#+#+",
	"assistant to=functions.shell",
	"assistant to=functions.patch",
	"assistant to=functions.multi_tool_use_parallel",
	"assistant to=multi_tool_use.parallel",
}

type defaultPhaseProtocol struct {
	engine *Engine
}

func (p *defaultPhaseProtocol) EnabledForModel(ctx context.Context) bool {
	e := p.engine
	e.mu.Lock()
	if e.phaseProtocolResolved {
		enabled := e.phaseProtocolEnabled
		e.mu.Unlock()
		return enabled
	}
	e.mu.Unlock()

	enabled := false
	if caps, err := e.providerCapabilities(ctx); err == nil {
		enabled = caps.SupportsResponsesAPI && caps.IsOpenAIFirstParty
	}

	e.mu.Lock()
	if !e.phaseProtocolResolved {
		e.phaseProtocolResolved = true
		e.phaseProtocolEnabled = enabled
	}
	result := e.phaseProtocolEnabled
	e.mu.Unlock()
	return result
}

func (p *defaultPhaseProtocol) Apply(ctx context.Context, resp llm.Response, assistant llm.Message, localToolCalls []llm.ToolCall, hostedToolExecutions []hostedToolExecution) phaseProtocolTurn {
	phaseProtocolEnabled := p.EnabledForModel(ctx)
	structuredPhaseProtocol := shouldTreatMissingAssistantPhaseAsCommentary(resp)
	hasExplicitAssistantPhase := strings.TrimSpace(string(assistant.Phase)) != ""
	enforcePhaseProtocol := phaseProtocolEnabled && (structuredPhaseProtocol || hasExplicitAssistantPhase)
	garbageAssistantContent := phaseProtocolEnabled && containsMalformedAssistantContent(assistant.Content)
	if garbageAssistantContent {
		assistant.Phase = llm.MessagePhaseCommentary
	}
	missingAssistantPhase := enforcePhaseProtocol && assistant.Phase == ""
	if missingAssistantPhase {
		assistant.Phase = llm.MessagePhaseCommentary
	}
	if len(localToolCalls) > 0 {
		assistant.ToolCalls = append([]llm.ToolCall(nil), localToolCalls...)
	}
	if len(hostedToolExecutions) > 0 {
		for _, hosted := range hostedToolExecutions {
			assistant.ToolCalls = append(assistant.ToolCalls, hosted.Call)
		}
	}
	if len(resp.ReasoningItems) > 0 && len(assistant.ReasoningItems) == 0 {
		assistant.ReasoningItems = append([]llm.ReasoningItem(nil), resp.ReasoningItems...)
	}
	finalAnswerIncludedToolCalls := false
	if phaseProtocolEnabled && assistant.Phase == llm.MessagePhaseFinal && (len(localToolCalls) > 0 || len(hostedToolExecutions) > 0) {
		finalAnswerIncludedToolCalls = true
		localToolCalls = nil
		hostedToolExecutions = nil
		assistant.ToolCalls = nil
	}

	return phaseProtocolTurn{
		Assistant:                    assistant,
		LocalToolCalls:               localToolCalls,
		HostedToolExecutions:         hostedToolExecutions,
		EnforcePhaseProtocol:         enforcePhaseProtocol,
		MissingAssistantPhase:        missingAssistantPhase,
		GarbageAssistantContent:      garbageAssistantContent,
		FinalAnswerIncludedToolCalls: finalAnswerIncludedToolCalls,
	}
}

func shouldTreatMissingAssistantPhaseAsCommentary(resp llm.Response) bool {
	for _, item := range resp.OutputItems {
		if item.Type == llm.ResponseItemTypeMessage && item.Role == llm.RoleAssistant {
			return true
		}
	}
	return false
}

func containsMalformedAssistantContent(content string) bool {
	if strings.TrimSpace(content) == "" {
		return false
	}
	lower := strings.ToLower(content)
	for _, artifact := range malformedAssistantArtifacts {
		if strings.Contains(lower, artifact) {
			return true
		}
	}
	return false
}
