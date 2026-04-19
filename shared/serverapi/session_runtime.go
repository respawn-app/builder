package serverapi

import (
	"context"
	"errors"
	"strings"

	"builder/shared/config"
)

type SessionRuntimeActivateRequest struct {
	ClientRequestID string              `json:"client_request_id"`
	SessionID       string              `json:"session_id"`
	ActiveSettings  config.Settings     `json:"active_settings"`
	EnabledToolIDs  []string            `json:"enabled_tool_ids"`
	Source          config.SourceReport `json:"source"`
}

type SessionRuntimeActivateResponse struct {
	LeaseID string `json:"lease_id"`
}

type SessionRuntimeReleaseRequest struct {
	ClientRequestID string `json:"client_request_id"`
	SessionID       string `json:"session_id"`
	LeaseID         string `json:"lease_id"`
}

type SessionRuntimeReleaseResponse struct{}

type SessionRuntimeService interface {
	ActivateSessionRuntime(ctx context.Context, req SessionRuntimeActivateRequest) (SessionRuntimeActivateResponse, error)
	ReleaseSessionRuntime(ctx context.Context, req SessionRuntimeReleaseRequest) (SessionRuntimeReleaseResponse, error)
}

func (r SessionRuntimeActivateRequest) Validate() error {
	if strings.TrimSpace(r.ClientRequestID) == "" {
		return errors.New("client_request_id is required")
	}
	if err := validateScopedSessionID(r.SessionID); err != nil {
		return err
	}
	return nil
}

func (r SessionRuntimeReleaseRequest) Validate() error {
	if strings.TrimSpace(r.ClientRequestID) == "" {
		return errors.New("client_request_id is required")
	}
	if err := validateScopedSessionID(r.SessionID); err != nil {
		return err
	}
	if strings.TrimSpace(r.LeaseID) == "" {
		return errors.New("lease_id is required")
	}
	return nil
}
