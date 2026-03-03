package app

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
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
}

type runtimeWiringOptions struct {
	AskHandler func(req askquestion.Request) (string, error)
	OnEvent    func(evt runtime.Event)
}

func newRuntimeWiring(store *session.Store, active config.Settings, enabledTools []tools.ID, workspaceRoot string, mgr *auth.Manager, logger *runLogger, opts runtimeWiringOptions) (*runtimeWiring, error) {
	bells := newBellHooks(defaultTerminalNotifier(active.NotificationMethod))

	toolRegistry, askBroker, err := buildToolRegistry(
		workspaceRoot,
		enabledTools,
		time.Duration(active.Timeouts.ShellDefaultSeconds)*time.Second,
		active.ShellOutputMaxChars,
		active.AllowNonCwdEdits,
		llm.SupportsVisionInputsModel(active.Model),
	)
	if err != nil {
		return nil, err
	}
	askBridge := newAskBridge()
	askHandler := askBridge.Handle
	if opts.AskHandler != nil {
		askHandler = opts.AskHandler
	}
	askBroker.SetAskHandler(func(req askquestion.Request) (string, error) {
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
	eng, err := runtime.New(store, client, toolRegistry, runtime.Config{
		Model:                         active.Model,
		Temperature:                   1,
		MaxTokens:                     0,
		ThinkingLevel:                 active.ThinkingLevel,
		WebSearchMode:                 active.WebSearch,
		EnabledTools:                  enabledTools,
		AutoCompactTokenLimit:         active.ContextCompactionThresholdTokens,
		ContextWindowTokens:           active.ModelContextWindow,
		EffectiveContextWindowPercent: 95,
		LocalCompactionCarryoverLimit: 20_000,
		UseNativeCompaction:           boolRef(active.UseNativeCompaction),
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
	}, nil
}

func boolRef(v bool) *bool {
	return &v
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

func buildToolRegistry(workspaceRoot string, enabled []tools.ID, shellDefaultTimeout time.Duration, shellOutputMaxChars int, allowNonCwdEdits bool, supportsViewImage bool) (*tools.Registry, *askquestion.Broker, error) {
	broker := askquestion.NewBroker()
	outsideWorkspaceApprover := newPatchOutsideWorkspaceApprover(broker)
	patch, err := patchtool.New(
		workspaceRoot,
		true,
		patchtool.WithAllowOutsideWorkspace(allowNonCwdEdits),
		patchtool.WithOutsideWorkspaceApprover(outsideWorkspaceApprover.Approve),
	)
	if err != nil {
		return nil, nil, err
	}
	var registry *tools.Registry
	parallel := multitooluseparallel.New(func() *tools.Registry { return registry })

	factories := map[tools.ID]func() tools.Handler{
		tools.ToolShell: func() tools.Handler {
			return shelltool.New(workspaceRoot, shellOutputMaxChars, shelltool.WithDefaultTimeout(shellDefaultTimeout))
		},
		tools.ToolViewImage: func() tools.Handler {
			return readimagetool.New(workspaceRoot, supportsViewImage)
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
			return nil, nil, fmt.Errorf("missing runtime tool factory for %q", id)
		}
		handlers = append(handlers, factory())
	}
	registry = tools.NewRegistry(handlers...)
	return registry, broker, nil
}

func isLocalRuntimeTool(id tools.ID) bool {
	switch id {
	case tools.ToolShell, tools.ToolViewImage, tools.ToolPatch, tools.ToolAskQuestion, tools.ToolMultiToolUseParallel:
		return true
	default:
		return false
	}
}
