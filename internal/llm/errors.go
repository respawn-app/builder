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
	var apiErr *APIStatusError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 400, 401, 403, 404:
			return true
		}
	}
	return false
}
