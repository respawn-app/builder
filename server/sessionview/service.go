package sessionview

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"builder/server/llm"
	"builder/server/runtime"
	"builder/server/runtimeview"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/clientui"
	"builder/shared/serverapi"
)

type SessionResolver interface {
	ResolveSession(ctx context.Context, sessionID string) (*session.Store, error)
}

type RuntimeResolver interface {
	ResolveRuntime(ctx context.Context, sessionID string) (*runtime.Engine, error)
}

type Service struct {
	sessions SessionResolver
	runtimes RuntimeResolver
}

func NewService(sessions SessionResolver, runtimes RuntimeResolver) *Service {
	return &Service{sessions: sessions, runtimes: runtimes}
}

type staticSessionResolver struct {
	store *session.Store
}

func NewStaticSessionResolver(store *session.Store) SessionResolver {
	if store == nil {
		return nil
	}
	return staticSessionResolver{store: store}
}

func (r staticSessionResolver) ResolveSession(_ context.Context, sessionID string) (*session.Store, error) {
	if r.store == nil {
		return nil, errors.New("session store is required")
	}
	if strings.TrimSpace(sessionID) != strings.TrimSpace(r.store.Meta().SessionID) {
		return nil, fmt.Errorf("session %q not available", strings.TrimSpace(sessionID))
	}
	return r.store, nil
}

type staticRuntimeResolver struct {
	engine *runtime.Engine
}

func NewStaticRuntimeResolver(engine *runtime.Engine) RuntimeResolver {
	if engine == nil {
		return nil
	}
	return staticRuntimeResolver{engine: engine}
}

func (r staticRuntimeResolver) ResolveRuntime(_ context.Context, sessionID string) (*runtime.Engine, error) {
	if r.engine == nil {
		return nil, nil
	}
	if strings.TrimSpace(sessionID) != strings.TrimSpace(r.engine.SessionID()) {
		return nil, fmt.Errorf("session %q not available", strings.TrimSpace(sessionID))
	}
	return r.engine, nil
}

func (s *Service) GetSessionMainView(ctx context.Context, req serverapi.SessionMainViewRequest) (serverapi.SessionMainViewResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.SessionMainViewResponse{}, err
	}
	if runtimeEngine, err := s.resolveRuntime(ctx, req.SessionID); err != nil {
		return serverapi.SessionMainViewResponse{}, err
	} else if runtimeEngine != nil {
		return serverapi.SessionMainViewResponse{MainView: runtimeview.MainViewFromRuntime(runtimeEngine)}, nil
	}
	store, err := s.resolveSession(ctx, req.SessionID)
	if err != nil {
		return serverapi.SessionMainViewResponse{}, err
	}
	view, err := dormantMainView(ctx, store)
	if err != nil {
		return serverapi.SessionMainViewResponse{}, err
	}
	return serverapi.SessionMainViewResponse{MainView: view}, nil
}

func (s *Service) GetRun(ctx context.Context, req serverapi.RunGetRequest) (serverapi.RunGetResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.RunGetResponse{}, err
	}
	if runtimeEngine, err := s.resolveRuntime(ctx, req.SessionID); err != nil {
		return serverapi.RunGetResponse{}, err
	} else if runtimeEngine != nil {
		if active := runtimeview.RunViewFromRuntime(runtimeEngine.SessionID(), runtimeEngine.ActiveRun()); active != nil && strings.TrimSpace(active.RunID) == strings.TrimSpace(req.RunID) {
			return serverapi.RunGetResponse{Run: active}, nil
		}
	}
	store, err := s.resolveSession(ctx, req.SessionID)
	if err != nil {
		return serverapi.RunGetResponse{}, err
	}
	runs, err := store.ReadRuns()
	if err != nil {
		return serverapi.RunGetResponse{}, err
	}
	for _, run := range runs {
		if run.RunID == strings.TrimSpace(req.RunID) {
			copyRun := run
			return serverapi.RunGetResponse{Run: runtimeview.RunViewFromSessionRecord(store.Meta().SessionID, &copyRun)}, nil
		}
	}
	return serverapi.RunGetResponse{}, fmt.Errorf("run %q not found", strings.TrimSpace(req.RunID))
}

func (s *Service) resolveSession(ctx context.Context, sessionID string) (*session.Store, error) {
	if s == nil || s.sessions == nil {
		return nil, errors.New("session resolver is required")
	}
	return s.sessions.ResolveSession(ctx, sessionID)
}

