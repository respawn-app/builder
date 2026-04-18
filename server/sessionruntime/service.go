package sessionruntime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"builder/server/auth"
	"builder/server/metadata"
	"builder/server/registry"
	"builder/server/runprompt"
	"builder/server/runtime"
	"builder/server/runtimeview"
	"builder/server/runtimewire"
	"builder/server/session"
	askquestion "builder/server/tools/askquestion"
	shelltool "builder/server/tools/shell"
	"builder/shared/clientui"
	"builder/shared/config"
	"builder/shared/serverapi"
	"builder/shared/toolspec"
	"builder/shared/transcriptdiag"

	"github.com/google/uuid"
)

type Service struct {
	persistenceRoot  string
	metadataStore    *metadata.Store
	authManager      *auth.Manager
	fastModeState    *runtime.FastModeState
	background       *shelltool.Manager
	backgroundRouter *runtimewire.BackgroundEventRouter
	runtimes         *registry.RuntimeRegistry
	sessionStores    *registry.SessionStoreRegistry
	storeOptions     []session.StoreOption

	mu      sync.Mutex
	handles map[string]*runtimeHandle
}

type runtimeHandle struct {
	controllerRequestID string
	controllerLeaseID   string
	activationErr       error
	ready               chan struct{}
	close               func()
}

func NewService(persistenceRoot string, metadataStore *metadata.Store, authManager *auth.Manager, fastModeState *runtime.FastModeState, background *shelltool.Manager, backgroundRouter *runtimewire.BackgroundEventRouter, runtimes *registry.RuntimeRegistry, sessionStores *registry.SessionStoreRegistry, storeOptions ...session.StoreOption) *Service {
	return &Service{
		persistenceRoot:  strings.TrimSpace(persistenceRoot),
		metadataStore:    metadataStore,
		authManager:      authManager,
		fastModeState:    fastModeState,
		background:       background,
		backgroundRouter: backgroundRouter,
		runtimes:         runtimes,
		sessionStores:    sessionStores,
		storeOptions:     append([]session.StoreOption(nil), storeOptions...),
		handles:          make(map[string]*runtimeHandle),
	}
}

