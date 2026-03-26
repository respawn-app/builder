package app

import (
	"errors"
	"io"
	"path/filepath"
	"strings"

	"builder/internal/config"
	"builder/internal/session"
	"builder/internal/tools"
)

type launchMode string

const (
	launchModeInteractive launchMode = "interactive"
	launchModeHeadless    launchMode = "headless"
)

type bootstrapLaunchPlan struct {
	WorkspaceRoot    string
	OpenAIBaseURL    string
	UseOpenAIBaseURL bool
}

type sessionLaunchRequest struct {
	Mode              launchMode
	SelectedSessionID string
	ForceNewSession   bool
	ParentSessionID   string
}

type sessionLaunchPlan struct {
	Mode                launchMode
	Store               *session.Store
	ActiveSettings      config.Settings
	EnabledTools        []tools.ID
	ConfiguredModelName string
	SessionName         string
	ModelContractLocked bool
	StatusConfig        uiStatusConfig
	WorkspaceRoot       string
	Source              config.SourceReport
}

type runtimeLaunchPlan struct {
	Logger *runLogger
	Wiring *runtimeWiring
	close  func()
}

func (p *runtimeLaunchPlan) Close() {
	if p == nil || p.close == nil {
		return
	}
	p.close()
}

type sessionPickerRunner func([]session.Summary, string, config.TUIAlternateScreenPolicy) (sessionPickerResult, error)

type launchPlanner struct {
	persistenceRoot string
	boot            *appBootstrap
	pickSession     sessionPickerRunner
}

func newBootstrapLaunchPlanner(persistenceRoot string) *launchPlanner {
	return &launchPlanner{persistenceRoot: strings.TrimSpace(persistenceRoot)}
}

func newSessionLaunchPlanner(boot *appBootstrap) *launchPlanner {
	return &launchPlanner{
		boot: boot,
		pickSession: func(summaries []session.Summary, theme string, alternateScreenPolicy config.TUIAlternateScreenPolicy) (sessionPickerResult, error) {
			return runSessionPicker(summaries, theme, alternateScreenPolicy)
		},
	}
}

func (p *launchPlanner) PlanBootstrap(opts Options) (bootstrapLaunchPlan, error) {
	plan := bootstrapLaunchPlan{
		WorkspaceRoot:    requestedWorkspaceRoot(opts),
		OpenAIBaseURL:    strings.TrimSpace(opts.OpenAIBaseURL),
		UseOpenAIBaseURL: opts.OpenAIBaseURLExplicit,
	}
	if strings.TrimSpace(opts.SessionID) == "" {
		return plan, nil
	}
	if strings.TrimSpace(p.persistenceRoot) == "" {
		return bootstrapLaunchPlan{}, errors.New("launch planner persistence root is required")
	}
	store, err := session.OpenByID(p.persistenceRoot, opts.SessionID)
	if err != nil {
		return bootstrapLaunchPlan{}, err
	}
	meta := store.Meta()
	if !opts.WorkspaceRootExplicit && strings.TrimSpace(meta.WorkspaceRoot) != "" {
		plan.WorkspaceRoot = strings.TrimSpace(meta.WorkspaceRoot)
	}
	if opts.OpenAIBaseURLExplicit {
		return plan, nil
	}
	if meta.Continuation != nil && strings.TrimSpace(meta.Continuation.OpenAIBaseURL) != "" {
		plan.OpenAIBaseURL = strings.TrimSpace(meta.Continuation.OpenAIBaseURL)
		plan.UseOpenAIBaseURL = true
	}
	return plan, nil
}

func (p *launchPlanner) PlanSession(req sessionLaunchRequest) (sessionLaunchPlan, error) {
	if p == nil || p.boot == nil {
		return sessionLaunchPlan{}, errors.New("launch planner bootstrap is required")
	}
	store, err := p.openStore(req)
	if err != nil {
		return sessionLaunchPlan{}, err
	}
	if req.Mode == launchModeHeadless {
		if err := ensureSubagentSessionName(store); err != nil {
			return sessionLaunchPlan{}, err
		}
	}
	active := effectiveSettings(p.boot.cfg.Settings, store.Meta().Locked)
	if err := store.SetContinuationContext(session.ContinuationContext{OpenAIBaseURL: active.OpenAIBaseURL}); err != nil {
		return sessionLaunchPlan{}, err
	}
	return sessionLaunchPlan{
		Mode:                req.Mode,
		Store:               store,
		ActiveSettings:      active,
		EnabledTools:        activeToolIDs(active, p.boot.cfg.Source, store.Meta().Locked),
		ConfiguredModelName: p.boot.cfg.Settings.Model,
		SessionName:         store.Meta().Name,
		ModelContractLocked: store.Meta().Locked != nil,
		StatusConfig: uiStatusConfig{
			WorkspaceRoot:   p.boot.cfg.WorkspaceRoot,
			PersistenceRoot: p.boot.cfg.PersistenceRoot,
			Settings:        active,
			Source:          p.boot.cfg.Source,
			AuthManager:     p.boot.authManager,
			AuthStatePath:   config.GlobalAuthConfigPath(p.boot.cfg),
		},
		WorkspaceRoot: p.boot.cfg.WorkspaceRoot,
		Source:        p.boot.cfg.Source,
	}, nil
}

