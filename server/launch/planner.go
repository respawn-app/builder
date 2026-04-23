package launch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"builder/server/auth"
	"builder/server/session"
	"builder/server/sessionpath"
	"builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/config"
	"builder/shared/serverapi"
	"builder/shared/toolspec"
)

const (
	ModeInteractive Mode = "interactive"
	ModeHeadless    Mode = "headless"

	SubagentSessionSuffix = "subagent"
)

type Mode string

type Planner struct {
	Config       config.App
	ContainerDir string
	ProjectID    string
	ProjectViews client.ProjectViewClient
	PickSession  SessionPicker
	StoreOptions []session.StoreOption
}

type SessionPicker func([]session.Summary) (SessionSelection, error)

type SessionSelection struct {
	Session   *session.Summary
	CreateNew bool
	Canceled  bool
}

type SessionRequest struct {
	Mode              Mode
	SelectedSessionID string
	ForceNewSession   bool
	ParentSessionID   string
}

type SessionPlan struct {
	Store               *session.Store
	ActiveSettings      config.Settings
	EnabledTools        []toolspec.ID
	ConfiguredModelName string
	SessionName         string
	ModelContractLocked bool
	WorkspaceRoot       string
	Source              config.SourceReport
}

func (p Planner) PlanSession(req SessionRequest) (SessionPlan, error) {
	store, err := p.openStore(req)
	if err != nil {
		return SessionPlan{}, err
	}
	if req.Mode == ModeHeadless {
		if err := EnsureSubagentSessionName(store); err != nil {
			return SessionPlan{}, err
		}
	}
	meta := store.Meta()
	active := EffectiveSettings(p.Config.Settings, meta.Locked)
	if meta.Continuation != nil {
		if baseURL := strings.TrimSpace(meta.Continuation.OpenAIBaseURL); baseURL != "" {
			active.OpenAIBaseURL = baseURL
		}
	}
	if err := store.SetContinuationContext(session.ContinuationContext{OpenAIBaseURL: active.OpenAIBaseURL}); err != nil {
		return SessionPlan{}, err
	}
	return SessionPlan{
		Store:               store,
		ActiveSettings:      active,
		EnabledTools:        ActiveToolIDs(active, p.Config.Source, meta.Locked),
		ConfiguredModelName: p.Config.Settings.Model,
		SessionName:         meta.Name,
		ModelContractLocked: meta.Locked != nil,
		WorkspaceRoot:       p.Config.WorkspaceRoot,
		Source:              p.Config.Source,
	}, nil
}

