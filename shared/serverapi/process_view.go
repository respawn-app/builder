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

type ProcessViewService interface {
	ListProcesses(ctx context.Context, req ProcessListRequest) (ProcessListResponse, error)
	GetProcess(ctx context.Context, req ProcessGetRequest) (ProcessGetResponse, error)
}

func (r ProcessGetRequest) Validate() error {
	if strings.TrimSpace(r.ProcessID) == "" {
		return errors.New("process_id is required")
	}
	return nil
}
