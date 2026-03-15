package llm

import (
	"fmt"
	"net/http"
	"strings"
)

type openAIRequestErrorMapper struct {
	providerID string
}

func newOpenAIRequestErrorMapper(providerID string) openAIRequestErrorMapper {
	return openAIRequestErrorMapper{providerID: providerID}
}

func mapOpenAIRequestError(providerID string, err error, rawResp *http.Response, prefix string) error {
	return newOpenAIRequestErrorMapper(providerID).Map(err, rawResp, prefix)
}

func (m openAIRequestErrorMapper) Map(err error, rawResp *http.Response, prefix string) error {
	reducer, reducerErr := providerErrorReducerForID(m.providerID)
	if reducerErr != nil {
		statusCode := 0
		if rawResp != nil {
			statusCode = rawResp.StatusCode
			if rawResp.Body != nil {
				rawResp.Body.Close()
				rawResp.Body = nil
			}
		}
		return fmt.Errorf("%s: %w", prefix, NewProviderContractError(m.providerID, statusCode, reducerErr))
	}
	reducedErr, ok := reducer.Reduce(err, rawResp)
	if ok && reducedErr != nil {
		return fmt.Errorf("%s: %w", prefix, reducedErr)
	}
	if err == nil {
		return fmt.Errorf("%s: unknown error", prefix)
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

func truncateError(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return "<empty error body>"
	}
	return trimmed
}
