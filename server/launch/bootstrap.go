package launch

import (
	"errors"
	"strings"

	"builder/server/metadata"
	"builder/server/session"
)

type BootstrapRequest struct {
	WorkspaceRoot         string
	WorkspaceRootExplicit bool
	SessionID             string
	OpenAIBaseURL         string
	OpenAIBaseURLExplicit bool
}

type BootstrapPlan struct {
	WorkspaceRoot    string
	OpenAIBaseURL    string
	UseOpenAIBaseURL bool
}

func ResolveBootstrapPlan(persistenceRoot string, req BootstrapRequest) (BootstrapPlan, error) {
	plan := BootstrapPlan{
		WorkspaceRoot:    strings.TrimSpace(req.WorkspaceRoot),
		OpenAIBaseURL:    strings.TrimSpace(req.OpenAIBaseURL),
		UseOpenAIBaseURL: req.OpenAIBaseURLExplicit,
	}
	if plan.WorkspaceRoot == "" {
		plan.WorkspaceRoot = "."
	}
	if strings.TrimSpace(req.SessionID) == "" {
		return plan, nil
	}
	if strings.TrimSpace(persistenceRoot) == "" {
		return BootstrapPlan{}, errors.New("launch planner persistence root is required")
	}
	store, err := session.OpenByID(persistenceRoot, req.SessionID)
	if err != nil {
		metadataStore, metadataErr := metadata.Open(persistenceRoot)
		if metadataErr == nil {
			defer func() { _ = metadataStore.Close() }()
			store, err = session.OpenByID(persistenceRoot, req.SessionID, metadataStore.AuthoritativeSessionStoreOptions()...)
		}
	}
	if err != nil {
		return BootstrapPlan{}, err
	}
	meta := store.Meta()
	if !req.WorkspaceRootExplicit && strings.TrimSpace(meta.WorkspaceRoot) != "" {
		plan.WorkspaceRoot = strings.TrimSpace(meta.WorkspaceRoot)
	}
	if req.OpenAIBaseURLExplicit {
		return plan, nil
	}
	if meta.Continuation != nil && strings.TrimSpace(meta.Continuation.OpenAIBaseURL) != "" {
		plan.OpenAIBaseURL = strings.TrimSpace(meta.Continuation.OpenAIBaseURL)
		plan.UseOpenAIBaseURL = true
	}
	return plan, nil
}
