package serverapi

import (
	"context"
	"errors"
	"strings"

	"builder/shared/clientui"
)

type SessionMainViewRequest struct {
	SessionID string
}

type SessionMainViewResponse struct {
	MainView clientui.RuntimeMainView
}

type RunGetRequest struct {
	SessionID string
	RunID     string
}

type RunGetResponse struct {
	Run *clientui.RunView
}

type SessionViewService interface {
	GetSessionMainView(ctx context.Context, req SessionMainViewRequest) (SessionMainViewResponse, error)
	GetRun(ctx context.Context, req RunGetRequest) (RunGetResponse, error)
}

func (r SessionMainViewRequest) Validate() error {
	if strings.TrimSpace(r.SessionID) == "" {
		return errors.New("session_id is required")
	}
	return nil
}

func (r RunGetRequest) Validate() error {
	if strings.TrimSpace(r.SessionID) == "" {
		return errors.New("session_id is required")
	}
	if strings.TrimSpace(r.RunID) == "" {
		return errors.New("run_id is required")
	}
	return nil
}