func (s *Service) resolveRuntime(ctx context.Context, sessionID string) (*runtime.Engine, error) {
	if s == nil || s.runtimes == nil {
		return nil, nil
	}
	return s.runtimes.ResolveRuntime(ctx, sessionID)
}

func dormantMainView(ctx context.Context, store *session.Store) (clientui.RuntimeMainView, error) {
	meta := store.Meta()
	freshness := runtimeview.ConversationFreshnessFromSession(store.ConversationFreshness())
	chat, lastAnswer, err := replayDormantSession(ctx, store)
	if err != nil {
		return clientui.RuntimeMainView{}, err
	}
	view := clientui.RuntimeMainView{
		Status: clientui.RuntimeStatus{
			ConversationFreshness:             freshness,
			ParentSessionID:                   meta.ParentSessionID,
			LastCommittedAssistantFinalAnswer: lastAnswer,
		},
		Session: clientui.RuntimeSessionView{
			SessionID:             meta.SessionID,
			SessionName:           meta.Name,
			ConversationFreshness: freshness,
			Chat:                  chat,
		},
	}
	latestRun, err := store.LatestRun()
	if err != nil {
		return clientui.RuntimeMainView{}, err
	}
	if latestRun != nil && latestRun.Status == session.RunStatusRunning {
		view.ActiveRun = runtimeview.RunViewFromSessionRecord(meta.SessionID, latestRun)
	}
	return view, nil
}

type dormantReplayClient struct{}

func (dormantReplayClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New("replay client does not support generation")
}

func (dormantReplayClient) ProviderCapabilities(context.Context) (llm.ProviderCapabilities, error) {
	return llm.ProviderCapabilities{ProviderID: "replay"}, nil
}

func replayDormantSession(ctx context.Context, store *session.Store) (clientui.ChatSnapshot, string, error) {
	cloneStore, cleanup, err := cloneStoreForReplay(ctx, store)
	if err != nil {
		return clientui.ChatSnapshot{}, "", err
	}
	defer cleanup()

	model := "gpt-5"
	if locked := store.Meta().Locked; locked != nil && strings.TrimSpace(locked.Model) != "" {
		model = strings.TrimSpace(locked.Model)
	}
	eng, err := runtime.New(cloneStore, dormantReplayClient{}, tools.NewRegistry(), runtime.Config{Model: model})
	if err != nil {
		return clientui.ChatSnapshot{}, "", err
	}
	select {
	case <-ctx.Done():
		return clientui.ChatSnapshot{}, "", ctx.Err()
	default:
	}
	return runtimeview.ChatSnapshotFromRuntime(eng.ChatSnapshot()), eng.LastCommittedAssistantFinalAnswer(), nil
}

func cloneStoreForReplay(ctx context.Context, source *session.Store) (*session.Store, func(), error) {
	if source == nil {
		return nil, nil, errors.New("session store is required")
	}
	meta := source.Meta()
	events, err := source.ReadEvents()
	if err != nil {
		return nil, nil, err
	}
	tempRoot, err := os.MkdirTemp("", "builder-sessionview-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		_ = os.RemoveAll(tempRoot)
	}
	cloneStore, err := session.NewLazy(tempRoot, meta.WorkspaceContainer, meta.WorkspaceRoot)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	if err := cloneStore.SetName(meta.Name); err != nil {
		cleanup()
		return nil, nil, err
	}
	if err := cloneStore.SetParentSessionID(meta.ParentSessionID); err != nil {
		cleanup()
		return nil, nil, err
	}
	if meta.Continuation != nil {
		if err := cloneStore.SetContinuationContext(*meta.Continuation); err != nil {
			cleanup()
			return nil, nil, err
		}
	}
	if meta.AgentsInjected {
		if err := cloneStore.MarkAgentsInjected(); err != nil {
			cleanup()
			return nil, nil, err
		}
	}
	replay := make([]session.ReplayEvent, 0, len(events))
	for _, evt := range events {
		select {
		case <-ctx.Done():
			cleanup()
			return nil, nil, ctx.Err()
		default:
		}
		replay = append(replay, session.ReplayEvent{StepID: evt.StepID, Kind: evt.Kind, Payload: evt.Payload})
	}
	if _, err := cloneStore.AppendReplayEvents(replay); err != nil {
		cleanup()
		return nil, nil, err
	}
	return cloneStore, cleanup, nil
}

var _ serverapi.SessionViewService = (*Service)(nil)
