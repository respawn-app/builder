package runprompt

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"builder/server/auth"
	"builder/server/llm"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/tools"
	askquestion "builder/server/tools/askquestion"
	shelltool "builder/server/tools/shell"
	"builder/shared/client"
	"builder/shared/config"
	"builder/shared/serverapi"
)

const SubagentSessionSuffix = "subagent"

type HeadlessBootstrap struct {
	Config           config.App
	ContainerDir     string
	AuthManager      *auth.Manager
	FastModeState    *runtime.FastModeState
	Background       *shelltool.Manager
	BackgroundRouter interface {
		SetActiveSession(sessionID string, engine *runtime.Engine)
		ClearActiveSession(sessionID string)
	}
}

func NewLoopbackRunPromptClient(boot HeadlessBootstrap) client.RunPromptClient {
	launcher := &headlessPromptLauncher{boot: boot}
	service := newDeduplicatingPromptService(strings.TrimSpace(boot.Config.PersistenceRoot), serverapi.NewPromptService(launcher))
	return client.NewLoopbackRunPromptClient(service)
}

type headlessPromptLauncher struct {
	boot HeadlessBootstrap
}

func (l *headlessPromptLauncher) PrepareHeadlessPrompt(_ context.Context, req serverapi.RunPromptRequest, progress serverapi.RunPromptProgressSink) (serverapi.PromptSessionRuntime, error) {
	plan, err := l.planSession(req.SelectedSessionID)
	if err != nil {
		return nil, err
	}
	runtimePlan, err := l.prepareRuntime(plan, progress)
	if err != nil {
		return nil, err
	}
	return &headlessPromptRuntime{plan: runtimePlan}, nil
}

type headlessSessionPlan struct {
	Store          *session.Store
	ActiveSettings config.Settings
	EnabledTools   []tools.ID
	WorkspaceRoot  string
	Source         config.SourceReport
}

type headlessRuntimePlan struct {
	logger      *RunLogger
	engine      *runtime.Engine
	eventBridge *RuntimeEventBridge
	close       func()
}

func (p *headlessRuntimePlan) Close() {
	if p == nil || p.close == nil {
		return
	}
	p.close()
}

type headlessRuntimeWiringOptions struct {
	OnEvent  func(evt runtime.Event)
	FastMode *runtime.FastModeState
}

type headlessRuntimeWiring struct {
	engine      *runtime.Engine
	eventBridge *RuntimeEventBridge
	background  *shelltool.Manager
}

func (l *headlessPromptLauncher) planSession(selectedSessionID string) (headlessSessionPlan, error) {
	store, err := l.openStore(selectedSessionID)
	if err != nil {
		return headlessSessionPlan{}, err
	}
	if err := EnsureSubagentSessionName(store); err != nil {
		return headlessSessionPlan{}, err
	}
	meta := store.Meta()
	active := EffectiveSettings(l.boot.Config.Settings, meta.Locked)
	if meta.Continuation != nil {
		if baseURL := strings.TrimSpace(meta.Continuation.OpenAIBaseURL); baseURL != "" {
			active.OpenAIBaseURL = baseURL
		}
	}
	if err := store.SetContinuationContext(session.ContinuationContext{OpenAIBaseURL: active.OpenAIBaseURL}); err != nil {
		return headlessSessionPlan{}, err
	}
	return headlessSessionPlan{
		Store:          store,
		ActiveSettings: active,
		EnabledTools:   ActiveToolIDs(active, l.boot.Config.Source, meta.Locked),
		WorkspaceRoot:  l.boot.Config.WorkspaceRoot,
		Source:         l.boot.Config.Source,
	}, nil
}

func (l *headlessPromptLauncher) openStore(selectedSessionID string) (*session.Store, error) {
	if strings.TrimSpace(selectedSessionID) != "" {
		return session.OpenByID(l.boot.Config.PersistenceRoot, selectedSessionID)
	}
	containerName := filepath.Base(l.boot.ContainerDir)
	return session.NewLazy(l.boot.ContainerDir, containerName, l.boot.Config.WorkspaceRoot)
}

