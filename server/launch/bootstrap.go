package launch

import (
	"errors"
	"fmt"
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

func ResolveSessionAgentRole(persistenceRoot string, sessionID string) (string, error) {
	store, err := openSessionByID(persistenceRoot, sessionID)
	if err != nil {
		return "", err
	}
	meta := store.Meta()
	if meta.Continuation == nil {
		return "", nil
	}
	return strings.TrimSpace(meta.Continuation.AgentRole), nil
}

func ResolveBootstrapPlan(persistenceRoot string, req BootstrapRequest) (BootstrapPlan, error) {
	plan := BootstrapPlan{
		WorkspaceRoot:    strings.TrimSpace(req.WorkspaceRoot),
		OpenAIBaseURL:    strings.TrimSpace(req.OpenAIBaseURL),
		UseOpenAIBaseURL: req.OpenAIBaseURLExplicit,
	}
	if strings.TrimSpace(req.SessionID) == "" {
		return plan, nil
	}
	if strings.TrimSpace(persistenceRoot) == "" {
		return BootstrapPlan{}, errors.New("launch planner persistence root is required")
	}
	store, err := openSessionByID(persistenceRoot, req.SessionID)
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

func openSessionByID(persistenceRoot string, sessionID string) (*session.Store, error) {
	store, err := session.OpenByID(persistenceRoot, sessionID)
	if err == nil {
		return store, nil
	}
	primaryErr := err
	metadataStore, metadataErr := metadata.Open(persistenceRoot)
	if metadataErr != nil {
		return nil, fmt.Errorf("%w; metadata.Open fallback failed: %v", primaryErr, metadataErr)
	}
	defer func() { _ = metadataStore.Close() }()
	store, err = session.OpenByID(persistenceRoot, sessionID, metadataStore.AuthoritativeSessionStoreOptions()...)
	if err != nil {
		return nil, fmt.Errorf("%w; session.OpenByID fallback failed: %v", primaryErr, err)
	}
	return store, nil
}
