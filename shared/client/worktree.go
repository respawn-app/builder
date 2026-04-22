package client

import (
	"context"
	"errors"

	"builder/shared/serverapi"
)

type WorktreeClient interface {
	ListWorktrees(ctx context.Context, req serverapi.WorktreeListRequest) (serverapi.WorktreeListResponse, error)
	CreateWorktree(ctx context.Context, req serverapi.WorktreeCreateRequest) (serverapi.WorktreeCreateResponse, error)
	SwitchWorktree(ctx context.Context, req serverapi.WorktreeSwitchRequest) (serverapi.WorktreeSwitchResponse, error)
	DeleteWorktree(ctx context.Context, req serverapi.WorktreeDeleteRequest) (serverapi.WorktreeDeleteResponse, error)
}

type loopbackWorktreeClient struct {
	service serverapi.WorktreeService
}

func NewLoopbackWorktreeClient(service serverapi.WorktreeService) WorktreeClient {
	return &loopbackWorktreeClient{service: service}
}

func (c *loopbackWorktreeClient) ListWorktrees(ctx context.Context, req serverapi.WorktreeListRequest) (serverapi.WorktreeListResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.WorktreeListResponse{}, errors.New("worktree service is required")
	}
	return c.service.ListWorktrees(ctx, req)
}

func (c *loopbackWorktreeClient) CreateWorktree(ctx context.Context, req serverapi.WorktreeCreateRequest) (serverapi.WorktreeCreateResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.WorktreeCreateResponse{}, errors.New("worktree service is required")
	}
	return c.service.CreateWorktree(ctx, req)
}

func (c *loopbackWorktreeClient) SwitchWorktree(ctx context.Context, req serverapi.WorktreeSwitchRequest) (serverapi.WorktreeSwitchResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.WorktreeSwitchResponse{}, errors.New("worktree service is required")
	}
	return c.service.SwitchWorktree(ctx, req)
}

func (c *loopbackWorktreeClient) DeleteWorktree(ctx context.Context, req serverapi.WorktreeDeleteRequest) (serverapi.WorktreeDeleteResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.WorktreeDeleteResponse{}, errors.New("worktree service is required")
	}
	return c.service.DeleteWorktree(ctx, req)
}