func (l *headlessPromptLauncher) prepareRuntime(plan headlessSessionPlan, progress serverapi.RunPromptProgressSink) (*headlessRuntimePlan, error) {
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
	for _, line := range ConfigSourceLines(plan.Source.Sources) {
		logger.Logf("config.source %s", line)
	}
	wiring, err := newHeadlessRuntimeWiringWithBackground(plan.Store, plan.ActiveSettings, plan.EnabledTools, plan.WorkspaceRoot, l.boot.AuthManager, logger, l.boot.Background, headlessRuntimeWiringOptions{
		FastMode: l.boot.FastModeState,
		OnEvent: func(evt runtime.Event) {
			PublishRunPromptProgress(progress, evt)
		},
	})
	if err != nil {
		_ = logger.Close()
		return nil, err
	}
	if l.boot.BackgroundRouter != nil {
		l.boot.BackgroundRouter.SetActiveSession(plan.Store.Meta().SessionID, wiring.engine)
	}
	return &headlessRuntimePlan{
		logger:      logger,
		engine:      wiring.engine,
		eventBridge: wiring.eventBridge,
		close: func() {
			if l.boot.BackgroundRouter != nil {
				l.boot.BackgroundRouter.ClearActiveSession(plan.Store.Meta().SessionID)
			}
			_ = wiring.Close()
			_ = logger.Close()
		},
	}, nil
}

func newHeadlessRuntimeWiringWithBackground(store *session.Store, active config.Settings, enabledTools []tools.ID, workspaceRoot string, mgr *auth.Manager, logger *RunLogger, background *shelltool.Manager, opts headlessRuntimeWiringOptions) (*headlessRuntimeWiring, error) {
	toolRegistry, askBroker, background, err := BuildToolRegistry(
		workspaceRoot,
		store.Meta().SessionID,
		enabledTools,
		time.Duration(active.Timeouts.ShellDefaultSeconds)*time.Second,
		time.Duration(active.MinimumExecToBgSeconds)*time.Second,
		active.ShellOutputMaxChars,
		active.AllowNonCwdEdits,
		llm.LockedContractSupportsVisionInputs(store.Meta().Locked, active.Model),
		logger,
		background,
	)
	if err != nil {
		return nil, err
	}
	askBroker.SetAskHandler(RunPromptAskHandler)

	modelHTTPClient := &http.Client{Timeout: time.Duration(active.Timeouts.ModelRequestSeconds) * time.Second}
	providerClient, err := llm.NewProviderClient(llm.ProviderClientOptions{
		Provider:            llm.Provider(strings.TrimSpace(active.ProviderOverride)),
		Model:               active.Model,
		Auth:                mgr,
		HTTPClient:          modelHTTPClient,
		OpenAIBaseURL:       active.OpenAIBaseURL,
		ModelVerbosity:      string(active.ModelVerbosity),
		Store:               active.Store,
		ContextWindowTokens: active.ModelContextWindow,
	})
	if err != nil {
		return nil, err
	}

	newReviewerClient := func() (llm.Client, error) {
		reviewerHTTPClient := &http.Client{Timeout: time.Duration(active.Reviewer.TimeoutSeconds) * time.Second}
		return llm.NewProviderClient(llm.ProviderClientOptions{
			Provider:            llm.Provider(strings.TrimSpace(active.ProviderOverride)),
			Model:               active.Reviewer.Model,
			Auth:                mgr,
			HTTPClient:          reviewerHTTPClient,
			OpenAIBaseURL:       active.OpenAIBaseURL,
			ModelVerbosity:      string(active.ModelVerbosity),
			Store:               false,
			ContextWindowTokens: active.ModelContextWindow,
		})
	}

	var reviewerClient llm.Client
	if strings.ToLower(strings.TrimSpace(active.Reviewer.Frequency)) != "off" {
		reviewerClient, err = newReviewerClient()
		if err != nil {
			return nil, err
		}
	}

	eventBridge := NewRuntimeEventBridge(2048, func(total uint64, evt runtime.Event) {
		if total == 1 || total%100 == 0 {
			logger.Logf("runtime.event.drop count=%d kind=%s step_id=%s", total, evt.Kind, evt.StepID)
		}
	})
	providerCapsOverride, hasProviderCapsOverride := llm.ProviderCapabilitiesFromOverride(active.ProviderCapabilities)
	engine, err := runtime.New(store, providerClient, toolRegistry, runtime.Config{
		Model:             active.Model,
		Temperature:       1,
		MaxTokens:         0,
		ThinkingLevel:     active.ThinkingLevel,
		ModelCapabilities: llm.LockedModelCapabilitiesForConfig(active.Model, active.ModelCapabilities),
		FastModeEnabled:   active.PriorityRequestMode,
		FastModeState:     opts.FastMode,
		WebSearchMode:     active.WebSearch,
		ProviderCapabilitiesOverride: func() *llm.ProviderCapabilities {
			if !hasProviderCapsOverride {
				return nil
			}
			return &providerCapsOverride
		}(),
		EnabledTools:                  enabledTools,
		DisabledSkills:                config.DisabledSkillToggles(active),
		AutoCompactTokenLimit:         active.ContextCompactionThresholdTokens,
		PreSubmitCompactionLeadTokens: active.PreSubmitCompactionLeadTokens,
		ContextWindowTokens:           active.ModelContextWindow,
		EffectiveContextWindowPercent: 95,
		LocalCompactionCarryoverLimit: 20_000,
		CompactionMode:                string(active.CompactionMode),
		AutoCompactionEnabled:         boolRef(true),
		HeadlessMode:                  true,
		ToolPreambles:                 active.ToolPreambles,
		Reviewer: runtime.ReviewerConfig{
			Frequency:     active.Reviewer.Frequency,
			Model:         active.Reviewer.Model,
			ThinkingLevel: active.Reviewer.ThinkingLevel,
			VerboseOutput: active.Reviewer.VerboseOutput,
			Client:        reviewerClient,
			ClientFactory: newReviewerClient,
		},
		OnEvent: func(evt runtime.Event) {
			logger.Logf("%s", FormatRuntimeEvent(evt))
			if opts.OnEvent != nil {
				opts.OnEvent(evt)
			}
			eventBridge.Publish(evt)
		},
	})
	if err != nil {
		return nil, err
	}
	return &headlessRuntimeWiring{engine: engine, eventBridge: eventBridge, background: background}, nil
}