func ApplyRunPromptOverrides(plan SessionPlan, overrides serverapi.RunPromptOverrides, authState auth.State) (SessionPlan, []string, error) {
	if !overrides.HasAny() {
		return plan, nil, nil
	}
	var warnings []string
	next := plan
	if trimmedRole := strings.TrimSpace(overrides.AgentRole); trimmedRole != "" && config.NormalizeSubagentRole(trimmedRole) == "" {
		return SessionPlan{}, nil, fmt.Errorf("invalid agent role %q", trimmedRole)
	}
	roleName := config.NormalizeSubagentRole(overrides.AgentRole)
	if roleName != "" {
		providerBase := cloneSettings(plan.ActiveSettings)
		if value := strings.TrimSpace(overrides.ProviderOverride); value != "" {
			providerBase.ProviderOverride = value
		}
		if value := strings.TrimSpace(overrides.OpenAIBaseURL); value != "" {
			providerBase.OpenAIBaseURL = value
		}
		resolved, warning, err := resolveSubagentSettings(plan.ActiveSettings, providerBase, plan.Source.Sources, roleName, authState, !plan.ModelContractLocked)
		if err != nil {
			return SessionPlan{}, nil, err
		}
		next.ActiveSettings = resolved
		if err := next.Store.SetContinuationContext(session.ContinuationContext{OpenAIBaseURL: next.ActiveSettings.OpenAIBaseURL}); err != nil {
			return SessionPlan{}, nil, err
		}
		if !plan.ModelContractLocked {
			next.ConfiguredModelName = resolved.Model
		}
		next.EnabledTools = ActiveToolIDs(next.ActiveSettings, next.Source, plan.Store.Meta().Locked)
		if strings.TrimSpace(warning) != "" {
			warnings = append(warnings, warning)
		}
	}
	if !overrides.HasConfigOverrides() {
		return next, warnings, nil
	}
	loaded, err := config.Load(plan.WorkspaceRoot, config.LoadOptions{
		Model:               strings.TrimSpace(overrides.Model),
		ProviderOverride:    strings.TrimSpace(overrides.ProviderOverride),
		ThinkingLevel:       strings.TrimSpace(overrides.ThinkingLevel),
		Theme:               strings.TrimSpace(overrides.Theme),
		ModelTimeoutSeconds: overrides.ModelTimeoutSeconds,
		Tools:               strings.TrimSpace(overrides.Tools),
		OpenAIBaseURL:       strings.TrimSpace(overrides.OpenAIBaseURL),
	})
	if err != nil {
		return SessionPlan{}, nil, err
	}
	locked := plan.Store.Meta().Locked
	mergedSource := mergeOverrideSources(plan.Source, loaded.Source)
	if strings.TrimSpace(overrides.Model) != "" && !next.ModelContractLocked {
		originalModel := strings.TrimSpace(next.ActiveSettings.Model)
		explicitSources := map[string]string{}
		for key, source := range mergedSource.Sources {
			if strings.TrimSpace(source) == "" || strings.TrimSpace(source) == "default" {
				continue
			}
			explicitSources[key] = source
		}
		next.ActiveSettings.Model = loaded.Settings.Model
		applyDerivedModelContextBudgetOverrides(&next.ActiveSettings, explicitSources, originalModel, true)
		next.ConfiguredModelName = loaded.Settings.Model
	}
	if strings.TrimSpace(overrides.ProviderOverride) != "" {
		next.ActiveSettings.ProviderOverride = loaded.Settings.ProviderOverride
	}
	if strings.TrimSpace(overrides.ThinkingLevel) != "" {
		next.ActiveSettings.ThinkingLevel = loaded.Settings.ThinkingLevel
	}
	if strings.TrimSpace(overrides.Theme) != "" {
		next.ActiveSettings.Theme = loaded.Settings.Theme
	}
	if overrides.ModelTimeoutSeconds > 0 {
		next.ActiveSettings.Timeouts.ModelRequestSeconds = loaded.Settings.Timeouts.ModelRequestSeconds
	}
	if locked == nil {
		if strings.TrimSpace(overrides.Tools) != "" {
			next.ActiveSettings.EnabledTools = cloneEnabledToolSet(loaded.Settings.EnabledTools)
		}
		if strings.TrimSpace(overrides.Tools) != "" || strings.TrimSpace(overrides.Model) != "" {
			next.EnabledTools = ActiveToolIDs(next.ActiveSettings, mergedSource, locked)
		}
	}
	if strings.TrimSpace(overrides.OpenAIBaseURL) != "" {
		next.ActiveSettings.OpenAIBaseURL = loaded.Settings.OpenAIBaseURL
		if err := next.Store.SetContinuationContext(session.ContinuationContext{OpenAIBaseURL: next.ActiveSettings.OpenAIBaseURL}); err != nil {
			return SessionPlan{}, nil, err
		}
	}
	next.Source = mergedSource
	return next, warnings, nil
}

func mergeOverrideSources(base config.SourceReport, override config.SourceReport) config.SourceReport {
	merged := base
	merged.SettingsPath = override.SettingsPath
	merged.SettingsFileExists = override.SettingsFileExists
	merged.CreatedDefaultConfig = override.CreatedDefaultConfig
	merged.Sources = make(map[string]string, len(base.Sources)+len(override.Sources))
	for key, value := range base.Sources {
		merged.Sources[key] = value
	}
	for key, value := range override.Sources {
		if strings.TrimSpace(value) == "cli" {
			merged.Sources[key] = value
		}
	}
	return merged
}

