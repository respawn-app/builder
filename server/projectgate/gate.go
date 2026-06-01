package projectgate

import (
	"context"
	"errors"
	"strings"
	"sync"
)

type Gate struct {
	mu    sync.Mutex
	locks map[string]chan struct{}
}

type activeProjectsContextKey struct{}

func New() *Gate {
	return &Gate{locks: map[string]chan struct{}{}}
}

func (g *Gate) WithProject(ctx context.Context, projectID string, fn func(context.Context) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	trimmedProjectID := strings.TrimSpace(projectID)
	if trimmedProjectID == "" {
		return errors.New("project id is required")
	}
	if fn == nil {
		return errors.New("project gate callback is required")
	}
	if projectGateActive(ctx, trimmedProjectID) {
		return fn(ctx)
	}
	lock := g.lockForProject(trimmedProjectID)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-lock:
	}
	defer func() { lock <- struct{}{} }()
	return fn(contextWithProjectGate(ctx, trimmedProjectID))
}

func (g *Gate) lockForProject(projectID string) chan struct{} {
	if g == nil {
		g = New()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if lock := g.locks[projectID]; lock != nil {
		return lock
	}
	lock := make(chan struct{}, 1)
	lock <- struct{}{}
	g.locks[projectID] = lock
	return lock
}

func projectGateActive(ctx context.Context, projectID string) bool {
	active, _ := ctx.Value(activeProjectsContextKey{}).(map[string]struct{})
	_, ok := active[projectID]
	return ok
}

func contextWithProjectGate(ctx context.Context, projectID string) context.Context {
	active, _ := ctx.Value(activeProjectsContextKey{}).(map[string]struct{})
	next := make(map[string]struct{}, len(active)+1)
	for id := range active {
		next[id] = struct{}{}
	}
	next[projectID] = struct{}{}
	return context.WithValue(ctx, activeProjectsContextKey{}, next)
}
