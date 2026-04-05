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
	WorkspaceRoot   string              `json:"workspace_root"`
	Source          config.SourceReport `json:"source"`
}

type SessionRuntimeReleaseRequest struct {
	ClientRequestID string `json:"client_request_id"`
	SessionID       string `json:"session_id"`
}

type SessionRuntimeService interface {
	ActivateSessionRuntime(ctx context.Context, req SessionRuntimeActivateRequest) error
	ReleaseSessionRuntime(ctx context.Context, req SessionRuntimeReleaseRequest) error
}

func (r SessionRuntimeActivateRequest) Validate() error {
	if strings.TrimSpace(r.ClientRequestID) == "" {
		return errors.New("client_request_id is required")
	}
	if err := validateScopedSessionID(r.SessionID); err != nil {
		return err
	}
	if strings.TrimSpace(r.WorkspaceRoot) == "" {
		return errors.New("workspace_root is required")
	}
	return nil
}

func (r SessionRuntimeReleaseRequest) Validate() error {
	if strings.TrimSpace(r.ClientRequestID) == "" {
		return errors.New("client_request_id is required")
	}
	return validateScopedSessionID(r.SessionID)
}
