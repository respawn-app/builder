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

// SupportsReasoningEffortModel reports whether reasoning effort is enabled by
// the explicit model capability contract for the given model identifier.
func SupportsReasoningEffortModel(model string) bool {
	contract, ok := LookupModelCapabilityContract(model)
	return ok && contract.SupportsReasoningEffort
}

// SupportsVisionInputsModel reports whether the explicit model capability
// contract allows multimodal image/file inputs for the Responses API.
func SupportsVisionInputsModel(model string) bool {
	contract, ok := LookupModelCapabilityContract(model)
	return ok && contract.SupportsVisionInputs
}

func LookupModelMetadata(model string) (ModelMetadata, bool) {
	contract, ok := LookupModelCapabilityContract(model)
	if !ok {
		return ModelMetadata{}, false
	}
	return ModelMetadata{ContextWindowTokens: contract.ContextWindowTokens}, contract.ContextWindowTokens > 0
}
