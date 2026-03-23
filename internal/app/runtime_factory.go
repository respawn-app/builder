package app

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"builder/internal/auth"
	"builder/internal/config"
	"builder/internal/llm"
	"builder/internal/runtime"
	"builder/internal/session"
	"builder/internal/tools"
	askquestion "builder/internal/tools/askquestion"
	multitooluseparallel "builder/internal/tools/multitooluseparallel"
	patchtool "builder/internal/tools/patch"
	readimagetool "builder/internal/tools/readimage"
	shelltool "builder/internal/tools/shell"
)

type runtimeWiring struct {
	engine        *runtime.Engine
	askBridge     *askBridge
	eventBridge   *runtimeEventBridge
	background    *shelltool.Manager
	promptHistory []string
}

type backgroundEventRouter struct {
	mu              sync.RWMutex
	activeSessionID string
	activeSince     time.Time
	activeEngine    *runtime.Engine
	outputLimit     int
	outputMode      shelltool.BackgroundOutputMode
}

func newBackgroundEventRouter(background *shelltool.Manager, outputLimit int, outputMode shelltool.BackgroundOutputMode) *backgroundEventRouter {
	router := &backgroundEventRouter{outputLimit: outputLimit, outputMode: outputMode}
	if background != nil {
		background.SetEventHandler(router.handle)
	}
	return router
}

func (r *backgroundEventRouter) SetActiveSession(sessionID string, engine *runtime.Engine) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.activeSessionID = strings.TrimSpace(sessionID)
	r.activeSince = time.Now().UTC()
	r.activeEngine = engine
}

func (r *backgroundEventRouter) ClearActiveSession(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(sessionID) != "" && r.activeSessionID != strings.TrimSpace(sessionID) {
		return
	}
	r.activeSessionID = ""
	r.activeSince = time.Time{}
	r.activeEngine = nil
}

func (r *backgroundEventRouter) handle(evt shelltool.Event) {
	r.mu.RLock()
	activeSessionID := r.activeSessionID
	activeSince := r.activeSince
	activeEngine := r.activeEngine
	outputLimit := r.outputLimit
	outputMode := r.outputMode
	r.mu.RUnlock()
	if activeEngine == nil {
		return
	}
	summary := shelltool.BackgroundNoticeSummary{}
	if evt.Type == shelltool.EventCompleted || evt.Type == shelltool.EventKilled {
		summary = shelltool.SummarizeBackgroundEvent(evt, shelltool.BackgroundNoticeOptions{
			MaxChars:          outputLimit,
			SuccessOutputMode: outputMode,
		})
	}
	ownerSessionID := strings.TrimSpace(evt.Snapshot.OwnerSessionID)
	shouldNotify := ownerSessionID != "" && ownerSessionID == activeSessionID && !evt.NoticeSuppressed
	if shouldNotify && !evt.Snapshot.FinishedAt.IsZero() && evt.Snapshot.FinishedAt.Before(activeSince) {
		shouldNotify = false
	}
	activeEngine.HandleBackgroundShellUpdate(runtime.BackgroundShellEvent{
		Type:              string(evt.Type),
		ID:                evt.Snapshot.ID,
		State:             evt.Snapshot.State,
		Command:           evt.Snapshot.Command,
		Workdir:           evt.Snapshot.Workdir,
		LogPath:           evt.Snapshot.LogPath,
		NoticeText:        summary.DetailText,
		CompactText:       summary.OngoingText,
		Preview:           evt.Preview,
		Removed:           evt.Removed,
		ExitCode:          cloneIntPtr(evt.Snapshot.ExitCode),
		UserRequestedKill: evt.Snapshot.KillRequested,
		NoticeSuppressed:  evt.NoticeSuppressed,
	}, shouldNotify)
}

type runtimeWiringOptions struct {
	AskHandler func(req askquestion.Request) (askquestion.Response, error)
	OnEvent    func(evt runtime.Event)
	Headless   bool
	FastMode   *runtime.FastModeState
}

func newRuntimeWiring(store *session.Store, active config.Settings, enabledTools []tools.ID, workspaceRoot string, mgr *auth.Manager, logger *runLogger, opts runtimeWiringOptions) (*runtimeWiring, error) {
	return newRuntimeWiringWithBackground(store, active, enabledTools, workspaceRoot, mgr, logger, nil, opts)
}

