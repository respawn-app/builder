package llm

import (
	"strings"

	"builder/internal/config"
	"builder/internal/session"
)

// capability_registry.go is the single source of truth for built-in provider
// and model capability contracts. Additions here should be deliberate and
// reviewable because request shaping depends on these contracts.

type ModelCapabilityContract struct {
	Model                   string
	ContextWindowTokens     int
	SupportsReasoningEffort bool
	SupportsVisionInputs    bool
}

type OpenAIProviderMetadata struct {
	CapabilityProviderID string
}

var knownModelCapabilityContracts = map[string]ModelCapabilityContract{
	"gpt-5": {
		Model:                   "gpt-5",
		SupportsReasoningEffort: true,
		SupportsVisionInputs:    true,
	},
	"gpt-5.3-codex": {
		Model:                   "gpt-5.3-codex",
		ContextWindowTokens:     400_000,
		SupportsReasoningEffort: true,
		SupportsVisionInputs:    true,
	},
	"gpt-4.1": {
		Model:                   "gpt-4.1",
		SupportsReasoningEffort: true,
		SupportsVisionInputs:    true,
	},
	"gpt-4o": {
		Model:                   "gpt-4o",
		SupportsReasoningEffort: true,
		SupportsVisionInputs:    true,
	},
	"gpt-4o-mini": {
		Model:                   "gpt-4o-mini",
		SupportsReasoningEffort: true,
		SupportsVisionInputs:    true,
	},
	"o1": {
		Model:                   "o1",
		SupportsReasoningEffort: true,
		SupportsVisionInputs:    true,
	},
	"o3": {
		Model:                   "o3",
		SupportsReasoningEffort: true,
		SupportsVisionInputs:    true,
	},
	"o3-mini": {
		Model:                   "o3-mini",
		SupportsReasoningEffort: true,
		SupportsVisionInputs:    true,
	},
	"o4": {
		Model:                   "o4",
		SupportsReasoningEffort: true,
		SupportsVisionInputs:    true,
	},
	"o4-mini": {
		Model:                   "o4-mini",
		SupportsReasoningEffort: true,
		SupportsVisionInputs:    true,
	},
}

var knownProviderCapabilityContracts = map[string]ProviderCapabilities{
	"openai": {
		ProviderID:                    "openai",
		SupportsResponsesAPI:          true,
		SupportsResponsesCompact:      true,
		SupportsNativeWebSearch:       true,
		SupportsReasoningEncrypted:    true,
		SupportsServerSideContextEdit: true,
		IsOpenAIFirstParty:            true,
	},
	"chatgpt-codex": {
		ProviderID:                    "chatgpt-codex",
		SupportsResponsesAPI:          true,
		SupportsResponsesCompact:      true,
		SupportsNativeWebSearch:       true,
		SupportsReasoningEncrypted:    true,
		SupportsServerSideContextEdit: true,
		IsOpenAIFirstParty:            true,
	},
	"openai-compatible": {
		ProviderID:                    "openai-compatible",
		SupportsResponsesAPI:          true,
		SupportsResponsesCompact:      false,
		SupportsNativeWebSearch:       false,
		SupportsReasoningEncrypted:    false,
		SupportsServerSideContextEdit: false,
		IsOpenAIFirstParty:            false,
	},
	"anthropic": {
		ProviderID:                    "anthropic",
		SupportsResponsesAPI:          false,
		SupportsResponsesCompact:      false,
		SupportsNativeWebSearch:       false,
		SupportsReasoningEncrypted:    false,
		SupportsServerSideContextEdit: false,
		IsOpenAIFirstParty:            false,
	},
}

func LookupModelCapabilityContract(model string) (ModelCapabilityContract, bool) {
	key := normalizeCapabilityRegistryKey(model)
	if key == "" {
		return ModelCapabilityContract{}, false
	}
	contract, ok := knownModelCapabilityContracts[key]
	return contract, ok
}

func LookupProviderCapabilityContract(providerID string) (ProviderCapabilities, bool) {
	key := normalizeCapabilityRegistryKey(providerID)
	if key == "" {
		return ProviderCapabilities{}, false
	}
	contract, ok := knownProviderCapabilityContracts[key]
	return contract, ok
}

func ResolveOpenAIProviderMetadata(baseURL string) OpenAIProviderMetadata {
	if normalizeOpenAIBaseURL(baseURL) == normalizeOpenAIBaseURL(defaultOpenAIBaseURL) {
		return OpenAIProviderMetadata{CapabilityProviderID: "openai"}
	}
	return OpenAIProviderMetadata{CapabilityProviderID: "openai-compatible"}
}