func (p *launchPlanner) PrepareRuntime(plan sessionLaunchPlan, diagnosticWriter io.Writer, startLogLine string, opts runtimeWiringOptions) (*runtimeLaunchPlan, error) {
	if p == nil || p.boot == nil {
		return nil, errors.New("launch planner bootstrap is required")
	}
	logger, err := newRunLogger(plan.Store.Dir(), func(diag runLoggerDiagnostic) {
		reportRunLoggerDiagnostic(diagnosticWriter, diag)
	})
	if err != nil {
		return nil, err
	}
	logLaunchPlanStart(logger, plan, startLogLine)
	wiring, err := newRuntimeWiringWithBackground(plan.Store, plan.ActiveSettings, plan.EnabledTools, plan.WorkspaceRoot, p.boot.authManager, logger, p.boot.background, opts)
	if err != nil {
		_ = logger.Close()
		return nil, err
	}
	if p.boot.backgroundRouter != nil {
		p.boot.backgroundRouter.SetActiveSession(plan.Store.Meta().SessionID, wiring.engine)
	}
	return &runtimeLaunchPlan{
		Logger: logger,
		Wiring: wiring,
		close: func() {
			if p.boot.backgroundRouter != nil {
				p.boot.backgroundRouter.ClearActiveSession(plan.Store.Meta().SessionID)
			}
			_ = wiring.Close()
			_ = logger.Close()
		},
	}, nil
}

func (p *launchPlanner) openStore(req sessionLaunchRequest) (*session.Store, error) {
	if p == nil || p.boot == nil {
		return nil, errors.New("launch planner bootstrap is required")
	}
	if strings.TrimSpace(req.SelectedSessionID) != "" {
		return session.OpenByID(p.boot.cfg.PersistenceRoot, req.SelectedSessionID)
	}
	if req.ForceNewSession || req.Mode == launchModeHeadless {
		return p.createSession(req.ParentSessionID)
	}
	summaries, err := session.ListSessions(p.boot.containerDir)
	if err != nil {
		return nil, err
	}
	if len(summaries) == 0 {
		return p.createSession(req.ParentSessionID)
	}
	runPicker := p.pickSession
	if runPicker == nil {
		runPicker = func(summaries []session.Summary, theme string, alternateScreenPolicy config.TUIAlternateScreenPolicy) (sessionPickerResult, error) {
			return runSessionPicker(summaries, theme, alternateScreenPolicy)
		}
	}
	picked, err := runPicker(summaries, p.boot.cfg.Settings.Theme, p.boot.cfg.Settings.TUIAlternateScreen)
	if err != nil {
		return nil, err
	}
	if picked.Canceled {
		return nil, errors.New("startup canceled by user")
	}
	if picked.CreateNew {
		return p.createSession(req.ParentSessionID)
	}
	if picked.Session == nil {
		return nil, errors.New("no session selected")
	}
	return session.Open(picked.Session.Path)
}

func (p *launchPlanner) createSession(parentSessionID string) (*session.Store, error) {
	if p == nil || p.boot == nil {
		return nil, errors.New("launch planner bootstrap is required")
	}
	containerName := filepath.Base(p.boot.containerDir)
	created, err := session.NewLazy(p.boot.containerDir, containerName, p.boot.cfg.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(parentSessionID) != "" {
		if err := created.SetParentSessionID(parentSessionID); err != nil {
			return nil, err
		}
	}
	return created, nil
}

func logLaunchPlanStart(logger *runLogger, plan sessionLaunchPlan, startLogLine string) {
	logger.Logf("%s", startLogLine)
	if plan.Mode == launchModeInteractive && plan.ActiveSettings.TUIAlternateScreen == config.TUIAlternateScreenAlways {
		logger.Logf("ui.scrollback.native keeps main UI startup in normal buffer even with tui_alternate_screen=always")
	}
	logger.Logf("config.settings path=%s created=%t", plan.Source.SettingsPath, plan.Source.CreatedDefaultConfig)
	for _, line := range configSourceLines(plan.Source) {
		logger.Logf("config.source %s", line)
	}
}
