package app

import (
	"errors"
	"io"
	"strings"

	"builder/server/launch"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/config"
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
	server          embeddedServer
	pickSession     sessionPickerRunner
}

func newBootstrapLaunchPlanner(persistenceRoot string) *launchPlanner {
	return &launchPlanner{persistenceRoot: strings.TrimSpace(persistenceRoot)}
}

func newSessionLaunchPlanner(server embeddedServer) *launchPlanner {
	return &launchPlanner{
		server: server,
		pickSession: func(summaries []session.Summary, theme string, alternateScreenPolicy config.TUIAlternateScreenPolicy) (sessionPickerResult, error) {
			return runSessionPicker(summaries, theme, alternateScreenPolicy)
		},
	}
}

func (p *launchPlanner) PlanBootstrap(opts Options) (bootstrapLaunchPlan, error) {
	plan, err := launch.ResolveBootstrapPlan(p.persistenceRoot, launch.BootstrapRequest{
		WorkspaceRoot:         requestedWorkspaceRootValue(opts.WorkspaceRoot),
		WorkspaceRootExplicit: opts.WorkspaceRootExplicit,
		SessionID:             strings.TrimSpace(opts.SessionID),
		OpenAIBaseURL:         strings.TrimSpace(opts.OpenAIBaseURL),
		OpenAIBaseURLExplicit: opts.OpenAIBaseURLExplicit,
	})
	if err != nil {
		return bootstrapLaunchPlan{}, err
	}
	return bootstrapLaunchPlan(plan), nil
}

func requestedWorkspaceRootValue(workspaceRoot string) string {
	if strings.TrimSpace(workspaceRoot) == "" {
		return "."
	}
	return workspaceRoot
}

func (p *launchPlanner) PlanSession(req sessionLaunchRequest) (sessionLaunchPlan, error) {
	if p == nil || p.server == nil {
		return sessionLaunchPlan{}, errors.New("launch planner bootstrap is required")
	}
	cfg := p.server.Config()
	planner := launch.Planner{
		Config:       cfg,
		ContainerDir: p.server.ContainerDir(),
		PickSession: func(summaries []session.Summary) (launch.SessionSelection, error) {
			runPicker := p.pickSession
			if runPicker == nil {
				runPicker = func(summaries []session.Summary, theme string, alternateScreenPolicy config.TUIAlternateScreenPolicy) (sessionPickerResult, error) {
					return runSessionPicker(summaries, theme, alternateScreenPolicy)
				}
			}
			picked, err := runPicker(summaries, cfg.Settings.Theme, cfg.Settings.TUIAlternateScreen)
			if err != nil {
				return launch.SessionSelection{}, err
			}
			return launch.SessionSelection{Session: picked.Session, CreateNew: picked.CreateNew, Canceled: picked.Canceled}, nil
		},
	}
	serverPlan, err := planner.PlanSession(launch.SessionRequest{
		Mode:              launch.Mode(req.Mode),
		SelectedSessionID: req.SelectedSessionID,
		ForceNewSession:   req.ForceNewSession,
		ParentSessionID:   req.ParentSessionID,
	})
	if err != nil {
		return sessionLaunchPlan{}, err
	}
	return sessionLaunchPlan{
		Mode:                req.Mode,
		Store:               serverPlan.Store,
		ActiveSettings:      serverPlan.ActiveSettings,
		EnabledTools:        serverPlan.EnabledTools,
		ConfiguredModelName: serverPlan.ConfiguredModelName,
		SessionName:         serverPlan.SessionName,
		ModelContractLocked: serverPlan.ModelContractLocked,
		StatusConfig: uiStatusConfig{
			WorkspaceRoot:   cfg.WorkspaceRoot,
			PersistenceRoot: cfg.PersistenceRoot,
			Settings:        serverPlan.ActiveSettings,
			Source:          serverPlan.Source,
			AuthManager:     p.server.AuthManager(),
			AuthStatePath:   config.GlobalAuthConfigPath(cfg),
		},
		WorkspaceRoot: serverPlan.WorkspaceRoot,
		Source:        serverPlan.Source,
	}, nil
}

func (p *launchPlanner) PrepareRuntime(plan sessionLaunchPlan, diagnosticWriter io.Writer, startLogLine string, opts runtimeWiringOptions) (*runtimeLaunchPlan, error) {
	if p == nil || p.server == nil {
		return nil, errors.New("launch planner bootstrap is required")
	}
	logger, err := newRunLogger(plan.Store.Dir(), func(diag runLoggerDiagnostic) {
		reportRunLoggerDiagnostic(diagnosticWriter, diag)
	})
	if err != nil {
		return nil, err
	}
	logLaunchPlanStart(logger, plan, startLogLine)
	wiring, err := newRuntimeWiringWithBackground(plan.Store, plan.ActiveSettings, plan.EnabledTools, plan.WorkspaceRoot, p.server.AuthManager(), logger, p.server.Background(), opts)
	if err != nil {
		_ = logger.Close()
		return nil, err
	}
	if router := p.server.BackgroundRouter(); router != nil {
		router.SetActiveSession(plan.Store.Meta().SessionID, wiring.engine)
	}
	return &runtimeLaunchPlan{
		Logger: logger,
		Wiring: wiring,
		close: func() {
			if router := p.server.BackgroundRouter(); router != nil {
				router.ClearActiveSession(plan.Store.Meta().SessionID)
			}
			_ = wiring.Close()
			_ = logger.Close()
		},
	}, nil
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