func normalizeOpenAIBaseURL(baseURL string) string {
	trimmed := strings.TrimSpace(baseURL)
	trimmed = strings.TrimSuffix(trimmed, "/")
	if trimmed == "" {
		return strings.TrimSuffix(defaultOpenAIBaseURL, "/")
	}
	return trimmed
}

func normalizeCapabilityRegistryKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func LockedModelCapabilitiesForModel(model string) session.LockedModelCapabilities {
	contract, ok := LookupModelCapabilityContract(model)
	if !ok {
		return session.LockedModelCapabilities{}
	}
	return session.LockedModelCapabilities{
		SupportsReasoningEffort: contract.SupportsReasoningEffort,
		SupportsVisionInputs:    contract.SupportsVisionInputs,
	}
}

func LockedModelCapabilitiesForConfig(model string, override config.ModelCapabilitiesOverride) session.LockedModelCapabilities {
	if override.SupportsReasoningEffort || override.SupportsVisionInputs {
		return session.LockedModelCapabilities{
			SupportsReasoningEffort: override.SupportsReasoningEffort,
			SupportsVisionInputs:    override.SupportsVisionInputs,
		}
	}
	return LockedModelCapabilitiesForModel(model)
}

func LockedProviderCapabilitiesFromContract(contract ProviderCapabilities) session.LockedProviderCapabilities {
	return session.LockedProviderCapabilities{
		ProviderID:                    strings.TrimSpace(contract.ProviderID),
		SupportsResponsesAPI:          contract.SupportsResponsesAPI,
		SupportsResponsesCompact:      contract.SupportsResponsesCompact,
		SupportsNativeWebSearch:       contract.SupportsNativeWebSearch,
		SupportsReasoningEncrypted:    contract.SupportsReasoningEncrypted,
		SupportsServerSideContextEdit: contract.SupportsServerSideContextEdit,
		IsOpenAIFirstParty:            contract.IsOpenAIFirstParty,
	}
}

func ProviderCapabilitiesFromOverride(override config.ProviderCapabilitiesOverride) (ProviderCapabilities, bool) {
	providerID := strings.TrimSpace(override.ProviderID)
	if providerID == "" {
		return ProviderCapabilities{}, false
	}
	return ProviderCapabilities{
		ProviderID:                    providerID,
		SupportsResponsesAPI:          override.SupportsResponsesAPI,
		SupportsResponsesCompact:      override.SupportsResponsesCompact,
		SupportsNativeWebSearch:       override.SupportsNativeWebSearch,
		SupportsReasoningEncrypted:    override.SupportsReasoningEncrypted,
		SupportsServerSideContextEdit: override.SupportsServerSideContextEdit,
		IsOpenAIFirstParty:            override.IsOpenAIFirstParty,
	}, true
}

func ProviderCapabilitiesFromLocked(locked *session.LockedContract) (ProviderCapabilities, bool) {
	if locked == nil {
		return ProviderCapabilities{}, false
	}
	providerID := strings.TrimSpace(locked.ProviderContract.ProviderID)
	if providerID == "" {
		return ProviderCapabilities{}, false
	}
	return ProviderCapabilities{
		ProviderID:                    providerID,
		SupportsResponsesAPI:          locked.ProviderContract.SupportsResponsesAPI,
		SupportsResponsesCompact:      locked.ProviderContract.SupportsResponsesCompact,
		SupportsNativeWebSearch:       locked.ProviderContract.SupportsNativeWebSearch,
		SupportsReasoningEncrypted:    locked.ProviderContract.SupportsReasoningEncrypted,
		SupportsServerSideContextEdit: locked.ProviderContract.SupportsServerSideContextEdit,
		IsOpenAIFirstParty:            locked.ProviderContract.IsOpenAIFirstParty,
	}, true
}

func LockedContractSupportsReasoningEffort(locked *session.LockedContract, model string) bool {
	if hasLockedCapabilitySnapshot(locked) {
		return locked.ModelCapabilities.SupportsReasoningEffort
	}
	return SupportsReasoningEffortModel(model)
}

func LockedContractSupportsVisionInputs(locked *session.LockedContract, model string) bool {
	if hasLockedCapabilitySnapshot(locked) {
		return locked.ModelCapabilities.SupportsVisionInputs
	}
	return SupportsVisionInputsModel(model)
}

func hasLockedCapabilitySnapshot(locked *session.LockedContract) bool {
	if locked == nil {
		return false
	}
	if strings.TrimSpace(locked.ProviderContract.ProviderID) != "" {
		return true
	}
	return locked.ModelCapabilities.SupportsReasoningEffort || locked.ModelCapabilities.SupportsVisionInputs
}
