package app

import (
	"context"
	"errors"
	"io"
	"strings"

	"builder/server/launch"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/clientui"
	"builder/shared/config"
	"builder/shared/serverapi"

	"github.com/google/uuid"
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
	SessionID           string
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
	if p == nil || p.server == nil || p.server.SessionLaunchClient() == nil {
		return sessionLaunchPlan{}, errors.New("launch planner bootstrap is required")
	}
	resolved, err := p.resolvePlanRequest(req)
	if err != nil {
		return sessionLaunchPlan{}, err
	}
	resp, err := p.server.SessionLaunchClient().PlanSession(context.Background(), resolved)
	if err != nil {
		return sessionLaunchPlan{}, err
	}
	enabledTools := make([]tools.ID, 0, len(resp.Plan.EnabledToolIDs))
	for _, raw := range resp.Plan.EnabledToolIDs {
		if id, ok := tools.ParseID(raw); ok {
			enabledTools = append(enabledTools, id)
		}
	}
	cfg := p.server.Config()
	return sessionLaunchPlan{
		Mode:                req.Mode,
		SessionID:           resp.Plan.SessionID,
		ActiveSettings:      resp.Plan.ActiveSettings,
		EnabledTools:        enabledTools,
		ConfiguredModelName: resp.Plan.ConfiguredModelName,
		SessionName:         resp.Plan.SessionName,
		ModelContractLocked: resp.Plan.ModelContractLocked,
		StatusConfig: uiStatusConfig{
			WorkspaceRoot:   resp.Plan.WorkspaceRoot,
			PersistenceRoot: cfg.PersistenceRoot,
			Settings:        resp.Plan.ActiveSettings,
			Source:          resp.Plan.Source,
			AuthManager:     p.server.AuthManager(),
			AuthStatePath:   config.GlobalAuthConfigPath(cfg),
		},
		WorkspaceRoot: resp.Plan.WorkspaceRoot,
		Source:        resp.Plan.Source,
	}, nil
}

func (p *launchPlanner) PrepareRuntime(plan sessionLaunchPlan, diagnosticWriter io.Writer, startLogLine string) (*runtimeLaunchPlan, error) {
	if p == nil || p.server == nil {
		return nil, io.ErrClosedPipe
	}
	return p.server.PrepareRuntime(plan, diagnosticWriter, startLogLine)
}

func (p *launchPlanner) resolvePlanRequest(req sessionLaunchRequest) (serverapi.SessionPlanRequest, error) {
	resolved := serverapi.SessionPlanRequest{
		ClientRequestID:   uuid.NewString(),
		Mode:              serverapi.SessionLaunchMode(req.Mode),
		SelectedSessionID: strings.TrimSpace(req.SelectedSessionID),
		ForceNewSession:   req.ForceNewSession,
		ParentSessionID:   strings.TrimSpace(req.ParentSessionID),
	}
	if resolved.Mode == serverapi.SessionLaunchModeHeadless && resolved.SelectedSessionID == "" {
		resolved.ForceNewSession = true
		return resolved, nil
	}
	if resolved.SelectedSessionID != "" || resolved.ForceNewSession {
		return resolved, nil
	}
	summaries, err := p.listSessionSummaries()
	if err != nil {
		return serverapi.SessionPlanRequest{}, err
	}
	if len(summaries) == 0 {
		resolved.ForceNewSession = true
		return resolved, nil
	}
	if p.pickSession == nil {
		return serverapi.SessionPlanRequest{}, errors.New("session picker is required")
	}
	cfg := p.server.Config()
	picked, err := p.pickSession(summaries, cfg.Settings.Theme, cfg.Settings.TUIAlternateScreen)
	if err != nil {
		return serverapi.SessionPlanRequest{}, err
	}
	if picked.Canceled {
		return serverapi.SessionPlanRequest{}, errors.New("startup canceled by user")
	}
	if picked.CreateNew {
		resolved.ForceNewSession = true
		return resolved, nil
	}
	if picked.Session == nil {
		return serverapi.SessionPlanRequest{}, errors.New("no session selected")
	}
	resolved.SelectedSessionID = picked.Session.SessionID
	return resolved, nil
}

func (p *launchPlanner) listSessionSummaries() ([]session.Summary, error) {
	if p == nil || p.server == nil || p.server.ProjectViewClient() == nil || strings.TrimSpace(p.server.ProjectID()) == "" {
		return nil, nil
	}
	resp, err := p.server.ProjectViewClient().GetProjectOverview(context.Background(), serverapi.ProjectGetOverviewRequest{ProjectID: p.server.ProjectID()})
	if err != nil {
		return nil, err
	}
	return sessionSummariesFromProjectView(resp.Overview.Sessions), nil
}

func sessionSummariesFromProjectView(items []clientui.SessionSummary) []session.Summary {
	out := make([]session.Summary, 0, len(items))
	for _, item := range items {
		out = append(out, session.Summary{
			SessionID:          item.SessionID,
			Name:               item.Name,
			FirstPromptPreview: item.FirstPromptPreview,
			UpdatedAt:          item.UpdatedAt,
		})
	}
	return out
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
