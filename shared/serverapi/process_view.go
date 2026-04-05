package serverapi

import (
	"context"
	"errors"
	"strings"

	"builder/shared/clientui"
)

type ProcessListRequest struct {
	OwnerSessionID string
	OwnerRunID     string
}

type ProcessListResponse struct {
	Processes []clientui.BackgroundProcess
}

type ProcessGetRequest struct {
	ProcessID string
}

type ProcessGetResponse struct {
	Process *clientui.BackgroundProcess
}

type ProcessKillRequest struct {
	ClientRequestID string
	ProcessID       string
}

type ProcessKillResponse struct{}

type ProcessInlineOutputRequest struct {
	ProcessID string
	MaxChars  int
}

type ProcessInlineOutputResponse struct {
	Output  string
	LogPath string
}

type ProcessViewService interface {
	ListProcesses(ctx context.Context, req ProcessListRequest) (ProcessListResponse, error)
	GetProcess(ctx context.Context, req ProcessGetRequest) (ProcessGetResponse, error)
}

type ProcessControlService interface {
	KillProcess(ctx context.Context, req ProcessKillRequest) (ProcessKillResponse, error)
	GetInlineOutput(ctx context.Context, req ProcessInlineOutputRequest) (ProcessInlineOutputResponse, error)
}

func (r ProcessGetRequest) Validate() error {
	if strings.TrimSpace(r.ProcessID) == "" {
		return errors.New("process_id is required")
	}
	return nil
}

func (r ProcessKillRequest) Validate() error {
	if strings.TrimSpace(r.ClientRequestID) == "" {
		return errors.New("client_request_id is required")
	}
	if strings.TrimSpace(r.ProcessID) == "" {
		return errors.New("process_id is required")
	}
	return nil
}

func (r ProcessInlineOutputRequest) Validate() error {
	if strings.TrimSpace(r.ProcessID) == "" {
		return errors.New("process_id is required")
	}
	return nil
}
