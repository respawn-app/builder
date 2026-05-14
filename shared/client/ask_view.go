package client

import (
	"context"
	"errors"

	"builder/shared/serverapi"
	"builder/shared/servicecontract"
)

type AskViewClient interface {
	ListPendingAsksBySession(ctx context.Context, req serverapi.AskListPendingBySessionRequest) (serverapi.AskListPendingBySessionResponse, error)
}

type loopbackAskViewClient struct {
	service servicecontract.AskViewService
}

func NewLoopbackAskViewClient(service servicecontract.AskViewService) AskViewClient {
	return &loopbackAskViewClient{service: service}
}

func (c *loopbackAskViewClient) ListPendingAsksBySession(ctx context.Context, req serverapi.AskListPendingBySessionRequest) (serverapi.AskListPendingBySessionResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.AskListPendingBySessionResponse{}, errors.New("ask view service is required")
	}
	return c.service.ListPendingAsksBySession(ctx, req)
}
