package sessionruntime

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"builder/server/auth"
	"builder/server/registry"
	"builder/server/runprompt"
	"builder/server/runtime"
	"builder/server/runtimewire"
	"builder/server/session"
	"builder/server/tools"
	askquestion "builder/server/tools/askquestion"
	shelltool "builder/server/tools/shell"
	"builder/shared/config"
	"builder/shared/serverapi"
	"github.com/google/uuid"
)

type Service struct {
	persistenceRoot  string
	authManager      *auth.Manager
	fastModeState    *runtime.FastModeState
	background       *shelltool.Manager
	backgroundRouter *runtimewire.BackgroundEventRouter
	runtimes         *registry.RuntimeRegistry
	sessionStores    *registry.SessionStoreRegistry

	mu      sync.Mutex
	handles map[string]*runtimeHandle
}

type runtimeHandle struct {
	refs               int
	activationRequests map[string]struct{}
	releaseRequests    map[string]struct{}
	ready              chan struct{}
	close              func()
}

func NewService(persistenceRoot string, authManager *auth.Manager, fastModeState *runtime.FastModeState, background *shelltool.Manager, backgroundRouter *runtimewire.BackgroundEventRouter, runtimes *registry.RuntimeRegistry, sessionStores *registry.SessionStoreRegistry) *Service {
	return &Service{
		persistenceRoot:  strings.TrimSpace(persistenceRoot),
		authManager:      authManager,
		fastModeState:    fastModeState,
		background:       background,
		backgroundRouter: backgroundRouter,
		runtimes:         runtimes,
		sessionStores:    sessionStores,
		handles:          make(map[string]*runtimeHandle),
	}
}

func (s *Service) ActivateSessionRuntime(ctx context.Context, req serverapi.SessionRuntimeActivateRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	sessionID := strings.TrimSpace(req.SessionID)
	requestID := strings.TrimSpace(req.ClientRequestID)

	s.mu.Lock()
	if handle := s.handles[sessionID]; handle != nil {
		if _, ok := handle.activationRequests[requestID]; ok {
			s.mu.Unlock()
			return waitForRuntimeHandleReady(ctx, handle)
		}
		handle.activationRequests[requestID] = struct{}{}
		handle.refs++
		s.mu.Unlock()
		return waitForRuntimeHandleReady(ctx, handle)
	}
	s.mu.Unlock()

	store, err := s.resolveStore(ctx, sessionID)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	logger, err := runprompt.NewRunLogger(store.Dir(), nil)
	if err != nil {
		return err
	}
	logger.Logf("app.interactive.start session_id=%s workspace=%s model=%s", sessionID, req.WorkspaceRoot, req.ActiveSettings.Model)
	logger.Logf("config.settings path=%s created=%t", req.Source.SettingsPath, req.Source.CreatedDefaultConfig)
	for _, line := range configSourceLines(req.Source.Sources) {
		logger.Logf("config.source %s", line)
	}
	enabledTools, err := parseToolIDs(req.EnabledToolIDs)
	if err != nil {
		_ = logger.Close()
		return err
	}
	wiring, err := runtimewire.NewRuntimeWiringWithBackground(store, req.ActiveSettings, enabledTools, req.WorkspaceRoot, s.authManager, logger, s.background, runtimewire.RuntimeWiringOptions{
		FastMode: s.fastModeState,
		OnEvent: func(evt runtime.Event) {
			logger.Logf("%s", runprompt.FormatRuntimeEvent(evt))
			if s.runtimes != nil {
				s.runtimes.PublishRuntimeEvent(sessionID, evt)
			}
		},
	})
	if err != nil {
		_ = logger.Close()
		return err
	}
	if wiring.AskBroker != nil && s.runtimes != nil {
		wiring.AskBroker.SetAskHandler(func(req askquestion.Request) (askquestion.Response, error) {
			return s.runtimes.AwaitPromptResponse(context.Background(), sessionID, req)
		})
	}
	handle := &runtimeHandle{
		refs:               1,
		activationRequests: map[string]struct{}{requestID: {}},
		releaseRequests:    make(map[string]struct{}),
		ready:              make(chan struct{}),
		close: func() {
			if s.runtimes != nil {
				s.runtimes.Unregister(sessionID, wiring.Engine)
			}
			if s.backgroundRouter != nil {
				s.backgroundRouter.ClearActiveSession(sessionID)
			}
			_ = logger.Close()
		},
	}
	current, installed := s.installHandle(sessionID, requestID, handle)
	if !installed {
		_ = logger.Close()
		return waitForRuntimeHandleReady(ctx, current)
	}
	defer close(handle.ready)
	if s.runtimes != nil {
		s.runtimes.Register(sessionID, wiring.Engine)
	}
	if s.backgroundRouter != nil {
		s.backgroundRouter.SetActiveSession(sessionID, wiring.Engine)
	}
	return nil
}

func (s *Service) ReleaseSessionRuntime(_ context.Context, req serverapi.SessionRuntimeReleaseRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	sessionID := strings.TrimSpace(req.SessionID)
	requestID := strings.TrimSpace(req.ClientRequestID)
	s.mu.Lock()
	handle := s.handles[sessionID]
	if handle == nil {
		s.mu.Unlock()
		return nil
	}
	if _, ok := handle.releaseRequests[requestID]; ok {
		s.mu.Unlock()
		return nil
	}
	handle.releaseRequests[requestID] = struct{}{}
	if handle.refs > 0 {
		handle.refs--
	}
	if handle.refs > 0 {
		s.mu.Unlock()
		return nil
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
	store, err := session.OpenByID(s.persistenceRoot, sessionID)
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

func (s *Service) installHandle(sessionID string, requestID string, handle *runtimeHandle) (*runtimeHandle, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current := s.handles[sessionID]; current != nil {
		if _, exists := current.activationRequests[requestID]; exists {
			return current, false
		}
		current.activationRequests[requestID] = struct{}{}
		current.refs++
		return current, false
	}
	s.handles[sessionID] = handle
	return handle, true
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

func NewActivateRequest(clientRequestID string, sessionID string, settings config.Settings, enabledToolIDs []string, workspaceRoot string, source config.SourceReport) serverapi.SessionRuntimeActivateRequest {
	id := strings.TrimSpace(clientRequestID)
	if id == "" {
		id = uuid.NewString()
	}
	return serverapi.SessionRuntimeActivateRequest{
		ClientRequestID: id,
		SessionID:       strings.TrimSpace(sessionID),
		ActiveSettings:  settings,
		EnabledToolIDs:  append([]string(nil), enabledToolIDs...),
		WorkspaceRoot:   strings.TrimSpace(filepath.Clean(workspaceRoot)),
		Source:          source,
	}
}

var _ serverapi.SessionRuntimeService = (*Service)(nil)
