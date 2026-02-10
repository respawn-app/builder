package llm

import (
	"net/url"
	"strings"
)

func InferProviderCapabilities(baseURL string, oauth bool) ProviderCapabilities {
	if oauth {
		return ProviderCapabilities{
			ProviderID:                    "chatgpt-codex",
			SupportsResponsesAPI:          true,
			SupportsResponsesCompact:      true,
			SupportsReasoningEncrypted:    true,
			SupportsServerSideContextEdit: true,
			IsOpenAIFirstParty:            true,
		}
	}

	host := hostForBaseURL(baseURL)
	switch {
	case host == "api.openai.com":
		return ProviderCapabilities{
			ProviderID:                    "openai",
			SupportsResponsesAPI:          true,
			SupportsResponsesCompact:      true,
			SupportsReasoningEncrypted:    true,
			SupportsServerSideContextEdit: true,
			IsOpenAIFirstParty:            true,
		}
	case strings.Contains(host, "chatgpt.com"):
		return ProviderCapabilities{
			ProviderID:                    "chatgpt-codex",
			SupportsResponsesAPI:          true,
			SupportsResponsesCompact:      true,
			SupportsReasoningEncrypted:    true,
			SupportsServerSideContextEdit: true,
			IsOpenAIFirstParty:            true,
		}
	case strings.Contains(host, "openai.azure.com"), strings.Contains(host, ".azure.com"):
		return ProviderCapabilities{
			ProviderID:                    "azure-openai",
			SupportsResponsesAPI:          true,
			SupportsResponsesCompact:      false,
			SupportsReasoningEncrypted:    false,
			SupportsServerSideContextEdit: false,
			IsOpenAIFirstParty:            false,
		}
	case strings.Contains(host, "lmstudio"):
		return ProviderCapabilities{
			ProviderID:                    "lmstudio",
			SupportsResponsesAPI:          true,
			SupportsResponsesCompact:      false,
			SupportsReasoningEncrypted:    false,
			SupportsServerSideContextEdit: false,
			IsOpenAIFirstParty:            false,
		}
	case strings.Contains(host, "ollama"), strings.HasPrefix(host, "localhost"), strings.HasPrefix(host, "127."):
		return ProviderCapabilities{
			ProviderID:                    "ollama",
			SupportsResponsesAPI:          true,
			SupportsResponsesCompact:      false,
			SupportsReasoningEncrypted:    false,
			SupportsServerSideContextEdit: false,
			IsOpenAIFirstParty:            false,
		}
	default:
		return ProviderCapabilities{
			ProviderID:                    "openai-compatible",
			SupportsResponsesAPI:          true,
			SupportsResponsesCompact:      false,
			SupportsReasoningEncrypted:    false,
			SupportsServerSideContextEdit: false,
			IsOpenAIFirstParty:            false,
		}
	}
}

func hostForBaseURL(baseURL string) string {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return "api.openai.com"
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(trimmed))
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return strings.ToLower(strings.TrimSpace(trimmed))
	}
	return host
}
