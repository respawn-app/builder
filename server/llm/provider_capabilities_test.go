package llm

import (
	"errors"
	"testing"
)

func TestInferProviderCapabilities_UsesRegistryContracts(t *testing.T) {
	openai, err := InferProviderCapabilities("openai")
	if err != nil {
		t.Fatalf("infer openai capabilities: %v", err)
	}
	if !openai.SupportsResponsesCompact || !openai.IsOpenAIFirstParty || !openai.SupportsNativeWebSearch {
		t.Fatalf("expected first-party openai compact support, got %+v", openai)
	}
	if !openai.SupportsPromptCacheKey {
		t.Fatalf("expected openai prompt cache key support, got %+v", openai)
	}

	oauth, err := InferProviderCapabilities("chatgpt-codex")
	if err != nil {
		t.Fatalf("infer codex capabilities: %v", err)
	}
	if oauth.ProviderID != "chatgpt-codex" || !oauth.SupportsResponsesCompact || !oauth.IsOpenAIFirstParty || !oauth.SupportsNativeWebSearch {
		t.Fatalf("unexpected oauth capabilities: %+v", oauth)
	}
	if !oauth.SupportsPromptCacheKey {
		t.Fatalf("expected chatgpt-codex prompt cache key support, got %+v", oauth)
	}
}

func TestInferProviderCapabilities_UnknownProviderFailsExplicitly(t *testing.T) {
	_, err := InferProviderCapabilities("custom-provider")
	if !errors.Is(err, ErrUnsupportedProvider) {
		t.Fatalf("expected unsupported provider error, got %v", err)
	}
}

func TestResolveOpenAITransportProviderVariant_DefaultLoopbackAndRemoteCompatibleBaseURL(t *testing.T) {
	if got, err := resolveOpenAITransportProviderVariant("", openAIAuthMode{}); err != nil || got != "openai" {
		t.Fatalf("expected default base url to resolve openai variant, got variant=%q err=%v", got, err)
	}
	if got, err := resolveOpenAITransportProviderVariant("https://api.openai.com/v1/", openAIAuthMode{}); err != nil || got != "openai" {
		t.Fatalf("expected normalized default base url to resolve openai variant, got variant=%q err=%v", got, err)
	}
	if got, err := resolveOpenAITransportProviderVariant("http://127.0.0.1:8080/v1", openAIAuthMode{}); err != nil || got != "openai" {
		t.Fatalf("expected loopback base url to resolve openai variant, got variant=%q err=%v", got, err)
	}
	if got, err := resolveOpenAITransportProviderVariant("https://example.openai.azure.com/openai/v1", openAIAuthMode{}); err != nil || got != "openai-compatible" {
		t.Fatalf("expected remote compatible base url to resolve openai-compatible variant, got variant=%q err=%v", got, err)
	}
	if got, err := resolveOpenAITransportProviderVariant("https://ignored.example/v1", openAIAuthMode{IsOAuth: true}); err != nil || got != "chatgpt-codex" {
		t.Fatalf("expected oauth mode to resolve chatgpt-codex variant, got variant=%q err=%v", got, err)
	}
}

func TestKnownNonFirstPartyProviderContractsRemainLocalCompactionOnly(t *testing.T) {
	for _, providerID := range []string{"anthropic", "openai-compatible"} {
		caps, err := InferProviderCapabilities(providerID)
		if err != nil {
			t.Fatalf("infer %s capabilities: %v", providerID, err)
		}
		if caps.SupportsResponsesCompact {
			t.Fatalf("expected compact unsupported for %s, got %+v", providerID, caps)
		}
		if caps.IsOpenAIFirstParty {
			t.Fatalf("expected third-party classification for %s, got %+v", providerID, caps)
		}
		if caps.SupportsPromptCacheKey {
			t.Fatalf("expected prompt cache key unsupported for %s, got %+v", providerID, caps)
		}
		if caps.SupportsNativeWebSearch {
			t.Fatalf("expected native web search unsupported for %s, got %+v", providerID, caps)
		}
	}
}

func TestSupportsFastModeProvider(t *testing.T) {
	if !SupportsFastModeProvider(ProviderCapabilities{ProviderID: "chatgpt-codex", SupportsResponsesAPI: true, IsOpenAIFirstParty: true}) {
		t.Fatal("expected chatgpt-codex to support fast mode")
	}
	if !SupportsFastModeProvider(ProviderCapabilities{ProviderID: "openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: true}) {
		t.Fatal("expected openai provider to support fast mode")
	}
	if SupportsFastModeProvider(ProviderCapabilities{ProviderID: "azure-openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: false}) {
		t.Fatal("did not expect non-first-party provider to support fast mode")
	}
}

func TestSupportsPromptCacheKeyProvider(t *testing.T) {
	if !SupportsPromptCacheKeyProvider(ProviderCapabilities{ProviderID: "openai-compatible", SupportsResponsesAPI: true, SupportsPromptCacheKey: true}) {
		t.Fatal("expected explicit prompt cache capability to enable support")
	}
	if SupportsPromptCacheKeyProvider(ProviderCapabilities{ProviderID: "openai-compatible", SupportsResponsesAPI: true, SupportsPromptCacheKey: false}) {
		t.Fatal("did not expect prompt cache support without explicit capability")
	}
}
