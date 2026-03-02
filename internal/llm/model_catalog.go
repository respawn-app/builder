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

// SupportsReasoningEffortModel reports whether reasoning effort is applicable
// for the given model identifier. The heuristic is shared between request
// payload construction and UI labeling to keep behavior consistent.
func SupportsReasoningEffortModel(model string) bool {
	normalizedModel := strings.ToLower(strings.TrimSpace(model))
	if normalizedModel == "" {
		return false
	}
	return strings.HasPrefix(normalizedModel, "gpt-") || strings.HasPrefix(normalizedModel, "o")
}

var defaultModelMetadata = map[string]ModelMetadata{
	"gpt-5.3-codex": {
		ContextWindowTokens: 400_000,
	},
}

func LookupModelMetadata(model string) (ModelMetadata, bool) {
	key := strings.ToLower(strings.TrimSpace(model))
	if key == "" {
		return ModelMetadata{}, false
	}
	meta, ok := defaultModelMetadata[key]
	return meta, ok
}