func (s *Service) ActivateSessionRuntime(ctx context.Context, req serverapi.SessionRuntimeActivateRequest) (serverapi.SessionRuntimeActivateResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.SessionRuntimeActivateResponse{}, err
	}
	sessionID := strings.TrimSpace(req.SessionID)
	requestID := strings.TrimSpace(req.ClientRequestID)
	handle, owner, err := s.claimActivation(sessionID, requestID)
	if err != nil {
		return serverapi.SessionRuntimeActivateResponse{}, err
	}
	if !owner {
		if err := waitForRuntimeHandleReady(ctx, handle); err != nil {
			return serverapi.SessionRuntimeActivateResponse{}, err
		}
		return activationResponseForHandle(handle)
	}
	var leaseID string
	var cleanup func()
	defer func() {
		if err == nil {
			return
		}
		if cleanup != nil {
			cleanup()
		}
		if strings.TrimSpace(leaseID) != "" {
			_, _ = s.releaseRuntimeLease(context.Background(), sessionID, leaseID)
		}
		s.failActivation(sessionID, handle, err)
	}()
	store, err := s.resolveStore(ctx, sessionID)
	if err != nil {
		return serverapi.SessionRuntimeActivateResponse{}, err
	}
	if err := store.EnsureDurable(); err != nil {
		return serverapi.SessionRuntimeActivateResponse{}, err
	}
	if err := s.releaseStaleRuntimeLeases(ctx, sessionID); err != nil {
		return serverapi.SessionRuntimeActivateResponse{}, err
	}
	lease, err := s.createRuntimeLease(ctx, sessionID, requestID)
	if err != nil {
		return serverapi.SessionRuntimeActivateResponse{}, err
	}
	leaseID = lease.LeaseID
	target, err := s.resolveExecutionTarget(ctx, sessionID)
	if err != nil {
		return serverapi.SessionRuntimeActivateResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		return serverapi.SessionRuntimeActivateResponse{}, err
	}
	logger, err := runprompt.NewRunLogger(store.Dir(), nil)
	if err != nil {
		return serverapi.SessionRuntimeActivateResponse{}, err
	}
	logger.Logf("app.interactive.start session_id=%s workspace=%s workdir=%s model=%s", sessionID, target.WorkspaceRoot, target.EffectiveWorkdir, req.ActiveSettings.Model)
	logger.Logf("config.settings path=%s created=%t", req.Source.SettingsPath, req.Source.CreatedDefaultConfig)
	for _, line := range configSourceLines(req.Source.Sources) {
		logger.Logf("config.source %s", line)
	}
	enabledTools, err := parseToolIDs(req.EnabledToolIDs)
	if err != nil {
		_ = logger.Close()
		return serverapi.SessionRuntimeActivateResponse{}, err
	}
	wiring, err := runtimewire.NewRuntimeWiringWithBackground(store, req.ActiveSettings, enabledTools, target.EffectiveWorkdir, s.authManager, logger, s.background, runtimewire.RuntimeWiringOptions{
		FastMode: s.fastModeState,
		OnEvent: func(evt runtime.Event) {
			logger.Logf("%s", runprompt.FormatRuntimeEvent(evt))
			if transcriptdiag.EnabledForProcess(req.ActiveSettings.Debug) {
				projected := runtimeview.EventFromRuntime(evt)
				logger.Logf("%s", runprompt.FormatTranscriptProjectionDiagnostic(sessionID, projected))
				logger.Logf("%s", runprompt.FormatTranscriptPublishDiagnostic(sessionID, projected))
			}
			if s.runtimes != nil {
				s.runtimes.PublishRuntimeEvent(sessionID, evt)
			}
		},
	})
	if err != nil {
		_ = logger.Close()
		return serverapi.SessionRuntimeActivateResponse{}, err
	}
	if wiring.AskBroker != nil && s.runtimes != nil {
		wiring.AskBroker.SetAskHandler(func(req askquestion.Request) (askquestion.Response, error) {
			return s.runtimes.AwaitPromptResponse(context.Background(), sessionID, req)
		})
	}
	if s.runtimes != nil {
		s.runtimes.Register(sessionID, wiring.Engine)
	}
	if s.backgroundRouter != nil {
		s.backgroundRouter.SetActiveSession(sessionID, wiring.Engine)
	}
	cleanup = func() {
		if s.runtimes != nil {
			s.runtimes.Unregister(sessionID, wiring.Engine)
		}
		if s.backgroundRouter != nil {
			s.backgroundRouter.ClearActiveSession(sessionID)
		}
		_ = wiring.Close()
		_ = logger.Close()
	}
	s.completeActivation(handle, leaseID, cleanup)
	cleanup = nil
	return serverapi.SessionRuntimeActivateResponse{LeaseID: leaseID}, nil
}

func (s *Service) ReleaseSessionRuntime(ctx context.Context, req serverapi.SessionRuntimeReleaseRequest) (serverapi.SessionRuntimeReleaseResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.SessionRuntimeReleaseResponse{}, err
	}
	sessionID := strings.TrimSpace(req.SessionID)
	leaseID := strings.TrimSpace(req.LeaseID)
	s.mu.Lock()
	handle := s.handles[sessionID]
	if handle == nil {
		s.mu.Unlock()
		return serverapi.SessionRuntimeReleaseResponse{}, errors.Join(serverapi.ErrInvalidControllerLease, fmt.Errorf("controller lease for session %q is invalid or expired", sessionID))
	}
	s.mu.Unlock()
	if err := waitForRuntimeHandleReady(ctx, handle); err != nil {
		return serverapi.SessionRuntimeReleaseResponse{}, err
	}
	s.mu.Lock()
	current := s.handles[sessionID]
	if current == nil || current != handle || strings.TrimSpace(current.controllerLeaseID) != leaseID {
		s.mu.Unlock()
		return serverapi.SessionRuntimeReleaseResponse{}, errors.Join(serverapi.ErrInvalidControllerLease, fmt.Errorf("controller lease for session %q is invalid or expired", sessionID))
	}
	delete(s.handles, sessionID)
	closeFn := current.close
	s.mu.Unlock()
	leaseErr := error(nil)
	if _, err := s.releaseRuntimeLease(ctx, sessionID, leaseID); err != nil {
		leaseErr = err
	}
	if closeFn != nil {
		closeFn()
	}
	return serverapi.SessionRuntimeReleaseResponse{}, leaseErr
}

