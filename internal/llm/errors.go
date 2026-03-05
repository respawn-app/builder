package llm

import (
	"errors"
	"fmt"
)

type APIStatusError struct {
	StatusCode int
	Body       string
}

func (e *APIStatusError) Error() string {
	if e == nil {
		return "openai status error"
	}
	return fmt.Sprintf("openai status %d: %s", e.StatusCode, e.Body)
}

type UnifiedErrorCode string

const (
	UnifiedErrorCodeUnknown               UnifiedErrorCode = "unknown"
	UnifiedErrorCodeAuthentication        UnifiedErrorCode = "authentication"
	UnifiedErrorCodeContextLengthOverflow UnifiedErrorCode = "context_length_overflow"
	UnifiedErrorCodeProviderContract      UnifiedErrorCode = "provider_contract_error"
)

type ProviderAPIError struct {
	ProviderID    string
	StatusCode    int
	Code          UnifiedErrorCode
	ProviderCode  string
	ProviderType  string
	ProviderParam string
	Message       string
	Raw           string
	Err           error
}

func NewProviderContractError(providerID string, statusCode int, cause error) *ProviderAPIError {
	message := "provider contract error"
	if cause != nil {
		message = cause.Error()
	}
	return &ProviderAPIError{
		ProviderID: providerID,
		StatusCode: statusCode,
		Code:       UnifiedErrorCodeProviderContract,
		Message:    message,
		Raw:        message,
		Err:        cause,
	}
}

func (e *ProviderAPIError) Error() string {
	if e == nil {
		return "provider api error"
	}
	return fmt.Sprintf("%s status %d [%s/%s]: %s", e.ProviderID, e.StatusCode, e.Code, e.ProviderCode, e.Message)
}

func (e *ProviderAPIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type AuthError struct {
	Err error
}

func (e *AuthError) Error() string {
	if e == nil || e.Err == nil {
		return "authentication error"
	}
	return e.Err.Error()
}

func (e *AuthError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func IsAuthenticationError(err error) bool {
	if err == nil {
		return false
	}
	var authErr *AuthError
	if errors.As(err, &authErr) {
		return true
	}
	var providerErr *ProviderAPIError
	if errors.As(err, &providerErr) {
		if providerErr.Code == UnifiedErrorCodeAuthentication {
			return true
		}
		switch providerErr.StatusCode {
		case 401, 403:
			return true
		}
	}
	var apiErr *APIStatusError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 401, 403:
			return true
		}
	}
	return false
}

func IsNonRetriableModelError(err error) bool {
	if err == nil {
		return false
	}
	if IsAuthenticationError(err) {
		return true
	}
	var providerErr *ProviderAPIError
	if errors.As(err, &providerErr) {
		if providerErr.Code == UnifiedErrorCodeProviderContract {
			return true
		}
		switch providerErr.StatusCode {
		case 400, 401, 403, 404:
			return true
		}
	}
	var apiErr *APIStatusError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 400, 401, 403, 404:
			return true
		}
	}
	return false
}

func IsContextLengthOverflowError(err error) bool {
	if err == nil {
		return false
	}
	var providerErr *ProviderAPIError
	if !errors.As(err, &providerErr) {
		return false
	}
	return providerErr.Code == UnifiedErrorCodeContextLengthOverflow
}
