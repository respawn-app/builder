package sessionlaunch

import (
	"context"
	"errors"
	"strings"

	"builder/server/auth"
	"builder/server/launch"
	"builder/server/requestmemo"
	"builder/server/session"
	"builder/shared/serverapi"
	"builder/shared/servicecontract"
)

type sessionStoreRegistrar interface {
	RegisterStore(store *session.Store)
}

type authStateReader interface {
	CurrentState(context.Context) (auth.State, error)
}

type Service struct {
	planner    launch.Planner
	stores     sessionStoreRegistrar
	authStates authStateReader
	projectID  string
	guard      ProjectDeleteGuard
	gate       ProjectSideEffectGate
	plans      *requestmemo.Memo[sessionPlanMemoRequest, PlanResult]
}

type ProjectDeleteGuard interface {
	RequireNoProjectDeleteInProgress(ctx context.Context, projectID string) error
}

type ProjectSideEffectGate interface {
	WithProject(ctx context.Context, projectID string, fn func(context.Context) error) error
}

type PlanResult struct {
	Plan     launch.SessionPlan
	Warnings []string
}

type sessionPlanMemoRequest struct {
	Mode              serverapi.SessionLaunchMode
	SelectedSessionID string
	ForceNewSession   bool
	ParentSessionID   string
	Overrides         serverapi.RunPromptOverrides
}

func NewService(planner launch.Planner, stores sessionStoreRegistrar) *Service {
	return &Service{planner: planner, stores: stores, plans: requestmemo.New[sessionPlanMemoRequest, PlanResult]()}
}

func (s *Service) WithAuthStateReader(reader authStateReader) *Service {
	if s == nil {
		return nil
	}
	s.authStates = reader
	return s
}

func (s *Service) WithProjectDeleteGuard(projectID string, guard ProjectDeleteGuard, gate ProjectSideEffectGate) *Service {
	if s == nil {
		return nil
	}
	s.projectID = strings.TrimSpace(projectID)
	s.guard = guard
	s.gate = gate
	return s
}

func (s *Service) PlanSession(ctx context.Context, req serverapi.SessionPlanRequest) (serverapi.SessionPlanResponse, error) {
	result, err := s.PlanLaunchSession(ctx, req)
	if err != nil {
		return serverapi.SessionPlanResponse{}, err
	}
	return sessionPlanResponseFromResult(result), nil
}

func (s *Service) PlanLaunchSession(ctx context.Context, req serverapi.SessionPlanRequest) (PlanResult, error) {
	if err := req.Validate(); err != nil {
		return PlanResult{}, err
	}
	memoReq := sessionPlanMemoRequest{
		Mode:              req.Mode,
		SelectedSessionID: strings.TrimSpace(req.SelectedSessionID),
		ForceNewSession:   req.ForceNewSession,
		ParentSessionID:   strings.TrimSpace(req.ParentSessionID),
		Overrides:         req.Overrides,
	}
	return s.plans.Do(ctx, strings.TrimSpace(req.ClientRequestID), memoReq, sameSessionPlanMemoRequest, func(ctx context.Context) (PlanResult, error) {
		if s.projectID != "" && s.guard != nil {
			if err := s.guard.RequireNoProjectDeleteInProgress(ctx, s.projectID); err != nil {
				return PlanResult{}, err
			}
		}
		var result PlanResult
		err := s.withProjectSideEffectGate(ctx, func(ctx context.Context) error {
			var err error
			result, err = s.planLaunchSessionInsideGate(ctx, req)
			return err
		})
		return result, err
	})
}

func (s *Service) planLaunchSessionInsideGate(ctx context.Context, req serverapi.SessionPlanRequest) (PlanResult, error) {
	if s.projectID != "" && s.guard != nil {
		if err := s.guard.RequireNoProjectDeleteInProgress(ctx, s.projectID); err != nil {
			return PlanResult{}, err
		}
	}
	plan, err := s.planner.PlanSession(ctx, launch.SessionRequest{
		Mode:              launch.Mode(req.Mode),
		SelectedSessionID: req.SelectedSessionID,
		ForceNewSession:   req.ForceNewSession,
		ParentSessionID:   req.ParentSessionID,
	})
	if err != nil {
		return PlanResult{}, err
	}
	authState := auth.EmptyState()
	if req.Overrides.NeedsAuthState() && s.authStates != nil {
		var authErr error
		authState, authErr = s.authStates.CurrentState(ctx)
		if authErr != nil {
			return PlanResult{}, authErr
		}
	}
	plan, warnings, err := launch.ApplyRunPromptOverrides(plan, req.Overrides, authState)
	if err != nil {
		return PlanResult{}, err
	}
	if s.stores != nil {
		s.stores.RegisterStore(plan.Store)
	}
	return PlanResult{Plan: plan, Warnings: warnings}, nil
}

func (s *Service) withProjectSideEffectGate(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return errors.New("session launch callback is required")
	}
	if s == nil || s.gate == nil || s.projectID == "" {
		return fn(ctx)
	}
	return s.gate.WithProject(ctx, s.projectID, fn)
}

func sessionPlanResponseFromResult(result PlanResult) serverapi.SessionPlanResponse {
	enabledToolIDs := make([]string, 0, len(result.Plan.EnabledTools))
	for _, id := range result.Plan.EnabledTools {
		enabledToolIDs = append(enabledToolIDs, string(id))
	}
	return serverapi.SessionPlanResponse{Plan: serverapi.SessionPlan{
		SessionID:           result.Plan.Store.Meta().SessionID,
		ActiveSettings:      result.Plan.ActiveSettings,
		EnabledToolIDs:      enabledToolIDs,
		ConfiguredModelName: result.Plan.ConfiguredModelName,
		SessionName:         result.Plan.SessionName,
		ModelContractLocked: result.Plan.ModelContractLocked,
		WorkspaceRoot:       result.Plan.WorkspaceRoot,
		Source:              result.Plan.Source,
	}, Warnings: result.Warnings}
}

func sameSessionPlanMemoRequest(a sessionPlanMemoRequest, b sessionPlanMemoRequest) bool {
	return a.Mode == b.Mode &&
		a.SelectedSessionID == b.SelectedSessionID &&
		a.ForceNewSession == b.ForceNewSession &&
		a.ParentSessionID == b.ParentSessionID &&
		a.Overrides == b.Overrides
}

var _ servicecontract.SessionLaunchService = (*Service)(nil)
