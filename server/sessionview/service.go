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
	ResolveSession(ctx context.Context, sessionID string) (session.Snapshot, error)
}

type SessionStoreResolver interface {
	ResolveSessionStore(ctx context.Context, sessionID string) (*session.Store, error)
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

func (r staticSessionResolver) ResolveSession(_ context.Context, sessionID string) (session.Snapshot, error) {
	if r.store == nil {
		return session.Snapshot{}, errors.New("session store is required")
	}
	if strings.TrimSpace(sessionID) != strings.TrimSpace(r.store.Meta().SessionID) {
		return session.Snapshot{}, fmt.Errorf("session %q not available", strings.TrimSpace(sessionID))
	}
	return session.SnapshotFromStore(r.store)
}

func (r staticSessionResolver) ResolveSessionStore(_ context.Context, sessionID string) (*session.Store, error) {
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
	if store, err := s.resolveSessionStore(ctx, req.SessionID); err != nil {
		return serverapi.SessionMainViewResponse{}, err
	} else if store != nil {
		view, err := dormantMainViewFromStore(ctx, store)
		if err != nil {
			return serverapi.SessionMainViewResponse{}, err
		}
		return serverapi.SessionMainViewResponse{MainView: view}, nil
	}
	snapshot, err := s.resolveSession(ctx, req.SessionID)
	if err != nil {
		return serverapi.SessionMainViewResponse{}, err
	}
	view, err := dormantMainView(ctx, snapshot)
	if err != nil {
		return serverapi.SessionMainViewResponse{}, err
	}
	return serverapi.SessionMainViewResponse{MainView: view}, nil
}

func (s *Service) GetSessionTranscriptPage(ctx context.Context, req serverapi.SessionTranscriptPageRequest) (serverapi.SessionTranscriptPageResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.SessionTranscriptPageResponse{}, err
	}
	pageReq := clientui.TranscriptPageRequest{Offset: req.Offset, Limit: req.Limit, Page: req.Page, PageSize: req.PageSize, Window: req.Window}
	if runtimeEngine, err := s.resolveRuntime(ctx, req.SessionID); err != nil {
		return serverapi.SessionTranscriptPageResponse{}, err
	} else if runtimeEngine != nil {
		return serverapi.SessionTranscriptPageResponse{Transcript: runtimeview.TranscriptPageFromRuntime(runtimeEngine, pageReq)}, nil
	}
	if store, err := s.resolveSessionStore(ctx, req.SessionID); err != nil {
		return serverapi.SessionTranscriptPageResponse{}, err
	} else if store != nil {
		page, err := dormantTranscriptPageFromStore(ctx, store, pageReq)
		if err != nil {
			return serverapi.SessionTranscriptPageResponse{}, err
		}
		return serverapi.SessionTranscriptPageResponse{Transcript: page}, nil
	}
	snapshot, err := s.resolveSession(ctx, req.SessionID)
	if err != nil {
		return serverapi.SessionTranscriptPageResponse{}, err
	}
	chat, _, err := replayDormantSession(ctx, snapshot)
	if err != nil {
		return serverapi.SessionTranscriptPageResponse{}, err
	}
	return serverapi.SessionTranscriptPageResponse{Transcript: runtimeview.TranscriptPageFromChat(
		snapshot.Meta.SessionID,
		snapshot.Meta.Name,
		runtimeview.ConversationFreshnessFromSession(snapshot.ConversationFreshness),
		snapshot.Meta.LastSequence,
		chat,
		pageReq,
	)}, nil
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
	snapshot, err := s.resolveSession(ctx, req.SessionID)
	if err != nil {
		return serverapi.RunGetResponse{}, err
	}
	for _, run := range snapshot.Runs {
		if run.RunID == strings.TrimSpace(req.RunID) {
			copyRun := run
			return serverapi.RunGetResponse{Run: runtimeview.RunViewFromSessionRecord(snapshot.Meta.SessionID, &copyRun)}, nil
		}
	}
	return serverapi.RunGetResponse{}, fmt.Errorf("run %q not found", strings.TrimSpace(req.RunID))
}

func (s *Service) resolveSession(ctx context.Context, sessionID string) (session.Snapshot, error) {
	if s == nil || s.sessions == nil {
		return session.Snapshot{}, errors.New("session resolver is required")
	}
	return s.sessions.ResolveSession(ctx, sessionID)
}

func (s *Service) resolveSessionStore(ctx context.Context, sessionID string) (*session.Store, error) {
	if s == nil || s.sessions == nil {
		return nil, nil
	}
	resolver, ok := s.sessions.(SessionStoreResolver)
	if !ok {
		return nil, nil
	}
	return resolver.ResolveSessionStore(ctx, sessionID)
}

func (s *Service) resolveRuntime(ctx context.Context, sessionID string) (*runtime.Engine, error) {
	if s == nil || s.runtimes == nil {
		return nil, nil
	}
	return s.runtimes.ResolveRuntime(ctx, sessionID)
}

