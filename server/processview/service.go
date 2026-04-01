package processview

import (
	"context"
	"fmt"
	"strings"

	shelltool "builder/server/tools/shell"
	"builder/shared/clientui"
	"builder/shared/serverapi"
)

type ProcessSource interface {
	List() []shelltool.Snapshot
	Snapshot(id string) (shelltool.Snapshot, error)
}

type Service struct {
	processes ProcessSource
}

func NewService(processes ProcessSource) *Service {
	return &Service{processes: processes}
}

func (s *Service) ListProcesses(_ context.Context, req serverapi.ProcessListRequest) (serverapi.ProcessListResponse, error) {
	if s == nil || s.processes == nil {
		return serverapi.ProcessListResponse{}, fmt.Errorf("process source is required")
	}
	ownerSessionID := strings.TrimSpace(req.OwnerSessionID)
	ownerRunID := strings.TrimSpace(req.OwnerRunID)
	snapshots := s.processes.List()
	processes := make([]clientui.BackgroundProcess, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if ownerSessionID != "" && strings.TrimSpace(snapshot.OwnerSessionID) != ownerSessionID {
			continue
		}
		if ownerRunID != "" && strings.TrimSpace(snapshot.OwnerRunID) != ownerRunID {
			continue
		}
		processes = append(processes, ProcessFromSnapshot(snapshot))
	}
	return serverapi.ProcessListResponse{Processes: processes}, nil
}

func (s *Service) GetProcess(_ context.Context, req serverapi.ProcessGetRequest) (serverapi.ProcessGetResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.ProcessGetResponse{}, err
	}
	if s == nil || s.processes == nil {
		return serverapi.ProcessGetResponse{}, fmt.Errorf("process source is required")
	}
	snapshot, err := s.processes.Snapshot(strings.TrimSpace(req.ProcessID))
	if err != nil {
		return serverapi.ProcessGetResponse{}, err
	}
	process := ProcessFromSnapshot(snapshot)
	return serverapi.ProcessGetResponse{Process: &process}, nil
}
