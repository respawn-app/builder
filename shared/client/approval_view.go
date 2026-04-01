package client

import (
	"context"
	"errors"

	"builder/shared/serverapi"
)

type ApprovalViewClient interface {
	ListPendingApprovalsBySession(ctx context.Context, req serverapi.ApprovalListPendingBySessionRequest) (serverapi.ApprovalListPendingBySessionResponse, error)
}

type loopbackApprovalViewClient struct {
	service serverapi.ApprovalViewService
}

func NewLoopbackApprovalViewClient(service serverapi.ApprovalViewService) ApprovalViewClient {
	return &loopbackApprovalViewClient{service: service}
}

func (c *loopbackApprovalViewClient) ListPendingApprovalsBySession(ctx context.Context, req serverapi.ApprovalListPendingBySessionRequest) (serverapi.ApprovalListPendingBySessionResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.ApprovalListPendingBySessionResponse{}, errors.New("approval view service is required")
	}
	return c.service.ListPendingApprovalsBySession(ctx, req)
}
