package sessionlaunch

import (
	"context"
	"strings"

	"builder/server/auth"
	"builder/server/launch"
	"builder/server/requestmemo"
	"builder/server/session"
	"builder/shared/serverapi"
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
	plans      *requestmemo.Memo[sessionPlanMemoRequest, serverapi.SessionPlanResponse]
}

type sessionPlanMemoRequest struct {
	Mode              serverapi.SessionLaunchMode
	SelectedSessionID string
	ForceNewSession   bool
	ParentSessionID   string
	Overrides         serverapi.RunPromptOverrides
}

func NewService(planner launch.Planner, stores sessionStoreRegistrar) *Service {
	return &Service{planner: planner, stores: stores, plans: requestmemo.New[sessionPlanMemoRequest, serverapi.SessionPlanResponse]()}
}

func (s *Service) WithAuthStateReader(reader authStateReader) *Service {
	if s == nil {
		return nil
	}
	s.authStates = reader
	return s
}

func (s *Service) PlanSession(ctx context.Context, req serverapi.SessionPlanRequest) (serverapi.SessionPlanResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.SessionPlanResponse{}, err
	}
	memoReq := sessionPlanMemoRequest{
		Mode:              req.Mode,
		SelectedSessionID: strings.TrimSpace(req.SelectedSessionID),
		ForceNewSession:   req.ForceNewSession,
		ParentSessionID:   strings.TrimSpace(req.ParentSessionID),
		Overrides:         req.Overrides,
	}
	return s.plans.Do(ctx, strings.TrimSpace(req.ClientRequestID), memoReq, sameSessionPlanMemoRequest, func(ctx context.Context) (serverapi.SessionPlanResponse, error) {
		plan, err := s.planner.PlanSession(ctx, launch.SessionRequest{
			Mode:              launch.Mode(req.Mode),
			SelectedSessionID: req.SelectedSessionID,
			ForceNewSession:   req.ForceNewSession,
			ParentSessionID:   req.ParentSessionID,
		})
		if err != nil {
			return serverapi.SessionPlanResponse{}, err
		}
		authState := auth.EmptyState()
		if req.Overrides.HasAny() && s.authStates != nil {
			var authErr error
			authState, authErr = s.authStates.CurrentState(ctx)
			if authErr != nil {
				return serverapi.SessionPlanResponse{}, authErr
			}
		}
		plan, warnings, err := launch.ApplyRunPromptOverrides(plan, req.Overrides, authState)
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
		}, Warnings: warnings}, nil
	})
}

func sameSessionPlanMemoRequest(a sessionPlanMemoRequest, b sessionPlanMemoRequest) bool {
	return a.Mode == b.Mode &&
		a.SelectedSessionID == b.SelectedSessionID &&
		a.ForceNewSession == b.ForceNewSession &&
		a.ParentSessionID == b.ParentSessionID &&
		a.Overrides == b.Overrides
}

var _ serverapi.SessionLaunchService = (*Service)(nil)
