package sessionlaunch

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"builder/server/idempotency"
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
	resourceID  string
	coordinator *idempotency.Coordinator
	inner       serverapi.SessionLaunchService
}

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

func NewDeduplicatingService(scopeID string, coordinator *idempotency.Coordinator, inner serverapi.SessionLaunchService) serverapi.SessionLaunchService {
	return &deduplicatingService{resourceID: strings.TrimSpace(scopeID), coordinator: coordinator, inner: inner}
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

func (s *deduplicatingService) PlanSession(ctx context.Context, req serverapi.SessionPlanRequest) (serverapi.SessionPlanResponse, error) {
	if s == nil || s.inner == nil {
		return serverapi.SessionPlanResponse{}, fmt.Errorf("session launch service is required")
	}
	if s.coordinator == nil {
		return s.inner.PlanSession(ctx, req)
	}
	fingerprint, err := idempotency.FingerprintPayload(req)
	if err != nil {
		return serverapi.SessionPlanResponse{}, err
	}
	request := idempotency.Request{
		Method:             "session_launch.plan",
		ResourceID:         strings.TrimSpace(s.resourceID),
		ClientRequestID:    strings.TrimSpace(req.ClientRequestID),
		PayloadFingerprint: fingerprint,
	}
	return idempotency.Execute(ctx, s.coordinator, request, idempotency.JSONCodec[serverapi.SessionPlanResponse]{}, func(ctx context.Context) (serverapi.SessionPlanResponse, error) {
		return s.inner.PlanSession(ctx, req)
	})
}

var _ serverapi.SessionLaunchService = (*Service)(nil)
var _ serverapi.SessionLaunchService = (*deduplicatingService)(nil)
