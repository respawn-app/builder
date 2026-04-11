package sessionruntime

import (
	"context"
	"fmt"
	"os"
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
	"builder/server/tools"
	askquestion "builder/server/tools/askquestion"
	shelltool "builder/server/tools/shell"
	"builder/shared/clientui"
	"builder/shared/config"
	"builder/shared/serverapi"
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
	refs               int
	activationRequests map[string]string
	activeLeases       map[string]struct{}
	ready              chan struct{}
	close              func()
}

const maxActivationRetries = 3

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

	s.mu.Lock()
	if handle := s.handles[sessionID]; handle != nil {
		if leaseID, ok := handle.activationRequests[requestID]; ok {
			s.mu.Unlock()
			if err := waitForRuntimeHandleReady(ctx, handle); err != nil {
				return serverapi.SessionRuntimeActivateResponse{}, err
			}
			return serverapi.SessionRuntimeActivateResponse{LeaseID: leaseID}, nil
		}
		s.mu.Unlock()

		for attempt := 0; attempt < maxActivationRetries; attempt++ {
			lease, err := s.createRuntimeLease(ctx, sessionID, requestID)
			if err != nil {
				return serverapi.SessionRuntimeActivateResponse{}, err
			}
			handle, actualLeaseID, ok, ownsClaim := s.claimExistingHandleLease(sessionID, requestID, lease.LeaseID)
			if !ok {
				_, _ = s.releaseRuntimeLease(context.Background(), sessionID, lease.LeaseID)
				continue
			}
			if actualLeaseID != lease.LeaseID {
				_, _ = s.releaseRuntimeLease(context.Background(), sessionID, lease.LeaseID)
				lease.LeaseID = actualLeaseID
			}
			if err := waitForRuntimeHandleReady(ctx, handle); err != nil {
				s.rollbackActivationClaim(sessionID, requestID, lease.LeaseID, handle, ownsClaim)
				_, _ = s.releaseRuntimeLease(context.Background(), sessionID, lease.LeaseID)
				return serverapi.SessionRuntimeActivateResponse{}, err
			}
			return serverapi.SessionRuntimeActivateResponse{LeaseID: lease.LeaseID}, nil
		}
		return serverapi.SessionRuntimeActivateResponse{}, fmt.Errorf("activate session runtime: exceeded retry budget for %q", sessionID)
	}
	s.mu.Unlock()
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
	target, err := s.resolveExecutionTarget(ctx, sessionID)
	if err != nil {
		_, _ = s.releaseRuntimeLease(context.Background(), sessionID, lease.LeaseID)
		return serverapi.SessionRuntimeActivateResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		_, _ = s.releaseRuntimeLease(context.Background(), sessionID, lease.LeaseID)
		return serverapi.SessionRuntimeActivateResponse{}, err
	}
	logger, err := runprompt.NewRunLogger(store.Dir(), nil)
	if err != nil {
		_, _ = s.releaseRuntimeLease(context.Background(), sessionID, lease.LeaseID)
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
		_, _ = s.releaseRuntimeLease(context.Background(), sessionID, lease.LeaseID)
		return serverapi.SessionRuntimeActivateResponse{}, err
	}
	wiring, err := runtimewire.NewRuntimeWiringWithBackground(store, req.ActiveSettings, enabledTools, target.EffectiveWorkdir, s.authManager, logger, s.background, runtimewire.RuntimeWiringOptions{
		FastMode: s.fastModeState,
		OnEvent: func(evt runtime.Event) {
			logger.Logf("%s", runprompt.FormatRuntimeEvent(evt))
			if transcriptdiag.EnabledFromEnv(os.Getenv) {
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
		_, _ = s.releaseRuntimeLease(context.Background(), sessionID, lease.LeaseID)
		return serverapi.SessionRuntimeActivateResponse{}, err
	}
	if wiring.AskBroker != nil && s.runtimes != nil {
		wiring.AskBroker.SetAskHandler(func(req askquestion.Request) (askquestion.Response, error) {
			return s.runtimes.AwaitPromptResponse(context.Background(), sessionID, req)
		})
	}
	handle := &runtimeHandle{
		refs:               1,
		activationRequests: map[string]string{requestID: lease.LeaseID},
		activeLeases:       map[string]struct{}{lease.LeaseID: {}},
		ready:              make(chan struct{}),
		close: func() {
			if s.runtimes != nil {
				s.runtimes.Unregister(sessionID, wiring.Engine)
			}
			if s.backgroundRouter != nil {
				s.backgroundRouter.ClearActiveSession(sessionID)
			}
			_ = wiring.Close()
			_ = logger.Close()
		},
	}
	current, installed, actualLeaseID, ownsClaim := s.installHandle(sessionID, requestID, lease.LeaseID, handle)
	if !installed {
		_ = logger.Close()
		if actualLeaseID != lease.LeaseID {
			_, _ = s.releaseRuntimeLease(context.Background(), sessionID, lease.LeaseID)
			lease.LeaseID = actualLeaseID
		}
		if err := waitForRuntimeHandleReady(ctx, current); err != nil {
			s.rollbackActivationClaim(sessionID, requestID, lease.LeaseID, current, ownsClaim)
			_, _ = s.releaseRuntimeLease(context.Background(), sessionID, lease.LeaseID)
			return serverapi.SessionRuntimeActivateResponse{}, err
		}
		return serverapi.SessionRuntimeActivateResponse{LeaseID: lease.LeaseID}, nil
	}
	defer close(handle.ready)
	if s.runtimes != nil {
		s.runtimes.Register(sessionID, wiring.Engine)
	}
	if s.backgroundRouter != nil {
		s.backgroundRouter.SetActiveSession(sessionID, wiring.Engine)
	}
	return serverapi.SessionRuntimeActivateResponse{LeaseID: lease.LeaseID}, nil
}

func (s *Service) ReleaseSessionRuntime(ctx context.Context, req serverapi.SessionRuntimeReleaseRequest) (serverapi.SessionRuntimeReleaseResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.SessionRuntimeReleaseResponse{}, err
	}
	sessionID := strings.TrimSpace(req.SessionID)
	leaseID := strings.TrimSpace(req.LeaseID)
	if _, err := s.releaseRuntimeLease(ctx, sessionID, leaseID); err != nil {
		return serverapi.SessionRuntimeReleaseResponse{}, err
	}
	s.mu.Lock()
	handle := s.handles[sessionID]
	if handle == nil {
		s.mu.Unlock()
		return serverapi.SessionRuntimeReleaseResponse{}, nil
	}
	if _, ok := handle.activeLeases[leaseID]; !ok {
		s.mu.Unlock()
		return serverapi.SessionRuntimeReleaseResponse{}, nil
	}
	delete(handle.activeLeases, leaseID)
	for requestID, claimedLeaseID := range handle.activationRequests {
		if claimedLeaseID == leaseID {
			delete(handle.activationRequests, requestID)
		}
	}
	if handle.refs > 0 {
		handle.refs--
	}
	if handle.refs > 0 {
		s.mu.Unlock()
		return serverapi.SessionRuntimeReleaseResponse{}, nil
	}
	delete(s.handles, sessionID)
	ready := handle.ready
	closeFn := handle.close
	s.mu.Unlock()
	if ready != nil {
		<-ready
	}
	if closeFn != nil {
		closeFn()
	}
	return serverapi.SessionRuntimeReleaseResponse{}, nil
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

func (s *Service) installHandle(sessionID string, requestID string, leaseID string, handle *runtimeHandle) (*runtimeHandle, bool, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current := s.handles[sessionID]; current != nil {
		if currentLeaseID, exists := current.activationRequests[requestID]; exists {
			return current, false, currentLeaseID, false
		}
		current.activationRequests[requestID] = leaseID
		current.activeLeases[leaseID] = struct{}{}
		current.refs++
		return current, false, leaseID, true
	}
	s.handles[sessionID] = handle
	return handle, true, leaseID, true
}

func (s *Service) claimExistingHandleLease(sessionID string, requestID string, leaseID string) (*runtimeHandle, string, bool, bool) {
	sessionID = strings.TrimSpace(sessionID)
	requestID = strings.TrimSpace(requestID)
	leaseID = strings.TrimSpace(leaseID)
	s.mu.Lock()
	defer s.mu.Unlock()
	handle := s.handles[sessionID]
	if handle == nil {
		return nil, "", false, false
	}
	if currentLeaseID, exists := handle.activationRequests[requestID]; exists {
		return handle, currentLeaseID, true, false
	}
	handle.activationRequests[requestID] = leaseID
	handle.activeLeases[leaseID] = struct{}{}
	handle.refs++
	return handle, leaseID, true, true
}

func (s *Service) rollbackActivationClaim(sessionID string, requestID string, leaseID string, handle *runtimeHandle, ownsClaim bool) {
	if handle == nil || !ownsClaim {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.handles[strings.TrimSpace(sessionID)]
	if current == nil || current != handle {
		return
	}
	claimedLeaseID, ok := current.activationRequests[strings.TrimSpace(requestID)]
	if !ok {
		return
	}
	delete(current.activationRequests, strings.TrimSpace(requestID))
	if strings.TrimSpace(claimedLeaseID) == strings.TrimSpace(leaseID) {
		delete(current.activeLeases, strings.TrimSpace(leaseID))
	}
	if current.refs > 0 {
		current.refs--
	}
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

func parseToolIDs(raw []string) ([]tools.ID, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	ids := make([]tools.ID, 0, len(raw))
	for _, item := range raw {
		id, ok := tools.ParseID(item)
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
