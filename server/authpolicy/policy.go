package authpolicy

import (
	"strings"

	"builder/server/llm"
	"builder/shared/config"
)

func RequiresStartupAuth(settings config.Settings) bool {
	if strings.TrimSpace(settings.OpenAIBaseURL) != "" {
		return false
	}
	if provider := strings.ToLower(strings.TrimSpace(settings.ProviderOverride)); provider != "" {
		return provider == string(llm.ProviderOpenAI)
	}
	provider, err := llm.InferProviderFromModel(settings.Model)
	if err != nil {
		return false
	}
	return provider == llm.ProviderOpenAI
}
