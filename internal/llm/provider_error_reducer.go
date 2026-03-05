package llm

import (
	"fmt"
	"net/http"
	"strings"
)

type ProviderErrorReducer interface {
	Reduce(err error, rawResp *http.Response) (*ProviderAPIError, bool)
}

func providerErrorReducerForID(providerID string) (ProviderErrorReducer, error) {
	normalizedID := strings.TrimSpace(providerID)
	if factory, ok := globalProviderRegistry.reducerFactories[normalizedID]; ok {
		reducer := factory(normalizedID)
		if reducer == nil {
			return nil, fmt.Errorf("error reducer factory returned nil for provider_id %q", normalizedID)
		}
		return reducer, nil
	}
	return nil, fmt.Errorf("no error reducer registered for provider_id %q; register a reducer for this provider_id or use a base URL/provider that is registered", normalizedID)
}
