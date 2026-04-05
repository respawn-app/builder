package processview

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	shelltool "builder/server/tools/shell"
	"builder/shared/clientui"
	"builder/shared/serverapi"
)

type ProcessSource interface {
	List() []shelltool.Snapshot
	Snapshot(id string) (shelltool.Snapshot, error)
	Kill(id string) error
	InlineOutput(id string, maxChars int) (string, string, error)
}

type Service struct {
	processes ProcessSource
	killMu    sync.Mutex
	kills     map[string]*killRequestEntry
}

func NewService(processes ProcessSource) *Service {
	return &Service{processes: processes, kills: map[string]*killRequestEntry{}}
}

type killRequestFingerprint struct {
	processID string
}

type killRequestEntry struct {
	fingerprint killRequestFingerprint
	response    serverapi.ProcessKillResponse
	err         error
	done        bool
	cacheable   bool
	completedAt time.Time
	ready       chan struct{}
}

const killProcessDedupeRetention = 10 * time.Minute

var killProcessDedupeNow = time.Now

func (s *Service) sweepExpiredKillEntriesLocked(now time.Time) {
	for key, entry := range s.kills {
		if entry == nil || !entry.done || entry.completedAt.IsZero() {
			continue
		}
		if now.Sub(entry.completedAt) >= killProcessDedupeRetention {
			delete(s.kills, key)
		}
	}
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

func (s *Service) KillProcess(ctx context.Context, req serverapi.ProcessKillRequest) (serverapi.ProcessKillResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.ProcessKillResponse{}, err
	}
	if s == nil || s.processes == nil {
		return serverapi.ProcessKillResponse{}, fmt.Errorf("process source is required")
	}
	for {
		key := strings.TrimSpace(req.ClientRequestID)
		fp := killRequestFingerprint{processID: strings.TrimSpace(req.ProcessID)}

		s.killMu.Lock()
		s.sweepExpiredKillEntriesLocked(killProcessDedupeNow())
		entry, exists := s.kills[key]
		if exists {
			if entry.fingerprint != fp {
				s.killMu.Unlock()
				return serverapi.ProcessKillResponse{}, fmt.Errorf("client_request_id %q reused with different payload", req.ClientRequestID)
			}
			if entry.done {
				if entry.cacheable {
					response, err := entry.response, entry.err
					s.killMu.Unlock()
					return response, err
				}
				delete(s.kills, key)
				s.killMu.Unlock()
				continue
			}
			ready := entry.ready
			s.killMu.Unlock()
			select {
			case <-ready:
				continue
			case <-ctx.Done():
				return serverapi.ProcessKillResponse{}, ctx.Err()
			}
		}

		entry = &killRequestEntry{fingerprint: fp, ready: make(chan struct{})}
		s.kills[key] = entry
		s.killMu.Unlock()

		err := s.processes.Kill(fp.processID)
		response := serverapi.ProcessKillResponse{}
		cacheable := !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)

		s.killMu.Lock()
		entry.response = response
		entry.err = err
		entry.done = true
		entry.cacheable = cacheable
		entry.completedAt = killProcessDedupeNow()
		close(entry.ready)
		if !cacheable {
			delete(s.kills, key)
		}
		s.killMu.Unlock()
		if err != nil {
			return serverapi.ProcessKillResponse{}, err
		}
		return response, nil
	}
}

func (s *Service) GetInlineOutput(_ context.Context, req serverapi.ProcessInlineOutputRequest) (serverapi.ProcessInlineOutputResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.ProcessInlineOutputResponse{}, err
	}
	if s == nil || s.processes == nil {
		return serverapi.ProcessInlineOutputResponse{}, fmt.Errorf("process source is required")
	}
	output, logPath, err := s.processes.InlineOutput(strings.TrimSpace(req.ProcessID), req.MaxChars)
	if err != nil {
		return serverapi.ProcessInlineOutputResponse{}, err
	}
	return serverapi.ProcessInlineOutputResponse{Output: output, LogPath: logPath}, nil
}
