package runprompt

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"builder/server/auth"
	"builder/server/launch"
	"builder/server/primaryrun"
	"builder/server/runtime"
	"builder/server/runtimewire"
	askquestion "builder/server/tools/askquestion"
	shelltool "builder/server/tools/shell"
	"builder/shared/client"
	"builder/shared/config"
	"builder/shared/serverapi"
)

type HeadlessBootstrap struct {
	Config          config.App
	ContainerDir    string
	AuthManager     *auth.Manager
	FastModeState   *runtime.FastModeState
	Background      *shelltool.Manager
	RuntimeRegistry interface {
		primaryrun.Gate
		Register(sessionID string, engine *runtime.Engine)
		Unregister(sessionID string)
		PublishRuntimeEvent(sessionID string, evt runtime.Event)
	}
	BackgroundRouter interface {
		SetActiveSession(sessionID string, engine *runtime.Engine)
		ClearActiveSession(sessionID string)
	}
}

func NewLoopbackRunPromptClient(boot HeadlessBootstrap) client.RunPromptClient {
	launcher := &headlessPromptLauncher{boot: boot}
	service := newDeduplicatingPromptService(runPromptDedupeScopeID(boot), primaryrun.NewGuardingPromptService(boot.RuntimeRegistry, serverapi.NewPromptService(launcher)))
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
	planner := launch.Planner{Config: l.boot.Config, ContainerDir: l.boot.ContainerDir}
	plan, err := planner.PlanSession(launch.SessionRequest{Mode: launch.ModeHeadless, SelectedSessionID: req.SelectedSessionID})
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
				l.boot.RuntimeRegistry.Unregister(plan.Store.Meta().SessionID)
			}
			if l.boot.BackgroundRouter != nil {
				l.boot.BackgroundRouter.ClearActiveSession(plan.Store.Meta().SessionID)
			}
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
	scopeID string
	inner   serverapi.RunPromptService
}

type dedupeFingerprint struct {
	selectedSessionID string
	prompt            string
}

type dedupeEntry struct {
	fingerprint dedupeFingerprint
	response    serverapi.RunPromptResponse
	err         error
	done        bool
	cacheable   bool
	completedAt time.Time
	ready       chan struct{}
}

const runPromptDedupeRetention = 10 * time.Minute

var runPromptDedupeNow = time.Now

var runPromptDedupeRegistry = struct {
	mu      sync.Mutex
	entries map[string]*dedupeEntry
}{entries: map[string]*dedupeEntry{}}

func newDeduplicatingPromptService(scopeID string, inner serverapi.RunPromptService) serverapi.RunPromptService {
	return &deduplicatingPromptService{scopeID: strings.TrimSpace(scopeID), inner: inner}
}

func sweepExpiredRunPromptDedupeEntriesLocked(now time.Time) {
	for key, entry := range runPromptDedupeRegistry.entries {
		if entry == nil || !entry.done || entry.completedAt.IsZero() {
			continue
		}
		if now.Sub(entry.completedAt) >= runPromptDedupeRetention {
			delete(runPromptDedupeRegistry.entries, key)
		}
	}
}

func (s *deduplicatingPromptService) RunPrompt(ctx context.Context, req serverapi.RunPromptRequest, progress serverapi.RunPromptProgressSink) (serverapi.RunPromptResponse, error) {
	for {
		key := strings.Join([]string{s.scopeID, strings.TrimSpace(req.SelectedSessionID), strings.TrimSpace(req.ClientRequestID)}, "|")
		fp := dedupeFingerprint{selectedSessionID: strings.TrimSpace(req.SelectedSessionID), prompt: strings.TrimSpace(req.Prompt)}

		runPromptDedupeRegistry.mu.Lock()
		sweepExpiredRunPromptDedupeEntriesLocked(runPromptDedupeNow())
		entry, exists := runPromptDedupeRegistry.entries[key]
		if exists {
			if entry.fingerprint != fp {
				runPromptDedupeRegistry.mu.Unlock()
				return serverapi.RunPromptResponse{}, fmt.Errorf("client_request_id %q reused with different payload", req.ClientRequestID)
			}
			if entry.done {
				if entry.cacheable {
					response, err := entry.response, entry.err
					runPromptDedupeRegistry.mu.Unlock()
					return response, err
				}
				delete(runPromptDedupeRegistry.entries, key)
				runPromptDedupeRegistry.mu.Unlock()
				continue
			}
			ready := entry.ready
			runPromptDedupeRegistry.mu.Unlock()
			select {
			case <-ready:
				continue
			case <-ctx.Done():
				return serverapi.RunPromptResponse{}, ctx.Err()
			}
		}

		entry = &dedupeEntry{fingerprint: fp, ready: make(chan struct{})}
		runPromptDedupeRegistry.entries[key] = entry
		runPromptDedupeRegistry.mu.Unlock()

		response, err := s.inner.RunPrompt(ctx, req, progress)
		cacheable := !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)

		runPromptDedupeRegistry.mu.Lock()
		entry.response = response
		entry.err = err
		entry.done = true
		entry.cacheable = cacheable
		entry.completedAt = runPromptDedupeNow()
		close(entry.ready)
		if !cacheable {
			delete(runPromptDedupeRegistry.entries, key)
		}
		runPromptDedupeRegistry.mu.Unlock()
		return response, err
	}
}
