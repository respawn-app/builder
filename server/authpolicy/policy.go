package authpolicy

import (
	"net/url"
	"strings"

	"builder/server/llm"
	"builder/shared/config"
)

func RequiresStartupAuth(settings config.Settings) bool {
	if baseURL := strings.TrimSpace(settings.OpenAIBaseURL); baseURL != "" {
		if explicitBaseURLRequiresStartupAuth(baseURL) {
			return true
		}
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

func explicitBaseURLRequiresStartupAuth(baseURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(parsed.Hostname()), "api.openai.com") {
		return false
	}
	path := strings.TrimSuffix(strings.TrimSpace(parsed.EscapedPath()), "/")
	return path == "/v1"
}
