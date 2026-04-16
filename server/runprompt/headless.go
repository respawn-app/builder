package runprompt

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"builder/server/auth"
	"builder/server/idempotency"
	"builder/server/launch"
	"builder/server/primaryrun"
	"builder/server/runtime"
	"builder/server/runtimeview"
	"builder/server/runtimewire"
	"builder/server/session"
	askquestion "builder/server/tools/askquestion"
	shelltool "builder/server/tools/shell"
	"builder/shared/client"
	"builder/shared/config"
	"builder/shared/serverapi"
	"builder/shared/transcriptdiag"
)

type HeadlessBootstrap struct {
	Config          config.App
	ContainerDir    string
	StoreOptions    []session.StoreOption
	AuthManager     *auth.Manager
	FastModeState   *runtime.FastModeState
	Background      *shelltool.Manager
	RuntimeRegistry interface {
		primaryrun.Gate
		Register(sessionID string, engine *runtime.Engine)
		Unregister(sessionID string, engine *runtime.Engine)
		PublishRuntimeEvent(sessionID string, evt runtime.Event)
	}
	BackgroundRouter interface {
		SetActiveSession(sessionID string, engine *runtime.Engine)
		ClearActiveSession(sessionID string)
	}
	Coordinator *idempotency.Coordinator
}

func NewLoopbackRunPromptClient(boot HeadlessBootstrap) client.RunPromptClient {
	launcher := &headlessPromptLauncher{boot: boot}
	service := newDeduplicatingPromptService(runPromptDedupeScopeID(boot), boot.Coordinator, primaryrun.NewGuardingPromptService(boot.RuntimeRegistry, serverapi.NewPromptService(launcher)))
	return client.NewLoopbackRunPromptClient(service)
}

func runPromptDedupeScopeID(boot HeadlessBootstrap) string {
	parts := make([]string, 0, 3)
	if part := normalizedRunPromptScopePart(boot.Config.PersistenceRoot); part != "" {
		parts = append(parts, part)
	}
	if part := normalizedRunPromptScopePart(boot.ContainerDir); part != "" {
		parts = append(parts, part)
	}
	if part := normalizedRunPromptScopePart(boot.Config.WorkspaceRoot); part != "" {
		parts = append(parts, part)
	}
	return strings.Join(parts, "|")
}

func normalizedRunPromptScopePart(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	return filepath.Clean(trimmed)
}

type headlessPromptLauncher struct {
	boot HeadlessBootstrap
}

func (l *headlessPromptLauncher) PrepareHeadlessPrompt(_ context.Context, req serverapi.RunPromptRequest, progress serverapi.RunPromptProgressSink) (serverapi.PromptSessionRuntime, error) {
	planner := launch.Planner{Config: l.boot.Config, ContainerDir: l.boot.ContainerDir, StoreOptions: l.boot.StoreOptions}
	plan, err := planner.PlanSession(launch.SessionRequest{Mode: launch.ModeHeadless, SelectedSessionID: req.SelectedSessionID})
	if err != nil {
		return nil, err
	}
	plan, err = launch.ApplyRunPromptOverrides(plan, req.Overrides)
	if err != nil {
		return nil, err
	}
	runtimePlan, err := l.prepareRuntime(plan, progress)
	if err != nil {
		return nil, err
	}
	return &headlessPromptRuntime{plan: runtimePlan}, nil
}

type headlessRuntimePlan struct {
	logger      *RunLogger
	engine      *runtime.Engine
	eventBridge *runtimewire.EventBridge
	close       func()
}

func (p *headlessRuntimePlan) Close() {
	if p == nil || p.close == nil {
		return
	}
	p.close()
}

