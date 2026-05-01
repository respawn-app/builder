package sessioncontrol

import (
	"context"
	"errors"

	"builder/server/auth"
	"builder/server/launch"
	"builder/server/lifecycle"
	"builder/server/session"
	"builder/shared/client"
	"builder/shared/config"
)

type SessionPicker func([]session.Summary, string) (launch.SessionSelection, error)

type Controller struct {
	Config       config.App
	ContainerDir string
	ProjectID    string
	ProjectViews client.ProjectViewClient
	AuthManager  *auth.Manager
	PickSession  SessionPicker
	Reauth       func(context.Context) error
}

func (c Controller) PlanSession(ctx context.Context, req launch.SessionRequest) (launch.SessionPlan, error) {
	planner := launch.Planner{
		Config:       c.Config,
		ContainerDir: c.ContainerDir,
		ProjectID:    c.ProjectID,
		ProjectViews: c.ProjectViews,
	}
	if c.PickSession != nil {
		planner.PickSession = func(summaries []session.Summary) (launch.SessionSelection, error) {
			return c.PickSession(summaries, c.Config.Settings.Theme)
		}
	}
	return planner.PlanSession(ctx, req)
}

func (c Controller) ResolveTransition(ctx context.Context, store *session.Store, transition lifecycle.Transition) (lifecycle.Resolved, error) {
	resolved, err := lifecycle.Resolve(ctx, lifecycle.ResolveRequest{
		Store:       store,
		Transition:  transition,
		AuthManager: c.AuthManager,
	})
	if err != nil {
		return lifecycle.Resolved{}, err
	}
	if !resolved.RequiresReauth {
		return resolved, nil
	}
	if c.Reauth == nil {
		return lifecycle.Resolved{}, errors.New("reauth handler is required")
	}
	if err := c.Reauth(ctx); err != nil {
		return lifecycle.Resolved{}, err
	}
	return resolved, nil
}
