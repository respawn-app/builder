package llm

import "strings"

type ModelMetadata struct {
	ContextWindowTokens int
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