func (s *Service) RequireControllerLease(ctx context.Context, sessionID string, leaseID string) error {
	trimmedSessionID := strings.TrimSpace(sessionID)
	trimmedLeaseID := strings.TrimSpace(leaseID)
	if trimmedLeaseID == "" {
		return errors.Join(serverapi.ErrInvalidControllerLease, fmt.Errorf("controller lease for session %q is required", trimmedSessionID))
	}
	s.mu.Lock()
	handle := s.handles[trimmedSessionID]
	s.mu.Unlock()
	if handle == nil {
		return errors.Join(serverapi.ErrInvalidControllerLease, fmt.Errorf("controller lease for session %q is invalid or expired", trimmedSessionID))
	}
	if err := waitForRuntimeHandleReady(ctx, handle); err != nil {
		return err
	}
	s.mu.Lock()
	current := s.handles[trimmedSessionID]
	if current == nil || current != handle {
		s.mu.Unlock()
		return errors.Join(serverapi.ErrInvalidControllerLease, fmt.Errorf("controller lease for session %q is invalid or expired", trimmedSessionID))
	}
	activationErr := current.activationErr
	controllerLeaseID := strings.TrimSpace(current.controllerLeaseID)
	s.mu.Unlock()
	if activationErr != nil {
		return errors.Join(serverapi.ErrInvalidControllerLease, fmt.Errorf("controller lease for session %q is invalid or expired", trimmedSessionID))
	}
	if controllerLeaseID != trimmedLeaseID {
		return errors.Join(serverapi.ErrInvalidControllerLease, fmt.Errorf("controller lease for session %q is invalid or expired", trimmedSessionID))
	}
	return nil
}

func (s *Service) resolveStore(ctx context.Context, sessionID string) (*session.Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.sessionStores != nil {
		store, err := s.sessionStores.ResolveStore(ctx, sessionID)
		if err != nil {
			return nil, err
		}
		if store != nil {
			return store, nil
		}
	}
	store, err := session.OpenByID(s.persistenceRoot, sessionID, s.storeOptions...)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.sessionStores != nil {
		s.sessionStores.RegisterStore(store)
	}
	return store, nil
}

// Phase 2 temporarily allows many attached readers, but exactly one controlling
// client per session. A second activation must fail explicitly instead of
// joining the active runtime.
func (s *Service) claimActivation(sessionID string, requestID string) (*runtimeHandle, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current := s.handles[sessionID]; current != nil {
		if current.controllerRequestID == requestID {
			return current, false, nil
		}
		return nil, false, errors.Join(serverapi.ErrSessionAlreadyControlled, fmt.Errorf("session %q is already controlled by another client", sessionID))
	}
	handle := &runtimeHandle{controllerRequestID: requestID, ready: make(chan struct{})}
	s.handles[sessionID] = handle
	return handle, true, nil
}

func (s *Service) completeActivation(handle *runtimeHandle, leaseID string, closeFn func()) {
	if handle == nil {
		return
	}
	handle.controllerLeaseID = strings.TrimSpace(leaseID)
	handle.close = closeFn
	close(handle.ready)
}

