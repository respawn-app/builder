package llm

import "strings"

type ModelMetadata struct {
	ContextWindowTokens int
}

func ModelDisplayLabel(model string, thinkingLevel string) string {
	modelLabel := strings.TrimSpace(model)
	if modelLabel == "" {
		modelLabel = "gpt-5"
	}
	level := strings.TrimSpace(thinkingLevel)
	if level == "" {
		return modelLabel
	}
	if !SupportsReasoningEffortModel(modelLabel) {
		return modelLabel
	}
	return modelLabel + " " + level
}

// SupportsReasoningEffortModel reports whether reasoning effort is enabled for
// the given model identifier. Unknown non-empty models default to reasoning
// support so new model rollouts do not silently disable thinking.
func SupportsReasoningEffortModel(model string) bool {
	normalized := strings.TrimSpace(model)
	if normalized == "" {
		return false
	}
	contract, ok := LookupModelCapabilityContract(normalized)
	if !ok {
		return true
	}
	return contract.SupportsReasoningEffort
}

// SupportsReasoningSummaryModel reports whether the Responses API
// reasoning.summary field should be sent for the given model identifier.
// Unknown models default to false because unsupported summary fields can
// hard-fail requests.
func SupportsReasoningSummaryModel(model string) bool {
	contract, ok := LookupModelCapabilityContract(model)
	return ok && contract.SupportsReasoningSummary
}

// SupportsVisionInputsModel reports whether the explicit model capability
// contract allows multimodal image/file inputs for the Responses API.
func SupportsVisionInputsModel(model string) bool {
	contract, ok := LookupModelCapabilityContract(model)
	return ok && contract.SupportsVisionInputs
}

// SupportsMultiToolUseParallelModel reports whether the model benefits from the
// Codex-style parallel tool wrapper compatibility schema.
func SupportsMultiToolUseParallelModel(model string) bool {
	contract, ok := LookupModelCapabilityContract(model)
	return ok && contract.SupportsMultiToolUseParallel
}

// SupportsVerbosityModel reports whether Responses API text verbosity should be
// sent for the given model identifier. Unknown models default to false because
// unsupported verbosity fields can hard-fail requests.
func SupportsVerbosityModel(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if normalized == "" {
		return false
	}
	contract, ok := LookupModelCapabilityContract(normalized)
	if ok {
		return contract.SupportsVerbosity
	}
	return strings.HasPrefix(normalized, "gpt-5")
}

func LookupModelMetadata(model string) (ModelMetadata, bool) {
	contract, ok := LookupModelCapabilityContract(model)
	if !ok {
		return ModelMetadata{}, false
	}
	return ModelMetadata{ContextWindowTokens: contract.ContextWindowTokens}, contract.ContextWindowTokens > 0
}