func newRuntimeWiringWithBackground(store *session.Store, active config.Settings, enabledTools []tools.ID, workspaceRoot string, mgr *auth.Manager, logger *runLogger, background *shelltool.Manager, opts runtimeWiringOptions) (*runtimeWiring, error) {
	promptHistory, err := store.ReadPromptHistory()
	if err != nil {
		return nil, err
	}

	bells := newBellHooks(defaultTerminalNotifier(active.NotificationMethod), func() string {
		return store.Meta().Name
	})

	toolRegistry, askBroker, background, err := buildToolRegistry(
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
	askBridge := newAskBridge()
	askHandler := askBridge.Handle
	if opts.AskHandler != nil {
		askHandler = opts.AskHandler
	}
	askBroker.SetAskHandler(func(req askquestion.Request) (askquestion.Response, error) {
		bells.OnAsk(req)
		return askHandler(req)
	})

	modelHTTPClient := &http.Client{Timeout: time.Duration(active.Timeouts.ModelRequestSeconds) * time.Second}
	client, err := llm.NewProviderClient(llm.ProviderClientOptions{
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

	eventBridge := newRuntimeEventBridge(2048, func(total uint64, evt runtime.Event) {
		if total == 1 || total%100 == 0 {
			logger.Logf("runtime.event.drop count=%d kind=%s step_id=%s", total, evt.Kind, evt.StepID)
		}
	})
	providerCapsOverride, hasProviderCapsOverride := llm.ProviderCapabilitiesFromOverride(active.ProviderCapabilities)
	eng, err := runtime.New(store, client, toolRegistry, runtime.Config{
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
		AutoCompactTokenLimit:         active.ContextCompactionThresholdTokens,
		ContextWindowTokens:           active.ModelContextWindow,
		EffectiveContextWindowPercent: 95,
		LocalCompactionCarryoverLimit: 20_000,
		CompactionMode:                string(active.CompactionMode),
		AutoCompactionEnabled:         boolRef(true),
		HeadlessMode:                  opts.Headless,
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
			logger.Logf("%s", formatRuntimeEvent(evt))
			bells.OnRuntimeEvent(evt)
			if opts.OnEvent != nil {
				opts.OnEvent(evt)
			}
			eventBridge.Publish(evt)
		},
	})
	if err != nil {
		return nil, err
	}
	return &runtimeWiring{
		engine:        eng,
		askBridge:     askBridge,
		eventBridge:   eventBridge,
		background:    background,
		promptHistory: append([]string(nil), promptHistory...),
	}, nil
}

func (w *runtimeWiring) Close() error {
	return nil
}

func boolRef(v bool) *bool {
	return &v
}

func cloneIntPtr(v *int) *int {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

func configSourceLines(src config.SourceReport) []string {
	keys := make([]string, 0, len(src.Sources))
	for k := range src.Sources {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("%s=%s", k, src.Sources[k]))
	}
	return lines
}

type localToolRuntimeContext struct {
	workspaceRoot                   string
	ownerSessionID                  string
	shellDefaultTimeout             time.Duration
	shellOutputMaxChars             int
	allowNonCwdEdits                bool
	supportsVision                  bool
	registryProvider                func() *tools.Registry
	askQuestionBroker               *askquestion.Broker
	backgroundShellManager          *shelltool.Manager
	outsideWorkspaceEditApprover    patchtool.OutsideWorkspaceApprover
	outsideWorkspaceReadApprover    patchtool.OutsideWorkspaceApprover
	viewImageOutsideWorkspaceLogger readimagetool.OutsideWorkspaceAuditLogger
}

func buildLocalRuntimeHandler(def tools.Definition, ctx localToolRuntimeContext) (tools.Handler, error) {
	switch def.LocalRuntimeBuilder() {
	case tools.LocalRuntimeBuilderShell:
		return shelltool.New(
			ctx.workspaceRoot,
			ctx.shellOutputMaxChars,
			shelltool.WithDefaultTimeout(ctx.shellDefaultTimeout),
		), nil
	case tools.LocalRuntimeBuilderExecCommand:
		if ctx.backgroundShellManager == nil {
			return nil, fmt.Errorf("exec_command background manager is unavailable")
		}
		return shelltool.NewExecCommandTool(
			ctx.workspaceRoot,
			ctx.shellOutputMaxChars,
			ctx.backgroundShellManager,
			ctx.ownerSessionID,
		), nil
	case tools.LocalRuntimeBuilderWriteStdin:
		if ctx.backgroundShellManager == nil {
			return nil, fmt.Errorf("write_stdin background manager is unavailable")
		}
		return shelltool.NewWriteStdinTool(ctx.shellOutputMaxChars, ctx.backgroundShellManager), nil
	case tools.LocalRuntimeBuilderPatch:
		if ctx.outsideWorkspaceEditApprover == nil {
			return nil, fmt.Errorf("patch outside-workspace approver is unavailable")
		}
		return patchtool.New(
			ctx.workspaceRoot,
			true,
			patchtool.WithAllowOutsideWorkspace(ctx.allowNonCwdEdits),
			patchtool.WithOutsideWorkspaceApprover(ctx.outsideWorkspaceEditApprover),
		)
	case tools.LocalRuntimeBuilderAskQuestion:
		if ctx.askQuestionBroker == nil {
			return nil, fmt.Errorf("ask_question broker is unavailable")
		}
		return askquestion.NewTool(ctx.askQuestionBroker), nil
	case tools.LocalRuntimeBuilderViewImage:
		if ctx.outsideWorkspaceReadApprover == nil {
			return nil, fmt.Errorf("view_image outside-workspace approver is unavailable")
		}
		opts := []readimagetool.Option{
			readimagetool.WithAllowOutsideWorkspace(ctx.allowNonCwdEdits),
			readimagetool.WithOutsideWorkspaceApprover(ctx.outsideWorkspaceReadApprover),
		}
		if ctx.viewImageOutsideWorkspaceLogger != nil {
			opts = append(opts, readimagetool.WithOutsideWorkspaceAuditLogger(ctx.viewImageOutsideWorkspaceLogger))
		}
		return readimagetool.New(ctx.workspaceRoot, ctx.supportsVision, opts...)
	case tools.LocalRuntimeBuilderMultiToolUseParallel:
		if ctx.registryProvider == nil {
			return nil, fmt.Errorf("multi_tool_use_parallel registry provider is unavailable")
		}
		return multitooluseparallel.New(ctx.registryProvider), nil
	default:
		return nil, fmt.Errorf("unsupported local runtime builder %q for tool %q", def.LocalRuntimeBuilder(), def.ID)
	}
}

func buildToolRegistry(workspaceRoot string, ownerSessionID string, enabled []tools.ID, shellDefaultTimeout time.Duration, minimumExecToBgTime time.Duration, shellOutputMaxChars int, allowNonCwdEdits bool, supportsVision bool, logger *runLogger, background *shelltool.Manager) (*tools.Registry, *askquestion.Broker, *shelltool.Manager, error) {
	broker := askquestion.NewBroker()
	if background == nil {
		var err error
		background, err = shelltool.NewManager(shelltool.WithMinimumExecToBgTime(minimumExecToBgTime))
		if err != nil {
			return nil, nil, nil, err
		}
	}
	background.SetMinimumExecToBgTime(minimumExecToBgTime)
	patchOutsideWorkspaceApprover := newOutsideWorkspaceApprover(broker, "editing")
	readOutsideWorkspaceApprover := newOutsideWorkspaceApprover(broker, "reading")
	ctx := localToolRuntimeContext{
		workspaceRoot:                workspaceRoot,
		ownerSessionID:               ownerSessionID,
		shellDefaultTimeout:          shellDefaultTimeout,
		shellOutputMaxChars:          shellOutputMaxChars,
		allowNonCwdEdits:             allowNonCwdEdits,
		supportsVision:               supportsVision,
		askQuestionBroker:            broker,
		backgroundShellManager:       background,
		outsideWorkspaceEditApprover: patchtool.OutsideWorkspaceApprover(patchOutsideWorkspaceApprover.Approve),
		outsideWorkspaceReadApprover: patchtool.OutsideWorkspaceApprover(readOutsideWorkspaceApprover.Approve),
		viewImageOutsideWorkspaceLogger: readimagetool.OutsideWorkspaceAuditLogger(func(entry readimagetool.OutsideWorkspaceAudit) {
			if logger == nil {
				return
			}
			logger.Logf(
				"tool.view_image.outside_workspace.approved requested=%q resolved=%q reason=%s",
				entry.RequestedPath,
				entry.ResolvedPath,
				entry.Reason,
			)
		}),
	}
	enabledSet := make(map[tools.ID]struct{}, len(enabled))
	for _, id := range enabled {
		enabledSet[id] = struct{}{}
	}
	handlers := make([]tools.Handler, 0, len(enabledSet))
	var registry *tools.Registry
	ctx.registryProvider = func() *tools.Registry { return registry }
	for _, id := range tools.CatalogIDs() {
		if _, ok := enabledSet[id]; !ok {
			continue
		}
		def, ok := tools.DefinitionFor(id)
		if !ok {
			return nil, nil, nil, fmt.Errorf("missing tool definition for %q", id)
		}
		if !def.AvailableInLocalRuntime() {
			continue
		}
		handler, err := buildLocalRuntimeHandler(def, ctx)
		if err != nil {
			return nil, nil, nil, err
		}
		handlers = append(handlers, handler)
		registry = tools.NewRegistry(handlers...)
	}
	if registry == nil {
		registry = tools.NewRegistry()
	}
	return registry, broker, background, nil
}
