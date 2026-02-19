package llm

import "testing"

func TestInferProviderCapabilities_OpenAIAndOAuth(t *testing.T) {
	openai := InferProviderCapabilities("https://api.openai.com/v1", false)
	if !openai.SupportsResponsesCompact || !openai.IsOpenAIFirstParty || !openai.SupportsNativeWebSearch {
		t.Fatalf("expected first-party openai compact support, got %+v", openai)
	}

	oauth := InferProviderCapabilities("https://chatgpt.com/backend-api/codex", true)
	if oauth.ProviderID != "chatgpt-codex" || !oauth.SupportsResponsesCompact || !oauth.IsOpenAIFirstParty || !oauth.SupportsNativeWebSearch {
		t.Fatalf("unexpected oauth capabilities: %+v", oauth)
	}
}

func TestInferProviderCapabilities_ThirdPartyDefaultsToLocalCompaction(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  string
	}{
		{name: "azure", url: "https://example.openai.azure.com/openai/v1"},
		{name: "lmstudio", url: "http://localhost:1234/v1"},
		{name: "ollama", url: "http://ollama.local/v1"},
		{name: "generic", url: "https://custom.provider/v1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caps := InferProviderCapabilities(tc.url, false)
			if caps.SupportsResponsesCompact {
				t.Fatalf("expected compact unsupported for %s, got %+v", tc.url, caps)
			}
			if caps.IsOpenAIFirstParty {
				t.Fatalf("expected third-party classification for %s, got %+v", tc.url, caps)
			}
			if caps.SupportsNativeWebSearch {
				t.Fatalf("expected native web search unsupported for %s, got %+v", tc.url, caps)
			}
		})
	}
}