func dormantMainView(ctx context.Context, snapshot session.Snapshot) (clientui.RuntimeMainView, error) {
	meta := snapshot.Meta
	freshness := runtimeview.ConversationFreshnessFromSession(snapshot.ConversationFreshness)
	chat, lastAnswer, err := replayDormantSession(ctx, snapshot)
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
			Transcript: clientui.TranscriptMetadata{
				Revision:            meta.LastSequence,
				CommittedEntryCount: len(chat.Entries),
			},
		},
	}
	if len(snapshot.Runs) > 0 {
		latestRun := snapshot.Runs[len(snapshot.Runs)-1]
		if latestRun.Status == session.RunStatusRunning {
			view.ActiveRun = runtimeview.RunViewFromSessionRecord(meta.SessionID, &latestRun)
		}
	}
	return view, nil
}

func dormantMainViewFromStore(ctx context.Context, store *session.Store) (clientui.RuntimeMainView, error) {
	if store == nil {
		return clientui.RuntimeMainView{}, errors.New("session store is required")
	}
	scan, err := scanDormantTranscript(ctx, store, runtime.PersistedTranscriptScanRequest{})
	if err != nil {
		return clientui.RuntimeMainView{}, err
	}
	meta := store.Meta()
	freshness := runtimeview.ConversationFreshnessFromSession(store.ConversationFreshness())
	view := clientui.RuntimeMainView{
		Status: clientui.RuntimeStatus{
			ConversationFreshness:             freshness,
			ParentSessionID:                   meta.ParentSessionID,
			LastCommittedAssistantFinalAnswer: scan.LastCommittedAssistantFinalAnswer(),
		},
		Session: clientui.RuntimeSessionView{
			SessionID:             meta.SessionID,
			SessionName:           meta.Name,
			ConversationFreshness: freshness,
			Transcript: clientui.TranscriptMetadata{
				Revision:            meta.LastSequence,
				CommittedEntryCount: scan.TotalEntries(),
			},
		},
	}
	runs, err := store.ReadRuns()
	if err != nil {
		return clientui.RuntimeMainView{}, err
	}
	if len(runs) > 0 {
		latestRun := runs[len(runs)-1]
		if latestRun.Status == session.RunStatusRunning {
			view.ActiveRun = runtimeview.RunViewFromSessionRecord(meta.SessionID, &latestRun)
		}
	}
	return view, nil
}

func dormantTranscriptPageFromStore(ctx context.Context, store *session.Store, req clientui.TranscriptPageRequest) (clientui.TranscriptPage, error) {
	if store == nil {
		return clientui.TranscriptPage{}, errors.New("session store is required")
	}
	meta := store.Meta()
	freshness := runtimeview.ConversationFreshnessFromSession(store.ConversationFreshness())
	if req.Window == clientui.TranscriptWindowOngoingTail {
		scan, err := scanDormantTranscript(ctx, store, runtime.PersistedTranscriptScanRequest{TrackOngoingTail: true, TailLimit: runtimeview.OngoingTailEntryLimit})
		if err != nil {
			return clientui.TranscriptPage{}, err
		}
		return runtimeview.TranscriptPageFromWindow(
			meta.SessionID,
			meta.Name,
			freshness,
			meta.LastSequence,
			scan.OngoingTailSnapshot(),
		), nil
	}
	offset := req.Offset
	limit := req.Limit
	if req.PageSize > 0 {
		offset = req.Page * req.PageSize
		limit = req.PageSize
	}
	scan, err := scanDormantTranscript(ctx, store, runtime.PersistedTranscriptScanRequest{Offset: offset, Limit: limit})
	if err != nil {
		return clientui.TranscriptPage{}, err
	}
	if offset > scan.TotalEntries() {
		offset = scan.TotalEntries()
	}
	return runtimeview.TranscriptPageFromCollectedChat(
		meta.SessionID,
		meta.Name,
		freshness,
		meta.LastSequence,
		runtimeview.ChatSnapshotFromRuntime(scan.CollectedPageSnapshot()),
		scan.TotalEntries(),
		offset,
		clientui.TranscriptPageRequest{Offset: offset, Limit: limit},
	), nil
}

func scanDormantTranscript(ctx context.Context, store *session.Store, req runtime.PersistedTranscriptScanRequest) (*runtime.PersistedTranscriptScan, error) {
	scan := runtime.NewPersistedTranscriptScan(req)
	if err := store.WalkEvents(func(evt session.Event) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		return scan.ApplyPersistedEvent(evt)
	}); err != nil {
		return nil, err
	}
	return scan, nil
}

type dormantReplayClient struct{}

func (dormantReplayClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New("replay client does not support generation")
}

func (dormantReplayClient) ProviderCapabilities(context.Context) (llm.ProviderCapabilities, error) {
	return llm.ProviderCapabilities{ProviderID: "replay"}, nil
}

func replayDormantSession(ctx context.Context, snapshot session.Snapshot) (clientui.ChatSnapshot, string, error) {
	cloneStore, cleanup, err := cloneStoreForReplay(ctx, snapshot)
	if err != nil {
		return clientui.ChatSnapshot{}, "", err
	}
	defer cleanup()

	model := "gpt-5"
	if locked := snapshot.Meta.Locked; locked != nil && strings.TrimSpace(locked.Model) != "" {
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

func cloneStoreForReplay(ctx context.Context, snapshot session.Snapshot) (*session.Store, func(), error) {
	meta := snapshot.Meta
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
	replay := make([]session.ReplayEvent, 0, len(snapshot.Events))
	for _, evt := range snapshot.Events {
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