func (s *Service) failActivation(sessionID string, handle *runtimeHandle, err error) {
	if handle == nil {
		return
	}
	handle.activationErr = err
	close(handle.ready)
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.handles[strings.TrimSpace(sessionID)]
	if current == nil || current != handle {
		return
	}
	delete(s.handles, strings.TrimSpace(sessionID))
}

func (s *Service) resolveExecutionTarget(ctx context.Context, sessionID string) (clientui.SessionExecutionTarget, error) {
	if s == nil || s.metadataStore == nil {
		return clientui.SessionExecutionTarget{}, fmt.Errorf("metadata store is required")
	}
	return s.metadataStore.ResolveSessionExecutionTarget(ctx, sessionID)
}

func (s *Service) createRuntimeLease(ctx context.Context, sessionID string, requestID string) (metadata.RuntimeLeaseRecord, error) {
	if s == nil || s.metadataStore == nil {
		return metadata.RuntimeLeaseRecord{}, fmt.Errorf("metadata store is required")
	}
	return s.metadataStore.CreateRuntimeLease(ctx, sessionID, requestID)
}

func (s *Service) releaseRuntimeLease(ctx context.Context, sessionID string, leaseID string) (metadata.RuntimeLeaseRecord, error) {
	if s == nil || s.metadataStore == nil {
		return metadata.RuntimeLeaseRecord{}, fmt.Errorf("metadata store is required")
	}
	return s.metadataStore.ReleaseRuntimeLease(ctx, sessionID, leaseID)
}

func (s *Service) releaseStaleRuntimeLeases(ctx context.Context, sessionID string) error {
	if s == nil || s.metadataStore == nil {
		return fmt.Errorf("metadata store is required")
	}
	return s.metadataStore.ReleaseActiveRuntimeLeasesBySession(ctx, sessionID)
}

func waitForRuntimeHandleReady(ctx context.Context, handle *runtimeHandle) error {
	if handle == nil || handle.ready == nil {
		return nil
	}
	select {
	case <-handle.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func activationResponseForHandle(handle *runtimeHandle) (serverapi.SessionRuntimeActivateResponse, error) {
	if handle == nil {
		return serverapi.SessionRuntimeActivateResponse{}, fmt.Errorf("activate session runtime: missing runtime handle")
	}
	if handle.activationErr != nil {
		return serverapi.SessionRuntimeActivateResponse{}, handle.activationErr
	}
	leaseID := strings.TrimSpace(handle.controllerLeaseID)
	if leaseID == "" {
		return serverapi.SessionRuntimeActivateResponse{}, fmt.Errorf("activate session runtime: controller lease is unavailable")
	}
	return serverapi.SessionRuntimeActivateResponse{LeaseID: leaseID}, nil
}

func parseToolIDs(raw []string) ([]toolspec.ID, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	ids := make([]toolspec.ID, 0, len(raw))
	for _, item := range raw {
		id, ok := toolspec.ParseID(item)
		if !ok {
			return nil, fmt.Errorf("unknown tool id %q", item)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func configSourceLines(src map[string]string) []string {
	keys := make([]string, 0, len(src))
	for key := range src {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, fmt.Sprintf("%s=%s", key, strings.TrimSpace(src[key])))
	}
	return lines
}

func NewActivateRequest(clientRequestID string, sessionID string, settings config.Settings, enabledToolIDs []string, source config.SourceReport) serverapi.SessionRuntimeActivateRequest {
	id := strings.TrimSpace(clientRequestID)
	if id == "" {
		id = uuid.NewString()
	}
	return serverapi.SessionRuntimeActivateRequest{
		ClientRequestID: id,
		SessionID:       strings.TrimSpace(sessionID),
		ActiveSettings:  settings,
		EnabledToolIDs:  append([]string(nil), enabledToolIDs...),
		Source:          source,
	}
}

var _ serverapi.SessionRuntimeService = (*Service)(nil)