func (l *headlessPromptLauncher) prepareRuntime(plan launch.SessionPlan, progress serverapi.RunPromptProgressSink) (*headlessRuntimePlan, error) {
	logger, err := NewRunLogger(plan.Store.Dir(), func(diag RunLoggerDiagnostic) {
		if progress != nil {
			progress.PublishRunPromptProgress(serverapi.RunPromptProgress{Kind: serverapi.RunPromptProgressKindWarning, Message: "Run logging degraded"})
		}
	})
	if err != nil {
		return nil, err
	}
	logger.Logf("app.run_prompt.start session_id=%s workspace=%s model=%s", plan.Store.Meta().SessionID, plan.WorkspaceRoot, plan.ActiveSettings.Model)
	logger.Logf("config.settings path=%s created=%t", plan.Source.SettingsPath, plan.Source.CreatedDefaultConfig)
	for _, line := range configSourceLines(plan.Source.Sources) {
		logger.Logf("config.source %s", line)
	}
	wiring, err := runtimewire.NewRuntimeWiringWithBackground(plan.Store, plan.ActiveSettings, plan.EnabledTools, plan.WorkspaceRoot, l.boot.AuthManager, logger, l.boot.Background, runtimewire.RuntimeWiringOptions{
		Headless: true,
		FastMode: l.boot.FastModeState,
		OnEvent: func(evt runtime.Event) {
			logger.Logf("%s", FormatRuntimeEvent(evt))
			if transcriptdiag.EnabledForProcess(plan.ActiveSettings.Debug) {
				projected := runtimeview.EventFromRuntime(evt)
				logger.Logf("%s", FormatTranscriptProjectionDiagnostic(plan.Store.Meta().SessionID, projected))
				logger.Logf("%s", FormatTranscriptPublishDiagnostic(plan.Store.Meta().SessionID, projected))
			}
			if l.boot.RuntimeRegistry != nil {
				l.boot.RuntimeRegistry.PublishRuntimeEvent(plan.Store.Meta().SessionID, evt)
			}
			PublishRunPromptProgress(progress, evt)
		},
	})
	if err != nil {
		_ = logger.Close()
		return nil, err
	}
	if wiring.AskBroker != nil {
		wiring.AskBroker.SetAskHandler(RunPromptAskHandler)
	}
	if l.boot.RuntimeRegistry != nil {
		l.boot.RuntimeRegistry.Register(plan.Store.Meta().SessionID, wiring.Engine)
	}
	if l.boot.BackgroundRouter != nil {
		l.boot.BackgroundRouter.SetActiveSession(plan.Store.Meta().SessionID, wiring.Engine)
	}
	return &headlessRuntimePlan{
		logger:      logger,
		engine:      wiring.Engine,
		eventBridge: wiring.EventBridge,
		close: func() {
			if l.boot.RuntimeRegistry != nil {
				l.boot.RuntimeRegistry.Unregister(plan.Store.Meta().SessionID, wiring.Engine)
			}
			if l.boot.BackgroundRouter != nil {
				l.boot.BackgroundRouter.ClearActiveSession(plan.Store.Meta().SessionID)
			}
			_ = wiring.Close()
			_ = logger.Close()
		},
	}, nil
}

type headlessPromptRuntime struct {
	plan *headlessRuntimePlan
}

func (r *headlessPromptRuntime) SubmitUserMessage(ctx context.Context, prompt string) (serverapi.PromptAssistantMessage, error) {
	assistant, err := r.plan.engine.SubmitUserMessage(ctx, prompt)
	return serverapi.PromptAssistantMessage{Content: assistant.Content}, err
}

func (r *headlessPromptRuntime) SessionID() string   { return r.plan.engine.SessionID() }
func (r *headlessPromptRuntime) SessionName() string { return r.plan.engine.SessionName() }
func (r *headlessPromptRuntime) DroppedEvents() uint64 {
	return r.plan.eventBridge.Dropped()
}
func (r *headlessPromptRuntime) Logf(format string, args ...any) { r.plan.logger.Logf(format, args...) }
func (r *headlessPromptRuntime) Close() error {
	if r == nil || r.plan == nil {
		return nil
	}
	r.plan.Close()
	return nil
}

func RunPromptAskHandler(req askquestion.Request) (askquestion.Response, error) {
	return askquestion.Response{}, errors.New("You can't ask questions in headless/background mode. If the question is critical and materially affects the task, ask it by ending your turn after trying to do as much work as possible beforehand. Otherwise, follow best practice and mention the ambiguity in your final answer.")
}

