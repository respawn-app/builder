package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	openai "github.com/openai/openai-go/v3"
)

type openAICompatibleErrorReducer struct {
	providerID string
}

type opaqueProviderErrorReducer struct {
	providerID string
}

func newOpenAICompatibleErrorReducer(providerID string) ProviderErrorReducer {
	return openAICompatibleErrorReducer{providerID: strings.TrimSpace(providerID)}
}

func newOpaqueProviderErrorReducer(providerID string) ProviderErrorReducer {
	return opaqueProviderErrorReducer{providerID: strings.TrimSpace(providerID)}
}

func (r openAICompatibleErrorReducer) Reduce(err error, rawResp *http.Response) (*ProviderAPIError, bool) {
	if reduced, ok := r.reduceFromSDK(err); ok {
		return reduced, true
	}
	if reduced, ok := r.reduceFromResponse(rawResp); ok {
		return reduced, true
	}
	return nil, false
}

func (r opaqueProviderErrorReducer) Reduce(err error, rawResp *http.Response) (*ProviderAPIError, bool) {
	if reduced, ok := r.reduceFromResponse(rawResp); ok {
		return reduced, true
	}
	if err != nil {
		message := strings.TrimSpace(err.Error())
		return &ProviderAPIError{
			ProviderID: r.providerID,
			StatusCode: 0,
			Code:       UnifiedErrorCodeUnknown,
			Message:    message,
			Raw:        message,
			Err:        err,
		}, true
	}
	return nil, false
}

func (r openAICompatibleErrorReducer) reduceFromSDK(err error) (*ProviderAPIError, bool) {
	if err == nil {
		return nil, false
	}
	var sdkErr *openai.Error
	if !errors.As(err, &sdkErr) {
		return nil, false
	}
	return mapOpenAIProviderErrorContract(
		r.providerID,
		sdkErr.StatusCode,
		sdkErr.Code,
		sdkErr.Type,
		sdkErr.Param,
		sdkErr.Message,
		sdkErr.RawJSON(),
		err,
	), true
}

func (r openAICompatibleErrorReducer) reduceFromResponse(rawResp *http.Response) (*ProviderAPIError, bool) {
	if rawResp == nil || rawResp.StatusCode < 300 {
		return nil, false
	}
	if rawResp.Body == nil {
		return &ProviderAPIError{
			ProviderID: r.providerID,
			StatusCode: rawResp.StatusCode,
			Code:       UnifiedErrorCodeUnknown,
			Message:    http.StatusText(rawResp.StatusCode),
			Raw:        "<empty error body>",
		}, true
	}
	body, _ := io.ReadAll(rawResp.Body)
	rawResp.Body.Close()
	rawResp.Body = io.NopCloser(bytes.NewReader(body))
	raw := truncateError(body)

	var payload struct {
		Error openai.Error `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		return mapOpenAIProviderErrorContract(
			r.providerID,
			rawResp.StatusCode,
			payload.Error.Code,
			payload.Error.Type,
			payload.Error.Param,
			payload.Error.Message,
			raw,
			nil,
		), true
	}

	return &ProviderAPIError{
		ProviderID: r.providerID,
		StatusCode: rawResp.StatusCode,
		Code:       UnifiedErrorCodeUnknown,
		Message:    raw,
		Raw:        raw,
	}, true
}

func (r opaqueProviderErrorReducer) reduceFromResponse(rawResp *http.Response) (*ProviderAPIError, bool) {
	if rawResp == nil || rawResp.StatusCode < 300 {
		return nil, false
	}
	if rawResp.Body == nil {
		return &ProviderAPIError{
			ProviderID: r.providerID,
			StatusCode: rawResp.StatusCode,
			Code:       classifyOpaqueUnifiedErrorCode(rawResp.StatusCode),
			Message:    http.StatusText(rawResp.StatusCode),
			Raw:        "<empty error body>",
		}, true
	}
	body, _ := io.ReadAll(rawResp.Body)
	rawResp.Body.Close()
	rawResp.Body = io.NopCloser(bytes.NewReader(body))
	raw := truncateError(body)
	return &ProviderAPIError{
		ProviderID: r.providerID,
		StatusCode: rawResp.StatusCode,
		Code:       classifyOpaqueUnifiedErrorCode(rawResp.StatusCode),
		Message:    raw,
		Raw:        raw,
	}, true
}

func mapOpenAIProviderErrorContract(
	providerID string,
	statusCode int,
	providerCode string,
	providerType string,
	providerParam string,
	message string,
	raw string,
	cause error,
) *ProviderAPIError {
	return &ProviderAPIError{
		ProviderID:    providerID,
		StatusCode:    statusCode,
		Code:          classifyOpenAIUnifiedErrorCode(statusCode, providerCode),
		ProviderCode:  providerCode,
		ProviderType:  providerType,
		ProviderParam: providerParam,
		Message:       message,
		Raw:           raw,
		Err:           cause,
	}
}

func classifyOpenAIUnifiedErrorCode(statusCode int, providerCode string) UnifiedErrorCode {
	if statusCode == 401 || statusCode == 403 {
		return UnifiedErrorCodeAuthentication
	}
	switch strings.ToLower(strings.TrimSpace(providerCode)) {
	case "context_length_exceeded",
		"context_window_exceeded",
		"max_context_length_exceeded",
		"token_limit_exceeded",
		"prompt_too_long",
		"input_too_long":
		return UnifiedErrorCodeContextLengthOverflow
	default:
		return UnifiedErrorCodeUnknown
	}
}

func classifyOpaqueUnifiedErrorCode(statusCode int) UnifiedErrorCode {
	if statusCode == 401 || statusCode == 403 {
		return UnifiedErrorCodeAuthentication
	}
	return UnifiedErrorCodeUnknown
}
