package app

import (
	"context"
	"errors"
	"strings"
	"sync"
)

var errControllerLeaseRecoveryUnavailable = errors.New("controller lease recovery is unavailable")

const controllerLeaseRecoveryTimeout = uiRuntimeControlTimeout

type controllerLeaseRecoverFunc func(context.Context) (string, error)

type controllerLeaseManager struct {
	mu        sync.Mutex
	leaseID   string
	recoverFn controllerLeaseRecoverFunc
	inflight  *controllerLeaseRecovery
}

type controllerLeaseRecovery struct {
	done    chan struct{}
	leaseID string
	err     error
}

func newControllerLeaseManager(leaseID string) *controllerLeaseManager {
	return &controllerLeaseManager{leaseID: strings.TrimSpace(leaseID)}
}

func (m *controllerLeaseManager) Value() string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.leaseID
}

func (m *controllerLeaseManager) Set(leaseID string) {
	if m == nil {
		return
	}
	trimmed := strings.TrimSpace(leaseID)
	if trimmed == "" {
		return
	}
	m.mu.Lock()
	m.leaseID = trimmed
	m.mu.Unlock()
}

func (m *controllerLeaseManager) SetRecoverFunc(fn controllerLeaseRecoverFunc) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.recoverFn = fn
	m.mu.Unlock()
}

func (m *controllerLeaseManager) Recover(ctx context.Context) (string, error) {
	if m == nil {
		return "", errControllerLeaseRecoveryUnavailable
	}
	m.mu.Lock()
	if recovery := m.inflight; recovery != nil {
		m.mu.Unlock()
		return waitForControllerLeaseRecovery(ctx, recovery)
	}
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return "", err
	}
	if m.recoverFn == nil {
		m.mu.Unlock()
		return "", errControllerLeaseRecoveryUnavailable
	}
	recovery := &controllerLeaseRecovery{done: make(chan struct{})}
	m.inflight = recovery
	recoverFn := m.recoverFn
	m.mu.Unlock()
	go m.runRecovery(recovery, recoverFn)
	return waitForControllerLeaseRecovery(ctx, recovery)
}

func (m *controllerLeaseManager) runRecovery(recovery *controllerLeaseRecovery, recoverFn controllerLeaseRecoverFunc) {
	recoverCtx, cancel := context.WithTimeout(context.Background(), controllerLeaseRecoveryTimeout)
	defer cancel()
	leaseID, err := recoverFn(recoverCtx)
	trimmedLeaseID := strings.TrimSpace(leaseID)

	m.mu.Lock()
	if err == nil && trimmedLeaseID != "" {
		m.leaseID = trimmedLeaseID
	}
	recovery.leaseID = m.leaseID
	recovery.err = err
	close(recovery.done)
	m.inflight = nil
	m.mu.Unlock()
}

func waitForControllerLeaseRecovery(ctx context.Context, recovery *controllerLeaseRecovery) (string, error) {
	if recovery == nil {
		return "", errControllerLeaseRecoveryUnavailable
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-recovery.done:
		return recovery.leaseID, recovery.err
	}
}
