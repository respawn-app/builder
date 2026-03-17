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
	registration, ok := lookupProviderVariantContract(normalizedID)
	if !ok {
		return nil, fmt.Errorf("no error reducer registered for provider_id %q; register a provider contract variant for this provider_id", normalizedID)
	}
	reducer := registration.Variant.NewErrorReducer(normalizedID)
	if reducer == nil {
		return nil, fmt.Errorf("error reducer factory returned nil for provider_id %q", normalizedID)
	}
	return reducer, nil
}