func PublishRunPromptProgress(progress serverapi.RunPromptProgressSink, evt runtime.Event) {
	if progress == nil {
		return
	}
	state, ok := RunPromptProgressFromRuntimeEvent(evt)
	if !ok {
		return
	}
	progress.PublishRunPromptProgress(state)
}

func RunPromptProgressFromRuntimeEvent(evt runtime.Event) (serverapi.RunPromptProgress, bool) {
	switch evt.Kind {
	case runtime.EventToolCallStarted:
		return serverapi.RunPromptProgress{Kind: serverapi.RunPromptProgressKindStatus, Message: "Running tool"}, true
	case runtime.EventToolCallCompleted:
		return serverapi.RunPromptProgress{Kind: serverapi.RunPromptProgressKindStatus, Message: "Tool finished"}, true
	case runtime.EventReviewerCompleted:
		return serverapi.RunPromptProgress{Kind: serverapi.RunPromptProgressKindStatus, Message: "Review finished"}, true
	case runtime.EventCompactionStarted:
		return serverapi.RunPromptProgress{Kind: serverapi.RunPromptProgressKindStatus, Message: "Compacting context"}, true
	case runtime.EventCompactionCompleted:
		return serverapi.RunPromptProgress{Kind: serverapi.RunPromptProgressKindStatus, Message: "Context compaction finished"}, true
	case runtime.EventCompactionFailed:
		return serverapi.RunPromptProgress{Kind: serverapi.RunPromptProgressKindWarning, Message: "Context compaction failed"}, true
	case runtime.EventInFlightClearFailed:
		return serverapi.RunPromptProgress{Kind: serverapi.RunPromptProgressKindWarning, Message: "Run cleanup warning"}, true
	default:
		return serverapi.RunPromptProgress{}, false
	}
}

type deduplicatingPromptService struct {
	scopeID     string
	coordinator *idempotency.Coordinator
	inner       serverapi.RunPromptService
}

func newDeduplicatingPromptService(scopeID string, coordinator *idempotency.Coordinator, inner serverapi.RunPromptService) serverapi.RunPromptService {
	return &deduplicatingPromptService{scopeID: strings.TrimSpace(scopeID), coordinator: coordinator, inner: inner}
}

func (s *deduplicatingPromptService) RunPrompt(ctx context.Context, req serverapi.RunPromptRequest, progress serverapi.RunPromptProgressSink) (serverapi.RunPromptResponse, error) {
	if s == nil || s.inner == nil {
		return serverapi.RunPromptResponse{}, fmt.Errorf("run prompt service is required")
	}
	if s.coordinator == nil {
		return s.inner.RunPrompt(ctx, req, progress)
	}
	fingerprint, err := idempotency.FingerprintPayload(struct {
		SelectedSessionID string                       `json:"selected_session_id,omitempty"`
		Prompt            string                       `json:"prompt"`
		Timeout           int64                        `json:"timeout_ns,omitempty"`
		Overrides         serverapi.RunPromptOverrides `json:"overrides,omitempty"`
	}{
		SelectedSessionID: strings.TrimSpace(req.SelectedSessionID),
		Prompt:            strings.TrimSpace(req.Prompt),
		Timeout:           int64(req.Timeout),
		Overrides:         req.Overrides,
	})
	if err != nil {
		return serverapi.RunPromptResponse{}, err
	}
	resourceID := strings.Join([]string{strings.TrimSpace(s.scopeID), strings.TrimSpace(req.SelectedSessionID)}, "|")
	request := idempotency.Request{
		Method:             "run_prompt.run",
		ResourceID:         resourceID,
		ClientRequestID:    strings.TrimSpace(req.ClientRequestID),
		PayloadFingerprint: fingerprint,
	}
	return idempotency.ExecuteWithPolicy(ctx, s.coordinator, request, idempotency.JSONCodec[serverapi.RunPromptResponse]{}, idempotency.CachePolicy{
		ShouldCacheError: func(err error) bool {
			return !errors.Is(err, primaryrun.ErrActivePrimaryRun)
		},
	}, func(ctx context.Context) (serverapi.RunPromptResponse, error) {
		return s.inner.RunPrompt(ctx, req, progress)
	})
}
