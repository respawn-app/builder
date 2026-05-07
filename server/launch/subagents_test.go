package launch

import (
	"testing"

	"builder/shared/config"
)

func TestApplyReviewerInheritanceRecomputesDefaultBaseURLWhenReviewerProviderExplicit(t *testing.T) {
	settings := config.Settings{
		ProviderOverride: "openai",
		OpenAIBaseURL:    "http://subagent.local/v1",
		Reviewer: config.ReviewerSettings{
			ProviderOverride: "openai",
			OpenAIBaseURL:    "http://parent.local/v1",
		},
	}
	sources := reviewerInheritanceDefaultSources()
	sources["openai_base_url"] = "subagent"
	sources["reviewer.provider_override"] = "subagent"

	applyReviewerInheritance(&settings, sources)

	if settings.Reviewer.ProviderOverride != "openai" {
		t.Fatalf("reviewer provider override = %q, want openai", settings.Reviewer.ProviderOverride)
	}
	if settings.Reviewer.OpenAIBaseURL != "http://subagent.local/v1" {
		t.Fatalf("reviewer base URL = %q, want subagent main base URL", settings.Reviewer.OpenAIBaseURL)
	}
}

func TestApplyReviewerInheritanceDoesNotCopyMainProviderCapabilitiesForExplicitReviewerEndpoint(t *testing.T) {
	settings := config.Settings{
		ProviderCapabilities: config.ProviderCapabilitiesOverride{
			ProviderID:               "main-provider",
			SupportsResponsesAPI:     true,
			SupportsPromptCacheKey:   true,
			IsOpenAIFirstParty:       true,
			SupportsNativeWebSearch:  true,
			SupportsResponsesCompact: true,
		},
		Reviewer: config.ReviewerSettings{
			ProviderOverride: "openai",
			OpenAIBaseURL:    "http://reviewer.local/v1",
		},
	}
	sources := reviewerInheritanceDefaultSources()
	sources["reviewer.provider_override"] = "subagent"
	sources["reviewer.openai_base_url"] = "subagent"

	applyReviewerInheritance(&settings, sources)

	if settings.Reviewer.ProviderCapabilities != (config.ProviderCapabilitiesOverride{}) {
		t.Fatalf("expected reviewer provider capabilities to stay unset for explicit endpoint, got %+v", settings.Reviewer.ProviderCapabilities)
	}
}

func reviewerInheritanceDefaultSources() map[string]string {
	sources := map[string]string{
		"reviewer.model":                                                    "default",
		"reviewer.thinking_level":                                           "default",
		"reviewer.model_verbosity":                                          "default",
		"reviewer.provider_override":                                        "default",
		"reviewer.openai_base_url":                                          "default",
		"reviewer.model_context_window":                                     "default",
		"reviewer.auth":                                                     "default",
		"reviewer.model_capabilities.supports_reasoning_effort":             "default",
		"reviewer.model_capabilities.supports_vision_inputs":                "default",
		"reviewer.provider_capabilities.provider_id":                        "default",
		"reviewer.provider_capabilities.supports_responses_api":             "default",
		"reviewer.provider_capabilities.supports_responses_compact":         "default",
		"reviewer.provider_capabilities.supports_request_input_token_count": "default",
		"reviewer.provider_capabilities.supports_prompt_cache_key":          "default",
		"reviewer.provider_capabilities.supports_native_web_search":         "default",
		"reviewer.provider_capabilities.supports_reasoning_encrypted":       "default",
		"reviewer.provider_capabilities.supports_server_side_context_edit":  "default",
		"reviewer.provider_capabilities.is_openai_first_party":              "default",
	}
	return sources
}
