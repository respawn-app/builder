package llm

import "testing"

func TestInferProviderCapabilities_UsesRegistryContracts(t *testing.T) {
	openai := InferProviderCapabilities("openai")
	if !openai.SupportsResponsesCompact || !openai.IsOpenAIFirstParty || !openai.SupportsNativeWebSearch {
		t.Fatalf("expected first-party openai compact support, got %+v", openai)
	}

	oauth := InferProviderCapabilities("chatgpt-codex")
	if oauth.ProviderID != "chatgpt-codex" || !oauth.SupportsResponsesCompact || !oauth.IsOpenAIFirstParty || !oauth.SupportsNativeWebSearch {
		t.Fatalf("unexpected oauth capabilities: %+v", oauth)
	}
}

func TestInferProviderCapabilities_UnknownProviderFallsBackToOpenAICompatible(t *testing.T) {
	caps := InferProviderCapabilities("custom-provider")
	if caps.ProviderID != "openai-compatible" {
		t.Fatalf("expected openai-compatible fallback, got %+v", caps)
	}
	if caps.SupportsResponsesCompact || caps.IsOpenAIFirstParty || caps.SupportsNativeWebSearch {
		t.Fatalf("expected conservative fallback capabilities, got %+v", caps)
	}
}

func TestResolveOpenAIProviderMetadata_DefaultAndCustomBaseURL(t *testing.T) {
	if got := ResolveOpenAIProviderMetadata(""); got.CapabilityProviderID != "openai" {
		t.Fatalf("expected default base url to resolve openai metadata, got %+v", got)
	}
	if got := ResolveOpenAIProviderMetadata("https://api.openai.com/v1/"); got.CapabilityProviderID != "openai" {
		t.Fatalf("expected normalized default base url to resolve openai metadata, got %+v", got)
	}
	if got := ResolveOpenAIProviderMetadata("https://example.openai.azure.com/openai/v1"); got.CapabilityProviderID != "openai-compatible" {
		t.Fatalf("expected custom base url to stay conservative, got %+v", got)
	}
}

func TestKnownNonFirstPartyProviderContractsRemainLocalCompactionOnly(t *testing.T) {
	for _, tc := range []struct {
		name       string
		providerID string
	}{
		{name: "generic", providerID: "openai-compatible"},
		{name: "anthropic", providerID: "anthropic"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caps := InferProviderCapabilities(tc.providerID)
			if caps.SupportsResponsesCompact {
				t.Fatalf("expected compact unsupported for %s, got %+v", tc.providerID, caps)
			}
			if caps.IsOpenAIFirstParty {
				t.Fatalf("expected third-party classification for %s, got %+v", tc.providerID, caps)
			}
			if caps.SupportsNativeWebSearch {
				t.Fatalf("expected native web search unsupported for %s, got %+v", tc.providerID, caps)
			}
		})
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
