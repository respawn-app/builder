package runtimewire

import (
	"net/http"
	"strings"
	"time"

	"builder/server/auth"
	"builder/server/llm"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/tools"
	askquestion "builder/server/tools/askquestion"
	shelltool "builder/server/tools/shell"
	"builder/shared/config"
)

type RuntimeWiring struct {
	Engine        *runtime.Engine
	AskBroker     *askquestion.Broker
	EventBridge   *EventBridge
	Background    *shelltool.Manager
	PromptHistory []string
}

type RuntimeWiringOptions struct {
	OnEvent  func(evt runtime.Event)
	Headless bool
	FastMode *runtime.FastModeState
}

func NewRuntimeWiring(store *session.Store, active config.Settings, enabledTools []tools.ID, workspaceRoot string, mgr *auth.Manager, logger Logger, opts RuntimeWiringOptions) (*RuntimeWiring, error) {
	return NewRuntimeWiringWithBackground(store, active, enabledTools, workspaceRoot, mgr, logger, nil, opts)
}

func NewRuntimeWiringWithBackground(store *session.Store, active config.Settings, enabledTools []tools.ID, workspaceRoot string, mgr *auth.Manager, logger Logger, background *shelltool.Manager, opts RuntimeWiringOptions) (*RuntimeWiring, error) {
	promptHistory, err := store.ReadPromptHistory()
	if err != nil {
		return nil, err
	}

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

	eventBridge := NewEventBridge(2048, func(total uint64, evt runtime.Event) {
		if logger == nil {
			return
		}
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
		DisabledSkills:                config.DisabledSkillToggles(active),
		AutoCompactTokenLimit:         active.ContextCompactionThresholdTokens,
		PreSubmitCompactionLeadTokens: active.PreSubmitCompactionLeadTokens,
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
			if opts.OnEvent != nil {
				opts.OnEvent(evt)
			}
			eventBridge.Publish(evt)
		},
	})
	if err != nil {
		return nil, err
	}
	return &RuntimeWiring{
		Engine:        eng,
		AskBroker:     askBroker,
		EventBridge:   eventBridge,
		Background:    background,
		PromptHistory: append([]string(nil), promptHistory...),
	}, nil
}

func boolRef(v bool) *bool { return &v }
