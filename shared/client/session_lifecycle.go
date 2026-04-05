package client

import (
	"context"
	"errors"

	"builder/shared/serverapi"
)

type SessionLifecycleClient interface {
	GetInitialInput(ctx context.Context, req serverapi.SessionInitialInputRequest) (serverapi.SessionInitialInputResponse, error)
	PersistInputDraft(ctx context.Context, req serverapi.SessionPersistInputDraftRequest) (serverapi.SessionPersistInputDraftResponse, error)
	ResolveTransition(ctx context.Context, req serverapi.SessionResolveTransitionRequest) (serverapi.SessionResolveTransitionResponse, error)
}

type loopbackSessionLifecycleClient struct {
	service serverapi.SessionLifecycleService
}

func NewLoopbackSessionLifecycleClient(service serverapi.SessionLifecycleService) SessionLifecycleClient {
	return &loopbackSessionLifecycleClient{service: service}
}

func (c *loopbackSessionLifecycleClient) GetInitialInput(ctx context.Context, req serverapi.SessionInitialInputRequest) (serverapi.SessionInitialInputResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.SessionInitialInputResponse{}, errors.New("session lifecycle service is required")
	}
	return c.service.GetInitialInput(ctx, req)
}

func (c *loopbackSessionLifecycleClient) PersistInputDraft(ctx context.Context, req serverapi.SessionPersistInputDraftRequest) (serverapi.SessionPersistInputDraftResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.SessionPersistInputDraftResponse{}, errors.New("session lifecycle service is required")
	}
	return c.service.PersistInputDraft(ctx, req)
}

func (c *loopbackSessionLifecycleClient) ResolveTransition(ctx context.Context, req serverapi.SessionResolveTransitionRequest) (serverapi.SessionResolveTransitionResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.SessionResolveTransitionResponse{}, errors.New("session lifecycle service is required")
	}
	return c.service.ResolveTransition(ctx, req)
}
