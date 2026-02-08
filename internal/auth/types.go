package auth

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrAuthNotConfigured   = errors.New("auth is not configured")
	ErrInvalidAuthMethod   = errors.New("invalid auth method")
	ErrSwitchRequiresIdle  = errors.New("auth method switch requires idle session")
	ErrOAuthRefreshFailed  = errors.New("oauth token refresh failed")
	ErrInvalidAuthScope    = errors.New("invalid auth scope")
	ErrMissingOAuthFactory = errors.New("oauth token source factory is required")
)

type Scope string

const (
	ScopeGlobal Scope = "global"
)

type MethodType string

const (
	MethodNone   MethodType = ""
	MethodAPIKey MethodType = "api_key"
	MethodOAuth  MethodType = "oauth"
)

type State struct {
	Scope     Scope     `json:"scope"`
	Method    Method    `json:"method"`
	UpdatedAt time.Time `json:"updated_at"`
}

func EmptyState() State {
	return State{Scope: ScopeGlobal}
}

func (s State) IsConfigured() bool {
	return s.Method.Type != MethodNone
}

func (s State) Validate() error {
	if s.Scope == "" {
		return fmt.Errorf("%w: empty", ErrInvalidAuthScope)
	}
	if s.Scope != ScopeGlobal {
		return fmt.Errorf("%w: %q", ErrInvalidAuthScope, s.Scope)
	}
	if s.Method.Type == MethodNone {
		return nil
	}
	if err := s.Method.Validate(); err != nil {
		return err
	}
	return nil
}

type Method struct {
	Type   MethodType    `json:"type"`
	APIKey *APIKeyMethod `json:"api_key,omitempty"`
	OAuth  *OAuthMethod  `json:"oauth,omitempty"`
}

type APIKeyMethod struct {
	Key string `json:"key"`
}

type OAuthMethod struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	Expiry       time.Time `json:"expiry"`
}

func (m Method) Validate() error {
	switch m.Type {
	case MethodAPIKey:
		if m.APIKey == nil {
			return fmt.Errorf("%w: api key payload is missing", ErrInvalidAuthMethod)
		}
		if strings.TrimSpace(m.APIKey.Key) == "" {
			return fmt.Errorf("%w: api key is empty", ErrInvalidAuthMethod)
		}
		if m.OAuth != nil {
			return fmt.Errorf("%w: unexpected oauth payload for api key", ErrInvalidAuthMethod)
		}
	case MethodOAuth:
		if m.OAuth == nil {
			return fmt.Errorf("%w: oauth payload is missing", ErrInvalidAuthMethod)
		}
		if strings.TrimSpace(m.OAuth.AccessToken) == "" {
			return fmt.Errorf("%w: oauth access token is empty", ErrInvalidAuthMethod)
		}
		if m.APIKey != nil {
			return fmt.Errorf("%w: unexpected api key payload for oauth", ErrInvalidAuthMethod)
		}
	case MethodNone:
		if m.APIKey != nil || m.OAuth != nil {
			return fmt.Errorf("%w: credentials present for unset type", ErrInvalidAuthMethod)
		}
	default:
		return fmt.Errorf("%w: unknown type %q", ErrInvalidAuthMethod, m.Type)
	}
	return nil
}

func (m Method) AuthHeaderValue() (string, error) {
	if err := m.Validate(); err != nil {
		return "", err
	}
	var token string
	switch m.Type {
	case MethodAPIKey:
		token = m.APIKey.Key
	case MethodOAuth:
		token = m.OAuth.AccessToken
	default:
		return "", ErrAuthNotConfigured
	}
	return "Bearer " + token, nil
}