func (p Planner) openStore(req SessionRequest) (*session.Store, error) {
	if strings.TrimSpace(p.Config.PersistenceRoot) == "" {
		return nil, errors.New("launch planner persistence root is required")
	}
	if strings.TrimSpace(p.ContainerDir) == "" {
		return nil, errors.New("launch planner container dir is required")
	}
	if strings.TrimSpace(req.SelectedSessionID) != "" {
		return p.openScopedSession(req.SelectedSessionID)
	}
	if req.ForceNewSession || req.Mode == ModeHeadless {
		return p.createSession(req.ParentSessionID)
	}
	if p.ProjectViews != nil && strings.TrimSpace(p.ProjectID) != "" {
		overview, err := p.ProjectViews.GetProjectOverview(context.Background(), serverapi.ProjectGetOverviewRequest{ProjectID: p.ProjectID})
		if err != nil {
			return nil, err
		}
		summaries := sessionSummariesFromProjectView(overview.Overview.Sessions)
		return p.pickOrCreateSession(req, summaries)
	}
	summaries, err := session.ListSessions(p.ContainerDir)
	if err != nil {
		return nil, err
	}
	return p.pickOrCreateSession(req, summaries)
}

func (p Planner) pickOrCreateSession(req SessionRequest, summaries []session.Summary) (*session.Store, error) {
	if len(summaries) == 0 {
		return p.createSession(req.ParentSessionID)
	}
	if p.PickSession == nil {
		return nil, errors.New("session picker is required")
	}
	picked, err := p.PickSession(summaries)
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
	return p.openScopedSession(picked.Session.SessionID)
}

func (p Planner) openScopedSession(sessionID string) (*session.Store, error) {
	realSessionDir, err := sessionpath.ResolveScopedSessionDir(p.ContainerDir, sessionID)
	if err != nil {
		return nil, err
	}
	return session.Open(realSessionDir, p.StoreOptions...)
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

func (p Planner) createSession(parentSessionID string) (*session.Store, error) {
	containerName := filepath.Base(p.ContainerDir)
	created, err := session.NewLazy(p.ContainerDir, containerName, p.Config.WorkspaceRoot, p.StoreOptions...)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(parentSessionID) != "" {
		if err := created.SetParentSessionID(parentSessionID); err != nil {
			return nil, err
		}
	} else {
		if err := created.EnsureDurable(); err != nil {
			return nil, err
		}
	}
	return created, nil
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

func EffectiveSettings(base config.Settings, locked *session.LockedContract) config.Settings {
	out := base
	if locked == nil {
		return out
	}
	if strings.TrimSpace(locked.Model) != "" {
		out.Model = locked.Model
	}
	return out
}

func ActiveToolIDs(settings config.Settings, source config.SourceReport, locked *session.LockedContract) []toolspec.ID {
	if locked != nil {
		ids := make([]toolspec.ID, 0, len(locked.EnabledTools))
		for _, raw := range locked.EnabledTools {
			if id, ok := toolspec.ParseID(raw); ok {
				ids = append(ids, id)
			}
		}
		return DedupeSortToolIDs(ids)
	}
	return DedupeSortToolIDs(config.EnabledToolIDs(settings))
}

func cloneEnabledToolSet(in map[toolspec.ID]bool) map[toolspec.ID]bool {
	if len(in) == 0 {
		return map[toolspec.ID]bool{}
	}
	out := make(map[toolspec.ID]bool, len(in))
	for id, enabled := range in {
		out[id] = enabled
	}
	return out
}

func DedupeSortToolIDs(ids []toolspec.ID) []toolspec.ID {
	seen := map[toolspec.ID]bool{}
	out := make([]toolspec.ID, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
