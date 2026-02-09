package llm

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var ErrUnsupportedProvider = errors.New("unsupported llm provider")

type Provider string

const (
	ProviderOpenAI    Provider = "openai"
	ProviderAnthropic Provider = "anthropic"
)

type ProviderClientOptions struct {
	Provider Provider
	Model    string

	Auth                AuthHeaderProvider
	HTTPClient          *http.Client
	OpenAIBaseURL       string
	ContextWindowTokens int
}

func NewProviderClient(opts ProviderClientOptions) (Client, error) {
	provider := opts.Provider
	if provider == "" {
		provider = InferProviderFromModel(opts.Model)
	}
	if opts.ContextWindowTokens <= 0 {
		if meta, ok := LookupModelMetadata(opts.Model); ok && meta.ContextWindowTokens > 0 {
			opts.ContextWindowTokens = meta.ContextWindowTokens
		}
	}
	switch provider {
	case ProviderOpenAI:
		if opts.Auth == nil {
			return nil, fmt.Errorf("openai auth provider is required")
		}
		transport := NewHTTPTransport(opts.Auth)
		if opts.HTTPClient != nil {
			transport.Client = opts.HTTPClient
		}
		if v := strings.TrimSpace(opts.OpenAIBaseURL); v != "" {
			transport.BaseURL = v
		}
		if opts.ContextWindowTokens > 0 {
			transport.ContextWindowTokens = opts.ContextWindowTokens
		}
		return NewOpenAIClient(transport), nil
	case ProviderAnthropic:
		return nil, fmt.Errorf("%w: %s (not implemented)", ErrUnsupportedProvider, provider)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedProvider, provider)
	}
}

func InferProviderFromModel(model string) Provider {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(m, "claude"):
		return ProviderAnthropic
	default:
		return ProviderOpenAI
	}
}
