package sessionlaunch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"builder/server/launch"
	"builder/server/session"
	"builder/shared/config"
	"builder/shared/serverapi"
)

type sessionStoreRegistrar interface {
	RegisterStore(store *session.Store)
}

type Service struct {
	planner launch.Planner
	stores  sessionStoreRegistrar
}

type deduplicatingService struct {
	scopeID string
	inner   serverapi.SessionLaunchService
}

type dedupeFingerprint struct {
	mode              string
	selectedSessionID string
	forceNewSession   bool
	parentSessionID   string
}

type dedupeEntry struct {
	fingerprint dedupeFingerprint
	response    serverapi.SessionPlanResponse
	err         error
	done        bool
	cacheable   bool
	completedAt time.Time
	ready       chan struct{}
}

const dedupeRetention = 10 * time.Minute

var dedupeNow = time.Now

var dedupeRegistry = struct {
	mu      sync.Mutex
	entries map[string]*dedupeEntry
}{entries: map[string]*dedupeEntry{}}

func NewService(planner launch.Planner, stores sessionStoreRegistrar) *Service {
	return &Service{planner: planner, stores: stores}
}

func ScopeID(cfg config.App, containerDir string) string {
	parts := make([]string, 0, 3)
	if part := normalizedScopePart(cfg.PersistenceRoot); part != "" {
		parts = append(parts, part)
	}
	if part := normalizedScopePart(containerDir); part != "" {
		parts = append(parts, part)
	}
	if part := normalizedScopePart(cfg.WorkspaceRoot); part != "" {
		parts = append(parts, part)
	}
	return strings.Join(parts, "|")
}

func NewDeduplicatingService(scopeID string, inner serverapi.SessionLaunchService) serverapi.SessionLaunchService {
	return &deduplicatingService{scopeID: strings.TrimSpace(scopeID), inner: inner}
}

func (s *Service) PlanSession(_ context.Context, req serverapi.SessionPlanRequest) (serverapi.SessionPlanResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.SessionPlanResponse{}, err
	}
	plan, err := s.planner.PlanSession(launch.SessionRequest{
		Mode:              launch.Mode(req.Mode),
		SelectedSessionID: req.SelectedSessionID,
		ForceNewSession:   req.ForceNewSession,
		ParentSessionID:   req.ParentSessionID,
	})
	if err != nil {
		return serverapi.SessionPlanResponse{}, err
	}
	if s.stores != nil {
		s.stores.RegisterStore(plan.Store)
	}
	enabledToolIDs := make([]string, 0, len(plan.EnabledTools))
	for _, id := range plan.EnabledTools {
		enabledToolIDs = append(enabledToolIDs, string(id))
	}
	return serverapi.SessionPlanResponse{Plan: serverapi.SessionPlan{
		SessionID:           plan.Store.Meta().SessionID,
		ActiveSettings:      plan.ActiveSettings,
		EnabledToolIDs:      enabledToolIDs,
		ConfiguredModelName: plan.ConfiguredModelName,
		SessionName:         plan.SessionName,
		ModelContractLocked: plan.ModelContractLocked,
		WorkspaceRoot:       plan.WorkspaceRoot,
		Source:              plan.Source,
	}}, nil
}

func normalizedScopePart(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	return filepath.Clean(trimmed)
}

func sweepExpiredEntriesLocked(now time.Time) {
	for key, entry := range dedupeRegistry.entries {
		if entry == nil || !entry.done || entry.completedAt.IsZero() {
			continue
		}
		if now.Sub(entry.completedAt) >= dedupeRetention {
			delete(dedupeRegistry.entries, key)
		}
	}
}

func (s *deduplicatingService) PlanSession(ctx context.Context, req serverapi.SessionPlanRequest) (serverapi.SessionPlanResponse, error) {
	for {
		key := strings.Join([]string{s.scopeID, strings.TrimSpace(req.ClientRequestID)}, "|")
		fp := dedupeFingerprint{
			mode:              strings.TrimSpace(string(req.Mode)),
			selectedSessionID: strings.TrimSpace(req.SelectedSessionID),
			forceNewSession:   req.ForceNewSession,
			parentSessionID:   strings.TrimSpace(req.ParentSessionID),
		}

		dedupeRegistry.mu.Lock()
		sweepExpiredEntriesLocked(dedupeNow())
		entry, exists := dedupeRegistry.entries[key]
		if exists {
			if entry.fingerprint != fp {
				dedupeRegistry.mu.Unlock()
				return serverapi.SessionPlanResponse{}, fmt.Errorf("client_request_id %q reused with different payload", req.ClientRequestID)
			}
			if entry.done {
				if entry.cacheable {
					response, err := entry.response, entry.err
					dedupeRegistry.mu.Unlock()
					return response, err
				}
				delete(dedupeRegistry.entries, key)
				dedupeRegistry.mu.Unlock()
				continue
			}
			ready := entry.ready
			dedupeRegistry.mu.Unlock()
			select {
			case <-ready:
				continue
			case <-ctx.Done():
				return serverapi.SessionPlanResponse{}, ctx.Err()
			}
		}

		entry = &dedupeEntry{fingerprint: fp, ready: make(chan struct{})}
		dedupeRegistry.entries[key] = entry
		dedupeRegistry.mu.Unlock()

		response, err := s.inner.PlanSession(ctx, req)
		cacheable := !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)

		dedupeRegistry.mu.Lock()
		entry.response = response
		entry.err = err
		entry.done = true
		entry.cacheable = cacheable
		entry.completedAt = dedupeNow()
		close(entry.ready)
		if !cacheable {
			delete(dedupeRegistry.entries, key)
		}
		dedupeRegistry.mu.Unlock()
		return response, err
	}
}

var _ serverapi.SessionLaunchService = (*Service)(nil)
var _ serverapi.SessionLaunchService = (*deduplicatingService)(nil)
