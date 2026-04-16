package sessionlaunch

import (
	"context"

	"builder/server/launch"
	"builder/server/session"
	"builder/shared/serverapi"
)

type sessionStoreRegistrar interface {
	RegisterStore(store *session.Store)
}

type Service struct {
	planner launch.Planner
	stores  sessionStoreRegistrar
}

func NewService(planner launch.Planner, stores sessionStoreRegistrar) *Service {
	return &Service{planner: planner, stores: stores}
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

var _ serverapi.SessionLaunchService = (*Service)(nil)
