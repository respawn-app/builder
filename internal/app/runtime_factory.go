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
	engine      *runtime.Engine
	askBridge   *askBridge
	eventBridge *runtimeEventBridge
	background  *shelltool.Manager
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
	bells := newBellHooks(defaultTerminalNotifier(active.NotificationMethod))

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
		Model:               active.Model,
		Auth:                mgr,
		HTTPClient:          modelHTTPClient,
		OpenAIBaseURL:       active.OpenAIBaseURL,
		Store:               active.Store,
		ContextWindowTokens: active.ModelContextWindow,
	})
	if err != nil {
		return nil, err
	}

	newReviewerClient := func() (llm.Client, error) {
		reviewerHTTPClient := &http.Client{Timeout: time.Duration(active.Reviewer.TimeoutSeconds) * time.Second}
		return llm.NewProviderClient(llm.ProviderClientOptions{
			Model:               active.Reviewer.Model,
			Auth:                mgr,
			HTTPClient:          reviewerHTTPClient,
			OpenAIBaseURL:       active.OpenAIBaseURL,
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
			Frequency:      active.Reviewer.Frequency,
			Model:          active.Reviewer.Model,
			ThinkingLevel:  active.Reviewer.ThinkingLevel,
			MaxSuggestions: active.Reviewer.MaxSuggestions,
			Client:         reviewerClient,
			ClientFactory:  newReviewerClient,
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
		engine:      eng,
		askBridge:   askBridge,
		eventBridge: eventBridge,
		background:  background,
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

func buildToolRegistry(workspaceRoot string, ownerSessionID string, enabled []tools.ID, shellDefaultTimeout time.Duration, minimumExecToBgTime time.Duration, shellOutputMaxChars int, allowNonCwdEdits bool, supportsViewImage bool, logger *runLogger, background *shelltool.Manager) (*tools.Registry, *askquestion.Broker, *shelltool.Manager, error) {
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
	patch, err := patchtool.New(
		workspaceRoot,
		true,
		patchtool.WithAllowOutsideWorkspace(allowNonCwdEdits),
		patchtool.WithOutsideWorkspaceApprover(patchOutsideWorkspaceApprover.Approve),
	)
	if err != nil {
		return nil, nil, nil, err
	}
	viewImage, err := readimagetool.New(
		workspaceRoot,
		supportsViewImage,
		readimagetool.WithAllowOutsideWorkspace(allowNonCwdEdits),
		readimagetool.WithOutsideWorkspaceApprover(readOutsideWorkspaceApprover.Approve),
		readimagetool.WithOutsideWorkspaceAuditLogger(func(entry readimagetool.OutsideWorkspaceAudit) {
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
	)
	if err != nil {
		return nil, nil, nil, err
	}
	var registry *tools.Registry
	parallel := multitooluseparallel.New(func() *tools.Registry { return registry })

	factories := map[tools.ID]func() tools.Handler{
		tools.ToolShell: func() tools.Handler {
			return shelltool.New(workspaceRoot, shellOutputMaxChars, shelltool.WithDefaultTimeout(shellDefaultTimeout))
		},
		tools.ToolExecCommand: func() tools.Handler {
			return shelltool.NewExecCommandTool(workspaceRoot, shellOutputMaxChars, background, ownerSessionID)
		},
		tools.ToolWriteStdin: func() tools.Handler {
			return shelltool.NewWriteStdinTool(shellOutputMaxChars, background)
		},
		tools.ToolViewImage: func() tools.Handler {
			return viewImage
		},
		tools.ToolPatch: func() tools.Handler {
			return patch
		},
		tools.ToolAskQuestion: func() tools.Handler {
			return askquestion.NewTool(broker)
		},
		tools.ToolMultiToolUseParallel: func() tools.Handler {
			return parallel
		},
	}
	enabledSet := map[tools.ID]bool{}
	for _, id := range enabled {
		enabledSet[id] = true
	}
	handlers := make([]tools.Handler, 0, len(enabledSet))
	for _, id := range tools.CatalogIDs() {
		if !enabledSet[id] {
			continue
		}
		if !isLocalRuntimeTool(id) {
			continue
		}
		factory, ok := factories[id]
		if !ok {
			return nil, nil, nil, fmt.Errorf("missing runtime tool factory for %q", id)
		}
		handlers = append(handlers, factory())
	}
	registry = tools.NewRegistry(handlers...)
	return registry, broker, background, nil
}

func isLocalRuntimeTool(id tools.ID) bool {
	switch id {
	case tools.ToolShell, tools.ToolExecCommand, tools.ToolWriteStdin, tools.ToolViewImage, tools.ToolPatch, tools.ToolAskQuestion, tools.ToolMultiToolUseParallel:
		return true
	default:
		return false
	}
}