func (w *headlessRuntimeWiring) Close() error { return nil }

func boolRef(v bool) *bool { return &v }

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

func EnsureSubagentSessionName(store *session.Store) error {
	if store == nil {
		return errors.New("session store is required")
	}
	meta := store.Meta()
	if strings.TrimSpace(meta.Name) != "" {
		return nil
	}
	name := strings.TrimSpace(meta.SessionID + " " + SubagentSessionSuffix)
	if name == "" {
		return nil
	}
	return store.SetName(name)
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
	ready       chan struct{}
}

var runPromptDedupeRegistry = struct {
	mu      sync.Mutex
	entries map[string]*dedupeEntry
}{entries: map[string]*dedupeEntry{}}

func newDeduplicatingPromptService(scopeID string, inner serverapi.RunPromptService) serverapi.RunPromptService {
	return &deduplicatingPromptService{scopeID: strings.TrimSpace(scopeID), inner: inner}
}

func (s *deduplicatingPromptService) RunPrompt(ctx context.Context, req serverapi.RunPromptRequest, progress serverapi.RunPromptProgressSink) (serverapi.RunPromptResponse, error) {
	for {
		key := strings.Join([]string{s.scopeID, strings.TrimSpace(req.SelectedSessionID), strings.TrimSpace(req.ClientRequestID)}, "|")
		fp := dedupeFingerprint{selectedSessionID: strings.TrimSpace(req.SelectedSessionID), prompt: strings.TrimSpace(req.Prompt)}

		runPromptDedupeRegistry.mu.Lock()
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
		close(entry.ready)
		runPromptDedupeRegistry.mu.Unlock()
		return response, err
	}
}
