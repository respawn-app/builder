package launch

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"

	"builder/server/llm"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/config"
	"builder/shared/serverapi"
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
	EnabledTools        []tools.ID
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

func (p Planner) openStore(req SessionRequest) (*session.Store, error) {
	if strings.TrimSpace(p.Config.PersistenceRoot) == "" {
		return nil, errors.New("launch planner persistence root is required")
	}
	if strings.TrimSpace(p.ContainerDir) == "" {
		return nil, errors.New("launch planner container dir is required")
	}
	if strings.TrimSpace(req.SelectedSessionID) != "" {
		return session.OpenByID(p.Config.PersistenceRoot, req.SelectedSessionID)
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
	return session.OpenByID(p.Config.PersistenceRoot, picked.Session.SessionID)
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
	created, err := session.NewLazy(p.ContainerDir, containerName, p.Config.WorkspaceRoot)
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

func ActiveToolIDs(settings config.Settings, source config.SourceReport, locked *session.LockedContract) []tools.ID {
	if locked != nil {
		ids := make([]tools.ID, 0, len(locked.EnabledTools))
		for _, raw := range locked.EnabledTools {
			if id, ok := tools.ParseID(raw); ok {
				ids = append(ids, id)
			}
		}
		return DedupeSortToolIDs(ids)
	}
	ids := config.EnabledToolIDs(settings)
	sourceKind := strings.TrimSpace(source.Sources["tools."+string(tools.ToolMultiToolUseParallel)])
	if sourceKind != "" && sourceKind != "default" {
		return DedupeSortToolIDs(ids)
	}
	enabled := map[tools.ID]bool{}
	for _, id := range ids {
		enabled[id] = true
	}
	if llm.SupportsMultiToolUseParallelModel(settings.Model) {
		enabled[tools.ToolMultiToolUseParallel] = true
	} else {
		delete(enabled, tools.ToolMultiToolUseParallel)
	}
	resolved := make([]tools.ID, 0, len(enabled))
	for id := range enabled {
		resolved = append(resolved, id)
	}
	return DedupeSortToolIDs(resolved)
}

func DedupeSortToolIDs(ids []tools.ID) []tools.ID {
	seen := map[tools.ID]bool{}
	out := make([]tools.ID, 0, len(ids))
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
