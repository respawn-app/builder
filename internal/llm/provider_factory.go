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
	Store               bool
	ContextWindowTokens int
}

type ProviderClientFactory func(opts ProviderClientOptions) (Client, error)

type ProviderErrorReducerFactory func(providerID string) ProviderErrorReducer

type ProviderContract struct {
	Provider         Provider
	NewClient        ProviderClientFactory
	ErrorReducerByID map[string]ProviderErrorReducerFactory
}

type providerRegistry struct {
	clientFactories  map[Provider]ProviderClientFactory
	reducerFactories map[string]ProviderErrorReducerFactory
}

var globalProviderRegistry = mustBuildProviderRegistry(providerContracts())

func providerContracts() []ProviderContract {
	return []ProviderContract{
		{
			Provider:  ProviderOpenAI,
			NewClient: newOpenAIProviderClient,
			ErrorReducerByID: map[string]ProviderErrorReducerFactory{
				"openai":        newOpenAICompatibleErrorReducer,
				"chatgpt-codex": newOpenAICompatibleErrorReducer,
			},
		},
		{
			Provider:  ProviderAnthropic,
			NewClient: newUnsupportedProviderClientFactory(ProviderAnthropic),
			ErrorReducerByID: map[string]ProviderErrorReducerFactory{
				"anthropic": newOpaqueProviderErrorReducer,
			},
		},
	}
}

func mustBuildProviderRegistry(contracts []ProviderContract) providerRegistry {

	clientFactories := make(map[Provider]ProviderClientFactory, len(contracts))
	reducerFactories := make(map[string]ProviderErrorReducerFactory)

	for _, contract := range contracts {
		if contract.Provider == "" {
			panic("provider contract missing provider key")
		}
		if contract.NewClient == nil {
			panic(fmt.Sprintf("provider %q missing client factory", contract.Provider))
		}
		if len(contract.ErrorReducerByID) == 0 {
			panic(fmt.Sprintf("provider %q missing error reducer registrations", contract.Provider))
		}
		if _, exists := clientFactories[contract.Provider]; exists {
			panic(fmt.Sprintf("duplicate provider contract for %q", contract.Provider))
		}
		clientFactories[contract.Provider] = contract.NewClient

		for providerID, reducerFactory := range contract.ErrorReducerByID {
			normalizedID := strings.TrimSpace(providerID)
			if normalizedID == "" {
				panic(fmt.Sprintf("provider %q has empty error reducer provider_id", contract.Provider))
			}
			if reducerFactory == nil {
				panic(fmt.Sprintf("provider %q missing reducer factory for provider_id %q", contract.Provider, normalizedID))
			}
			if _, exists := reducerFactories[normalizedID]; exists {
				panic(fmt.Sprintf("duplicate error reducer registration for provider_id %q", normalizedID))
			}
			reducerFactories[normalizedID] = reducerFactory
		}
	}
	return providerRegistry{
		clientFactories:  clientFactories,
		reducerFactories: reducerFactories,
	}
}

func newUnsupportedProviderClientFactory(provider Provider) ProviderClientFactory {
	return func(_ ProviderClientOptions) (Client, error) {
		return nil, fmt.Errorf("%w: %s (not implemented)", ErrUnsupportedProvider, provider)
	}
}

func newOpenAIProviderClient(opts ProviderClientOptions) (Client, error) {
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
	transport.ProviderMetadata = ResolveOpenAIProviderMetadata(opts.OpenAIBaseURL)
	if opts.ContextWindowTokens > 0 {
		transport.ContextWindowTokens = opts.ContextWindowTokens
	}
	transport.Store = opts.Store
	return NewOpenAIClient(transport), nil
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
	factory, ok := globalProviderRegistry.clientFactories[provider]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedProvider, provider)
	}
	return factory(opts)
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
