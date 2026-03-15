package llm

import "strings"

func InferProviderCapabilities(providerID string) ProviderCapabilities {
	contract, ok := LookupProviderCapabilityContract(providerID)
	if !ok {
		fallback, fallbackOK := LookupProviderCapabilityContract("openai-compatible")
		if fallbackOK {
			return fallback
		}
		return ProviderCapabilities{ProviderID: strings.TrimSpace(providerID)}
	}
	return contract
}

func SupportsFastModeProvider(caps ProviderCapabilities) bool {
	return caps.SupportsResponsesAPI && caps.IsOpenAIFirstParty
}
