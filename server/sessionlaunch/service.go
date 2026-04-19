package sessionlaunch

import (
	"context"
	"strings"

	"builder/server/launch"
	"builder/server/requestmemo"
	"builder/server/session"
	"builder/shared/serverapi"
)

type sessionStoreRegistrar interface {
	RegisterStore(store *session.Store)
}

type Service struct {
	planner launch.Planner
	stores  sessionStoreRegistrar
	plans   *requestmemo.Memo[sessionPlanMemoRequest, serverapi.SessionPlanResponse]
}

type sessionPlanMemoRequest struct {
	Mode              serverapi.SessionLaunchMode
	SelectedSessionID string
	ForceNewSession   bool
	ParentSessionID   string
}

func NewService(planner launch.Planner, stores sessionStoreRegistrar) *Service {
	return &Service{planner: planner, stores: stores, plans: requestmemo.New[sessionPlanMemoRequest, serverapi.SessionPlanResponse]()}
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
	}
	return s.plans.Do(ctx, strings.TrimSpace(req.ClientRequestID), memoReq, sameSessionPlanMemoRequest, func(context.Context) (serverapi.SessionPlanResponse, error) {
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
	})
}

func sameSessionPlanMemoRequest(a sessionPlanMemoRequest, b sessionPlanMemoRequest) bool {
	return a.Mode == b.Mode &&
		a.SelectedSessionID == b.SelectedSessionID &&
		a.ForceNewSession == b.ForceNewSession &&
		a.ParentSessionID == b.ParentSessionID
}

var _ serverapi.SessionLaunchService = (*Service)(nil)
